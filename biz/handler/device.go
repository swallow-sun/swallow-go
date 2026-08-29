// device.go 放设备注册和设备身份接口.
//
// 做的事情:
//  1. RegisterDevice:主人认证后注册设备,返回只出现一次的设备令牌.
//  2. GetCurrentDevice:使用设备令牌认证,返回当前设备公开信息.
//  3. CreateDeviceSession:设备认证后创建属于自己的对话会话.
//  4. DeviceChat:设备认证后复用共用云端聊天链路.
//  5. 解析 Device 认证头,不把设备令牌写入日志或响应错误.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	htresp "github.com/cloudwego/hertz/pkg/protocol/http1/resp"
	"github.com/swallow-sun/swallow-go/biz/service"
	"github.com/swallow-sun/swallow-go/internal/apperror"
	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/device"
	"github.com/swallow-sun/swallow-go/internal/emotion"
	"github.com/swallow-sun/swallow-go/internal/memory"
	"github.com/swallow-sun/swallow-go/internal/provider/asr"
	"github.com/swallow-sun/swallow-go/internal/provider/tts"
	"github.com/swallow-sun/swallow-go/internal/trace"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

type deviceMemorySyncItem struct {
	ID          int64  `json:"id"`
	Content     string `json:"content"`
	MemoryType  string `json:"memory_type"`
	Keywords    string `json:"keywords"`
	Status      string `json:"status"`
	SyncVersion int    `json:"sync_version"`
	UpdatedAt   int64  `json:"updated_at"`
}

type deviceMemoryTombstoneItem struct {
	MemoryID    int64 `json:"memory_id"`
	SyncVersion int   `json:"sync_version"`
	DeletedAt   int64 `json:"deleted_at"`
}

const (
	defaultDeviceCandidateLimit = 20
	maxDeviceCandidateLimit     = 50
	minCandidateDeferSeconds    = 60
	maxCandidateDeferSeconds    = 7 * 24 * 60 * 60
)

// deviceMemoryCandidateItem 是设备可见的候选字段；不返回 user_id，用户归属只由
// 已认证设备决定，也不返回内部 ORM 字段.
type deviceMemoryCandidateItem struct {
	ID            int64  `json:"id"`
	Content       string `json:"content"`
	MemoryType    string `json:"memory_type"`
	Reason        string `json:"reason"`
	UsageHint     string `json:"usage_hint"`
	Status        string `json:"status"`
	Revision      int    `json:"revision"`
	CreatedAt     int64  `json:"created_at"`
	DeferredUntil int64  `json:"deferred_until,omitempty"`
}

type deviceMemoryDecisionReq struct {
	Decision         string `json:"decision"`
	ExpectedRevision int    `json:"expected_revision"`
	DeferSeconds     int    `json:"defer_seconds,omitempty"`
}

type deviceMemoryDecisionResp struct {
	Candidate deviceMemoryCandidateItem `json:"candidate"`
	MemoryID  int64                     `json:"memory_id,omitempty"`
	Decision  string                    `json:"decision"`
	Replayed  bool                      `json:"replayed"`
}

func newDeviceMemoryCandidateItem(candidate data.MemoryCandidate) deviceMemoryCandidateItem {
	deferredUntil := int64(0)
	if !candidate.DeferredUntil.IsZero() {
		deferredUntil = candidate.DeferredUntil.UnixMilli()
	}
	return deviceMemoryCandidateItem{
		ID:            candidate.ID,
		Content:       candidate.Content,
		MemoryType:    candidate.MemoryType,
		Reason:        candidate.Reason,
		UsageHint:     candidate.UsageHint,
		Status:        candidate.Status,
		Revision:      candidate.Revision,
		CreatedAt:     candidate.CreatedAt.UnixMilli(),
		DeferredUntil: deferredUntil,
	}
}

// DeviceMemorySync GET /api/v1/device/memories/sync?since_version=0&limit=100.
// user_id 从设备令牌得到，设备不能越权拉取其他用户的记忆。
func (d *Deps) DeviceMemorySync(ctx context.Context, c *app.RequestContext) {
	ctx, _ = trace.EnsureFromHeader(ctx, string(c.GetHeader("X-Trace-Id")))
	registered, ok := d.authenticateDevice(ctx, c)
	if !ok {
		return
	}
	sinceVersion, err := strconv.Atoi(c.Query("since_version"))
	if err != nil || sinceVersion < 0 {
		writeErrorFromCtx(ctx, c, apperror.BadRequest("invalid_sync_version", "since_version must be a non-negative integer", ""))
		return
	}
	limit := 100
	if raw := c.Query("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 500 {
			writeErrorFromCtx(ctx, c, apperror.BadRequest("invalid_limit", "limit must be between 1 and 500", ""))
			return
		}
	}
	result, err := d.memory.SyncChanges(ctx, registered.UserID, sinceVersion, limit)
	if err != nil {
		logger.Error("设备记忆增量同步失败", zap.String("device_id", registered.ID), zap.Error(err))
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}
	memories := make([]deviceMemorySyncItem, 0, len(result.Memories))
	for _, item := range result.Memories {
		memories = append(memories, deviceMemorySyncItem{ID: item.ID, Content: item.Content, MemoryType: item.MemoryType, Keywords: item.Keywords, Status: item.Status, SyncVersion: item.SyncVersion, UpdatedAt: item.UpdatedAt.UnixMilli()})
	}
	tombstones := make([]deviceMemoryTombstoneItem, 0, len(result.Tombstones))
	for _, item := range result.Tombstones {
		tombstones = append(tombstones, deviceMemoryTombstoneItem{MemoryID: item.MemoryID, SyncVersion: item.SyncVersion, DeletedAt: item.DeletedAt.UnixMilli()})
	}
	c.JSON(consts.StatusOK, map[string]any{"memories": memories, "tombstones": tombstones, "next_version": result.NextVersion, "has_more": result.HasMore})
}

// DeviceListMemoryCandidates GET /api/v1/device/memory-candidates/pending?limit=20.
// 候选只能按设备令牌所属用户读取，仍在“稍后”期限内的记录由仓库自动排除.
func (d *Deps) DeviceListMemoryCandidates(ctx context.Context, c *app.RequestContext) {
	ctx, _ = trace.EnsureFromHeader(ctx, string(c.GetHeader("X-Trace-Id")))
	registered, ok := d.authenticateDevice(ctx, c)
	if !ok {
		return
	}
	limit := defaultDeviceCandidateLimit
	if raw := string(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxDeviceCandidateLimit {
			writeErrorFromCtx(ctx, c, apperror.BadRequest("invalid_limit", "limit must be between 1 and 50", ""))
			return
		}
		limit = parsed
	}
	result, err := d.memory.ListPendingCandidates(ctx, registered.UserID, limit)
	if err != nil {
		logger.Error("设备候选记忆拉取失败", zap.String("device_id", registered.ID), zap.Error(err))
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}
	items := make([]deviceMemoryCandidateItem, 0, len(result.Items))
	for _, candidate := range result.Items {
		items = append(items, newDeviceMemoryCandidateItem(candidate))
	}
	c.JSON(consts.StatusOK, map[string]any{"items": items})
}

// DeviceDecideMemoryCandidate POST /api/v1/device/memory-candidates/:id/decision.
// confirm/reject 是按候选最终状态幂等的；defer 使用 revision 防止覆盖另一台设备的更新.
func (d *Deps) DeviceDecideMemoryCandidate(ctx context.Context, c *app.RequestContext) {
	ctx, _ = trace.EnsureFromHeader(ctx, string(c.GetHeader("X-Trace-Id")))
	registered, ok := d.authenticateDevice(ctx, c)
	if !ok {
		return
	}
	candidateID, validID := parsePathID(c, "id")
	if !validID {
		writeErrorFromCtx(ctx, c, apperror.BadRequest("invalid_candidate_id", "candidate id must be a positive integer", ""))
		return
	}
	var req deviceMemoryDecisionReq
	if err := c.BindAndValidate(&req); err != nil || req.ExpectedRevision < 1 {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(memory.CandidateDecisionErrorInvalid, "invalid memory candidate decision", ""))
		return
	}

	deferredUntil := time.Time{}
	if req.Decision == memory.CandidateDecisionDefer {
		if req.DeferSeconds < minCandidateDeferSeconds || req.DeferSeconds > maxCandidateDeferSeconds {
			writeErrorFromCtx(ctx, c, apperror.BadRequest(memory.CandidateDecisionErrorInvalid, "defer_seconds must be between 60 and 604800", ""))
			return
		}
		deferredUntil = time.Now().Add(time.Duration(req.DeferSeconds) * time.Second)
	}

	result, err := d.memory.DecideCandidate(
		ctx, candidateID, registered.UserID, req.Decision, req.ExpectedRevision, registered.ID, deferredUntil,
	)
	if err != nil {
		var decisionErr *memory.CandidateDecisionError
		if errors.As(err, &decisionErr) {
			switch decisionErr.Code {
			case memory.CandidateDecisionErrorNotFound:
				writeErrorFromCtx(ctx, c, apperror.New(decisionErr.Code, "memory candidate not found", consts.StatusNotFound, false, ""))
			case memory.CandidateDecisionErrorConflict:
				writeErrorFromCtx(ctx, c, apperror.New(decisionErr.Code, "memory candidate was changed by another client", consts.StatusConflict, false, ""))
			default:
				writeErrorFromCtx(ctx, c, apperror.BadRequest(decisionErr.Code, "invalid memory candidate decision", ""))
			}
			return
		}
		if writeMemorySafetyError(ctx, c, err) {
			return
		}
		logger.Error("设备候选记忆审核失败", zap.String("device_id", registered.ID), zap.Int64("candidate_id", candidateID), zap.Error(err))
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}
	response := deviceMemoryDecisionResp{
		Candidate: newDeviceMemoryCandidateItem(result.Candidate),
		Decision:  result.Decision,
		Replayed:  result.Replayed,
	}
	if result.Memory != nil {
		response.MemoryID = result.Memory.ID
	}
	c.JSON(consts.StatusOK, response)
}

// RegisterDevice POST /api/v1/devices/register.
// 本接口必须使用主人 Bearer Token,不能使用尚未注册的设备凭据.
// 成功响应中的 token 只出现一次;后续查询设备信息不会再次返回 token 或 token_hash.
func (d *Deps) RegisterDevice(ctx context.Context, c *app.RequestContext) {
	if !d.authorizeOwner(c) {
		return
	}
	ctx, _ = trace.Ensure(ctx)
	var req registerDeviceReq
	if err := c.BindAndValidate(&req); err != nil {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(
			device.ErrorCodeInvalidRegistration, "invalid device registration body", ""))
		return
	}
	if req.Capabilities == nil {
		req.Capabilities = map[string]any{}
	}
	capabilities, err := json.Marshal(req.Capabilities)
	if err != nil {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(
			device.ErrorCodeInvalidRegistration, "invalid device capabilities", ""))
		return
	}
	result, err := d.device.RegisterDevice(ctx, req.Name, req.Platform, string(capabilities))
	if err != nil {
		var domainErr *device.DomainError
		if errors.As(err, &domainErr) {
			status := consts.StatusBadRequest
			if domainErr.Code == device.ErrorCodeNameConflict {
				status = consts.StatusConflict
			}
			writeErrorFromCtx(ctx, c, apperror.New(
				domainErr.Code, "device registration rejected", status, false, ""))
			return
		}
		logger.Error("设备注册失败", zap.Error(err))
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}
	logger.Info("设备注册完成",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", result.Device.ID),
		zap.Int64("user_id", result.Device.UserID),
	)
	c.JSON(consts.StatusCreated, registerDeviceResp{
		Device: newDevicePublicResp(result.Device),
		Token:  result.Token,
	})
}

// GetCurrentDevice GET /api/v1/devices/me.
// 请求头格式:Authorization: Device <device_id>.<token>.
func (d *Deps) GetCurrentDevice(ctx context.Context, c *app.RequestContext) {
	ctx, _ = trace.EnsureFromHeader(ctx, string(c.GetHeader("X-Trace-Id")))
	registered, ok := d.authenticateDevice(ctx, c)
	if !ok {
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"device": newDevicePublicResp(registered)})
}

// CreateDeviceSession POST /api/v1/device/session.
// 设备身份决定 userID,请求体不能指定或覆盖用户归属.
func (d *Deps) CreateDeviceSession(ctx context.Context, c *app.RequestContext) {
	ctx, _ = trace.EnsureFromHeader(ctx, string(c.GetHeader("X-Trace-Id")))
	registered, ok := d.authenticateDevice(ctx, c)
	if !ok {
		return
	}
	result, err := d.session.CreateSessionForUser(ctx, registered.UserID, registered.ID)
	if err != nil {
		logger.Error("设备会话创建失败",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", registered.ID),
			zap.Error(err),
		)
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}
	c.JSON(consts.StatusCreated, createSessionResp{
		SessionID: result.SessionID,
		UserName:  result.UserName,
		UserID:    result.UserID,
	})
}

// DeviceChat POST /api/v1/device/chat.
// 设备只负责提供 session_id、client_message_id 和 message,其余聊天业务复用现有服务端链路.
func (d *Deps) DeviceChat(ctx context.Context, c *app.RequestContext) {
	ctx, _ = trace.EnsureFromHeader(ctx, string(c.GetHeader("X-Trace-Id")))
	registered, ok := d.authenticateDevice(ctx, c)
	if !ok {
		return
	}
	d.chatForUser(ctx, c, registered.UserID, registered.ID, "device_chat", "POST /api/v1/device/chat")
}

// authenticateDevice 解析请求头并调用设备业务服务认证.
// 请求头格式固定为 Device <device_id>.<token>;任何凭据错误统一返回 401,
// 日志只记录 trace_id,认证成功后才允许记录公开的 device_id,避免泄露认证材料.
func (d *Deps) authenticateDevice(ctx context.Context, c *app.RequestContext) (data.Device, bool) {
	header := strings.TrimSpace(string(c.GetHeader("Authorization")))
	prefix := device.AuthorizationScheme + " "
	if !strings.HasPrefix(header, prefix) {
		writeErrorFromCtx(ctx, c, apperror.Unauthorized(""))
		return data.Device{}, false
	}
	credential := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	separator := strings.LastIndexByte(credential, '.')
	if separator <= 0 || separator == len(credential)-1 {
		writeErrorFromCtx(ctx, c, apperror.Unauthorized(""))
		return data.Device{}, false
	}
	registered, err := d.device.AuthenticateDevice(ctx, credential[:separator], credential[separator+1:])
	if err != nil {
		var domainErr *device.DomainError
		if errors.As(err, &domainErr) && domainErr.Code == device.ErrorCodeInvalidCredentials {
			logger.Warn("设备认证失败", zap.String("trace_id", trace.FromContext(ctx)))
			writeErrorFromCtx(ctx, c, apperror.Unauthorized(""))
			return data.Device{}, false
		}
		logger.Error("设备认证服务不可用",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.Error(err),
		)
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return data.Device{}, false
	}
	logger.Debug("设备认证成功",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", registered.ID),
		zap.Int64("user_id", registered.UserID),
	)
	return registered, true
}

// newDevicePublicResp 把设备业务对象转换成不会泄露令牌摘要的响应对象.
// capabilities_json 即使因历史脏数据无法解析也降级为空对象,不影响认证后的基础信息响应.
func newDevicePublicResp(registered data.Device) devicePublicResp {
	capabilities := map[string]any{}
	_ = json.Unmarshal([]byte(registered.CapabilitiesJSON), &capabilities)
	response := devicePublicResp{
		ID:           registered.ID,
		Name:         registered.Name,
		Platform:     registered.Platform,
		Status:       registered.Status,
		Capabilities: capabilities,
		CreatedAt:    registered.CreatedAt.Format(time.RFC3339Nano),
	}
	if registered.LastSeenAt != nil {
		response.LastSeenAt = registered.LastSeenAt.Format(time.RFC3339Nano)
	}
	return response
}

// DeviceASR POST /api/v1/device/asr.
// 设备上传音频，Go 转发给配置中显式选定的 ASR 供应商，返回识别结果。
// 请求体是 binary 音频数据 (WAV/MP3 等), 不是 JSON.
// 响应是 JSON {"text": "识别出的文字"}.
func (d *Deps) DeviceASR(ctx context.Context, c *app.RequestContext) {
	ctx, _ = trace.EnsureFromHeader(ctx, string(c.GetHeader("X-Trace-Id")))
	registered, ok := d.authenticateDevice(ctx, c)
	if !ok {
		return
	}

	// 检查 ASR Provider 是否配置. 没配置直接返回 503.
	if d.asr == nil {
		logger.Warn("ASR 服务未配置",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", registered.ID),
		)
		writeErrorFromCtx(ctx, c, apperror.ServiceUnavailable("ASR provider not configured", ""))
		return
	}

	// 从请求体读取音频数据.
	// 设备发的是 binary 音频 (WAV/MP3 等), 不是 JSON.
	// Hertz 的 Body() 返回请求体的字节切片和错误.
	audioData, err := c.Body()
	if err != nil {
		writeErrorFromCtx(ctx, c, apperror.BadRequest("invalid_audio", "failed to read audio body", ""))
		return
	}
	if len(audioData) == 0 {
		writeErrorFromCtx(ctx, c, apperror.BadRequest("invalid_audio", "audio body is empty", ""))
		return
	}

	// 从 query 参数推断音频格式, 默认 wav.
	audioFormat := c.Query("format")
	if audioFormat == "" {
		audioFormat = "wav"
	}

	// 调 ASR Provider 做语音识别.
	resp, err := d.asr.Transcribe(ctx, asr.TranscribeRequest{
		AudioData:   audioData,
		AudioFormat: audioFormat,
		// 语种由所选 Provider 的配置决定；auto 模式不会强制传 zh，
		// 可以正确处理中文、英文和中英混说。
		Language: "",
	})
	if err != nil {
		logger.Error("ASR 语音识别失败",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", registered.ID),
			zap.Int("audio_bytes", len(audioData)),
			zap.Error(err),
		)
		var providerErr *asr.ProviderError
		if errors.As(err, &providerErr) {
			switch providerErr.Kind {
			case asr.ProviderErrorInvalidInput:
				writeErrorFromCtx(ctx, c, apperror.BadRequest(
					apperror.CodeInvalidAudio, "audio format or content is invalid", "",
				))
			case asr.ProviderErrorRateLimited:
				writeErrorFromCtx(ctx, c, apperror.New(
					apperror.CodeASRRateLimited,
					"ASR service rate limited",
					consts.StatusTooManyRequests,
					true,
					"",
				))
			case asr.ProviderErrorAuthentication:
				// 设备认证本身已经通过；这里是服务端保存的上游密钥失效，
				// 对设备返回不可重试的配置错误，不能误报成 401。
				writeErrorFromCtx(ctx, c, apperror.New(
					apperror.CodeASRConfiguration,
					"ASR service is not configured correctly",
					consts.StatusServiceUnavailable,
					false,
					"",
				))
			default:
				writeErrorFromCtx(ctx, c, apperror.ServiceUnavailable("ASR service unavailable", ""))
			}
			return
		}
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}

	logger.Debug("ASR 语音识别完成",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", registered.ID),
		zap.Int("audio_bytes", len(audioData)),
		zap.Int("text_chars", len(resp.Text)),
	)

	// 返回识别文字
	c.JSON(consts.StatusOK, deviceASRResp{
		Text:     resp.Text,
		Language: resp.Language,
		Emotion:  resp.Emotion,
		Duration: resp.Duration,
	})
}

// DeviceRuntimeConfig GET /api/v1/device/runtime-config。
// 设备启动后拉取播放策略，使 config.local.toml 中的参数真正控制 C++ 客户端。
// 这里只返回非敏感配置，绝不返回供应商地址、API Key 或设备凭证。
func (d *Deps) DeviceRuntimeConfig(ctx context.Context, c *app.RequestContext) {
	ctx, _ = trace.EnsureFromHeader(ctx, string(c.GetHeader("X-Trace-Id")))
	registered, ok := d.authenticateDevice(ctx, c)
	if !ok {
		return
	}
	if d.config == nil {
		writeErrorFromCtx(ctx, c, apperror.ServiceUnavailable("runtime config unavailable", ""))
		return
	}

	logger.Debug("设备运行配置已下发",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", registered.ID),
		zap.String("tts_playback_mode", d.config.TTS.PlaybackMode),
	)
	c.JSON(consts.StatusOK, deviceRuntimeConfigResp{
		TTSPlayback: deviceTTSPlaybackConfigResp{
			Mode:                  d.config.TTS.PlaybackMode,
			MaxSynthesisUnitBytes: d.config.TTS.MaxSynthesisUnitBytes,
			FinalPaddingMs:        d.config.TTS.FinalPaddingMs,
			CrossfadeMs:           d.config.TTS.CrossfadeMs,
			StartPrebufferMs:      d.config.TTS.StartPrebufferMs,
			RecoveryPrebufferMs:   d.config.TTS.RecoveryPrebufferMs,
		},
	})
}

// DeviceTTS POST /api/v1/device/tts.
// 设备发送要合成语音的文字, Go 转发给 TTS 供应商 (硅基流动 CosyVoice2), 返回 WAV 音频.
// 请求体是 JSON {"text": "要合成的文字"}.
// 响应 body 是 binary WAV 音频, Content-Type: audio/wav.
//
// 情感语音处理: C++ 发来的 text 是 LLM 完整回复, 末尾可能带 <tags> JSON 块.
// handler 做三件事:
//  1. 解析 <tags> 块, 提取 assistant_tone (助手语气标签).
//  2. 剥离 <tags> 块, 只把纯净回复文本发给 TTS, 避免 TTS 把 JSON 当文字朗读.
//  3. 根据 assistant_tone 在文本前加 CosyVoice2 情感指令 (如 "用温和的语气说 <|endofprompt|>文本"),
//     让合成语音带有对应情绪色彩.
func (d *Deps) DeviceTTS(ctx context.Context, c *app.RequestContext) {
	ctx, _ = trace.EnsureFromHeader(ctx, string(c.GetHeader("X-Trace-Id")))
	registered, ok := d.authenticateDevice(ctx, c)
	if !ok {
		return
	}

	// 检查 TTS Provider 是否配置. 没配置直接返回 503.
	if d.tts == nil {
		logger.Warn("TTS 服务未配置",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", registered.ID),
		)
		writeErrorFromCtx(ctx, c, apperror.ServiceUnavailable("TTS provider not configured", ""))
		return
	}

	// 解析 JSON 请求体.
	var req deviceTTSReq
	if err := c.BindAndValidate(&req); err != nil {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeInvalidRequestBody, "invalid request body", ""))
		return
	}
	if req.Text == "" {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeMissingContent, "text is required", ""))
		return
	}
	// 限制文本长度, 防止撑爆 TTS 服务.
	if len(req.Text) > MaxMessageLength {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeMessageTooLong, "text is too long", ""))
		return
	}

	// 解析 <tags> 块, 提取 assistant_tone, 剥离 tags 得到纯净文本.
	cleanText, tone := emotion.StripTagsAndTone(req.Text)

	logger.Info("DeviceTTS: 标签已剥离",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", registered.ID),
		zap.Int("raw_text_chars", len(req.Text)),
		zap.Int("clean_text_chars", len(cleanText)),
		zap.String("tone", tone),
	)

	// 根据 assistant_tone 拼装 CosyVoice2 情感前缀.
	ttsText := tts.ApplyTonePrefix(cleanText, tone)

	logger.Info("DeviceTTS: 发送给 TTS 服务",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", registered.ID),
		zap.String("tone", tone),
		zap.Int("tts_text_chars", len(ttsText)),
		zap.String("tts_text_preview", truncStr(ttsText, 80)),
	)

	// 调 TTS Provider 做语音合成.
	resp, err := d.tts.Synthesize(ctx, tts.SynthesizeRequest{
		Text: ttsText,
		Tone: tone,
	})
	if err != nil {
		logger.Error("TTS 合成失败",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", registered.ID),
			zap.Int("text_chars", len(req.Text)),
			zap.String("tone", tone),
			zap.Error(err),
		)
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}

	logger.Info("DeviceTTS: 合成完成",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", registered.ID),
		zap.Int("text_chars", len(req.Text)),
		zap.Int("clean_text_chars", len(cleanText)),
		zap.String("tone", tone),
		zap.Int("audio_bytes", len(resp.AudioData)),
	)

	// 返回音频数据 (binary WAV, 不是 JSON).
	// 设备拿到 WAV 字节后用 waveOut API 直接播放.
	c.Data(consts.StatusOK, "audio/wav", resp.AudioData)
}

// DeviceTTSStream POST /api/v1/device/tts/stream.
// 流式 TTS: 设备发送单句文本, Go 转发给 TTS 供应商, 逐块返回 PCM 数据.
// 响应是 streaming binary: 先 44 字节 WAV 头, 再逐块 PCM16 数据.
// 和 /device/tts 的区别: 不等整句合成完, 边生成边返回, 首包延迟从 7-18s 降到 2-3s.
//
// 降级机制:
//   - 如果 TTS provider 实现了 StreamProvider (如 CosyVoice2), 走真正的流式合成.
//   - 如果只实现了 Provider (如 SiliconFlow/Edge), 自动降级:
//     调 Synthesize 拿完整 WAV, 通过 streaming 响应一次性发送.
//     C++ 侧按流式协议解析 (WAV 头 + PCM), 无需任何改动.
func (d *Deps) DeviceTTSStream(ctx context.Context, c *app.RequestContext) {
	ctx, _ = trace.EnsureFromHeader(ctx, string(c.GetHeader("X-Trace-Id")))
	registered, ok := d.authenticateDevice(ctx, c)
	if !ok {
		return
	}

	// 检查 TTS Provider 是否配置.
	if d.tts == nil {
		logger.Warn("TTS 服务未配置",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", registered.ID),
		)
		writeErrorFromCtx(ctx, c, apperror.ServiceUnavailable("TTS provider not configured", ""))
		return
	}

	// 解析 JSON 请求体.
	var req deviceTTSReq
	if err := c.BindAndValidate(&req); err != nil {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeInvalidRequestBody, "invalid request body", ""))
		return
	}
	if req.Text == "" {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeMissingContent, "text is required", ""))
		return
	}
	if len(req.Text) > MaxMessageLength {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeMessageTooLong, "text is too long", ""))
		return
	}

	// 语气提取:
	// 流式端点 C++ 发来的是已剥离 tags 的干净句子 + 可选 tone 字段.
	// 如果请求带了 tone, 直接用 ApplyTonePrefix 拼; 没带才走 StripTagsAndTone 提取.
	// 非流式 DeviceTTS 不变 (总是 StripTagsAndTone), 因为 C++ 发的是完整含 tags 文本.
	var ttsText string
	var tone string
	if req.Tone != "" {
		ttsText = tts.ApplyProsodyPrefix(req.Text, req.Tone, req.SpeakingRate)
		tone = req.Tone
	} else {
		cleanText, extractedTone := emotion.StripTagsAndTone(req.Text)
		ttsText = tts.ApplyProsodyPrefix(cleanText, extractedTone, req.SpeakingRate)
		tone = extractedTone
	}

	logger.Info("DeviceTTSStream: 请求已解析",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", registered.ID),
		zap.Int("raw_text_chars", len(req.Text)),
		zap.String("tone", tone),
		zap.Float64("speaking_rate", req.SpeakingRate),
		zap.String("tts_text_preview", truncStr(ttsText, 80)),
	)

	// 设置响应头, 用 streaming binary.
	// 不手动设 Transfer-Encoding: chunked, chunkedBodyWriter 的 WriteHeader
	// 会自动设 Content-Length: -1 (HTTP 约定 = chunked encoding).
	c.Response.Header.SetContentType("audio/wav")
	c.Response.SetStatusCode(consts.StatusOK)

	// 检查 provider 是否支持流式.
	streamProvider, canStream := d.tts.(tts.StreamProvider)

	if canStream {
		// 真正的流式合成: CosyVoice2 等支持 StreamSynthesize 的 provider.
		d.streamTTS(ctx, c, streamProvider, ttsText, tone, registered.ID)
	} else {
		// 降级: provider 不支持流式 (SiliconFlow/Edge), 调 Synthesize 拿完整 WAV 再通过 streaming 响应发送.
		d.fallbackStreamTTS(ctx, c, d.tts, ttsText, tone, registered.ID)
	}
}

// streamTTS 处理真正支持流式的 provider (如 CosyVoice2).
// 从 StreamSynthesize 拿到 reader, 逐块读取并写入响应.
func (d *Deps) streamTTS(ctx context.Context, c *app.RequestContext,
	streamProvider tts.StreamProvider, ttsText, tone, deviceID string) {

	reader, err := streamProvider.StreamSynthesize(ctx, tts.SynthesizeRequest{
		Text: ttsText,
		Tone: tone,
	})
	if err != nil {
		logger.Error("TTS 流式合成失败",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", deviceID),
			zap.String("tone", tone),
			zap.Error(err),
		)
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}
	defer reader.Close()

	// Hijack 响应: 用 chunkedBodyWriter 直接写网络.
	// c.Write() 只是把数据追加到内存 buffer (AppendBody), c.Flush() 在没设
	// hijack writer 时是空操作 (返回 nil). 结果所有 PCM 攒在内存里, handler
	// return 后才一次性发给 C++ 端, 完全失去流式效果.
	// chunkedBodyWriter 的 Write 把数据按 HTTP chunked encoding 写到网络,
	// Flush 立即推送给客户端. handler return 时框架调 Finalize 写结束 chunk.
	w := htresp.NewChunkedBodyWriter(&c.Response, c.GetWriter())
	c.Response.HijackWriter(w)

	logger.Info("DeviceTTSStream: 流式合成已开始",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", deviceID),
		zap.String("tone", tone),
		zap.String("tts_text_preview", truncStr(ttsText, 60)),
	)

	// 从 reader 读取音频数据, 逐块写入响应.
	// reader 先发 44 字节 WAV 头, 再逐块发 PCM16, 直接透传给客户端.
	// 每次 w.Write + w.Flush 立即把数据推到 TCP, C++ 端边收边播.
	totalBytes := 0
	chunkCount := 0
	startTime := time.Now()
	buf := make([]byte, 8192)
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			w.Flush()
			totalBytes += n
			chunkCount++
			if chunkCount == 1 {
				logger.Info("DeviceTTSStream: 首包已发送",
					zap.String("trace_id", trace.FromContext(ctx)),
					zap.String("device_id", deviceID),
					zap.Int("bytes", n),
					zap.Duration("latency", time.Since(startTime)),
				)
			}
		}
		if readErr != nil {
			break
		}
	}

	logger.Info("DeviceTTSStream: 流式合成完成",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", deviceID),
		zap.String("tone", tone),
		zap.Int("total_bytes", totalBytes),
		zap.Int("chunk_count", chunkCount),
		zap.Duration("elapsed", time.Since(startTime)),
	)
}

// fallbackStreamTTS 处理不支持流式的 provider (如 SiliconFlow/Edge).
// 调 Synthesize 拿完整 WAV, 通过 streaming 响应一次性发送给客户端.
// C++ 侧按流式协议解析 (WAV 头 + PCM), 无需任何改动.
func (d *Deps) fallbackStreamTTS(ctx context.Context, c *app.RequestContext,
	provider tts.Provider, ttsText, tone, deviceID string) {

	logger.Info("DeviceTTSStream: 降级模式（provider 不支持流式）",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", deviceID),
		zap.String("tone", tone),
		zap.Int("tts_text_chars", len(ttsText)),
	)

	startTime := time.Now()

	synthResp, err := provider.Synthesize(ctx, tts.SynthesizeRequest{
		Text: ttsText,
		Tone: tone,
	})
	if err != nil {
		logger.Error("DeviceTTSStream: 降级合成失败",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", deviceID),
			zap.String("tone", tone),
			zap.Error(err),
		)
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}

	logger.Info("DeviceTTSStream: 降级合成完成",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", deviceID),
		zap.String("tone", tone),
		zap.Int("audio_bytes", len(synthResp.AudioData)),
		zap.Duration("latency", time.Since(startTime)),
	)

	// Hijack 响应: 用 chunkedBodyWriter 写网络, 和 streamTTS 保持一致.
	w := htresp.NewChunkedBodyWriter(&c.Response, c.GetWriter())
	c.Response.HijackWriter(w)

	// 一次性发送完整 WAV 数据通过 chunked encoding.
	// C++ 侧 streamSynthesizeSpeech 会先解析 44 字节 WAV 头, 再逐块读 PCM, 兼容.
	w.Write(synthResp.AudioData)
	w.Flush()

	logger.Info("DeviceTTSStream: 降级数据已发送",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", deviceID),
		zap.Int("total_bytes", len(synthResp.AudioData)),
		zap.Duration("elapsed", time.Since(startTime)),
	)
}

// deviceSyncItem 是 POST /api/v1/device/sync 请求体里的一条同步条目.
// 对应 C++ 侧 SyncOutboxItem 的 JSON 序列化形式.
type deviceSyncItem struct {
	ItemID   string `json:"item_id"`
	ItemType string `json:"item_type"`
	Payload  string `json:"payload"`
}

// deviceSyncReq 是 POST /api/v1/device/sync 的请求体.
// 设备把 sync_outbox 里的待同步条目批量上报给服务端.
type deviceSyncReq struct {
	Items []deviceSyncItem `json:"items"`
}

// deviceSyncResp 是 POST /api/v1/device/sync 的响应体.
// Acknowledged 里的 item_id 表示服务端已确认接收, 设备可以从 outbox 删除.
// Failed 里的 item_id 表示处理失败, 设备保留这些条目等待下次重试.
type deviceSyncResp struct {
	Acknowledged []string `json:"acknowledged"`
	Failed       []string `json:"failed,omitempty"`
}

// DeviceSync POST /api/v1/device/sync.
// 设备把 sync_outbox 里的条目批量上报给服务端, 服务端做幂等入库后返回已确认列表.
// 设备收到响应后把 acknowledged 里的条目从 sync_outbox 删除.
func (d *Deps) DeviceSync(ctx context.Context, c *app.RequestContext) {
	ctx, _ = trace.EnsureFromHeader(ctx, string(c.GetHeader("X-Trace-Id")))
	registered, ok := d.authenticateDevice(ctx, c)
	if !ok {
		return
	}

	var req deviceSyncReq
	if err := c.BindAndValidate(&req); err != nil {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeInvalidRequestBody, "invalid request body", ""))
		return
	}

	// 限制单次上报条目数量, 防止超大请求.
	if len(req.Items) > 100 {
		writeErrorFromCtx(ctx, c, apperror.BadRequest("too_many_items", "max 100 items per batch", ""))
		return
	}

	// 空批次直接返回空确认列表.
	if len(req.Items) == 0 {
		c.JSON(consts.StatusOK, deviceSyncResp{
			Acknowledged: []string{},
		})
		return
	}

	// 转换为 service 层的类型.
	svcItems := make([]service.SyncBatchItem, len(req.Items))
	for i, item := range req.Items {
		svcItems[i] = service.SyncBatchItem{
			ItemID:   item.ItemID,
			ItemType: item.ItemType,
			Payload:  item.Payload,
		}
	}

	result, err := d.deviceSync.SyncBatch(ctx, registered.ID, registered.UserID, svcItems)
	if err != nil {
		logger.Error("设备同步失败",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", registered.ID),
			zap.Int("item_count", len(req.Items)),
			zap.Error(err),
		)
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}

	logger.Debug("设备同步完成",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", registered.ID),
		zap.Int("acknowledged", len(result.Acknowledged)),
		zap.Int("failed", len(result.Failed)),
	)

	c.JSON(consts.StatusOK, deviceSyncResp{
		Acknowledged: result.Acknowledged,
		Failed:       result.Failed,
	})
}
