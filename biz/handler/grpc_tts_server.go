// grpc_tts_server.go 是 gRPC TTS 服务端实现.
//
// 做的事情:
//  1. 实现 proto/tts.proto 定义的 TTS 服务 (Synthesize + StreamSynthesize).
//  2. 复用 handler.Deps 里的 TTS Provider, 和 HTTP handler 共用同一套 provider 逻辑.
//  3. 认证: 从 gRPC metadata 提取 Authorization 头, 复用 device.AuthenticateDevice.
//
// 和 HTTP handler 的关系:
//   - HTTP /api/v1/device/tts       → gRPC TTS.Synthesize
//   - HTTP /api/v1/device/tts/stream → gRPC TTS.StreamSynthesize
//   - 聊天 SSE 暂不走 gRPC, 仍然用 HTTP.
package handler

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/device"
	"github.com/swallow-sun/swallow-go/internal/emotion"
	"github.com/swallow-sun/swallow-go/internal/provider/tts"
	"github.com/swallow-sun/swallow-go/internal/trace"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	pb "github.com/swallow-sun/swallow-go/proto/ttspb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// TTSServer 实现 proto 定义的 TTS gRPC 服务.
// 嵌入 pb.UnimplementedTTSServer 保证 proto 新增方法时有默认实现.
type TTSServer struct {
	pb.UnimplementedTTSServer

	// deps 持有 handler 层的所有依赖, 复用 HTTP handler 的 TTS Provider 和设备认证.
	deps *Deps
}

// NewTTSServer 创建 gRPC TTS 服务实例.
// deps 是 handler.NewDeps 返回的依赖集合, 和 HTTP handler 共用.
func NewTTSServer(deps *Deps) *TTSServer {
	return &TTSServer{deps: deps}
}

// RegisterGRPC 把 TTS gRPC 服务注册到 grpc.Server 上.
// 在 main.go 里调一次, 和 HTTP 路由注册是平行的.
func RegisterTTSServer(s *grpc.Server, deps *Deps) {
	pb.RegisterTTSServer(s, NewTTSServer(deps))
}

// authenticateGRPCDevice 从 gRPC metadata 提取 Authorization 头并认证设备.
// 和 HTTP handler 的 authenticateDevice 逻辑完全一致, 只是从 metadata 而非 HTTP header 取值.
// 认证失败返回 gRPC Unauthenticated 错误.
func authenticateGRPCDevice(ctx context.Context, deps *Deps) (data.Device, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return data.Device{}, status.Error(codes.Unauthenticated, "missing metadata")
	}

	values := md.Get("authorization")
	if len(values) == 0 {
		return data.Device{}, status.Error(codes.Unauthenticated, "missing authorization header")
	}

	header := strings.TrimSpace(values[0])
	prefix := device.AuthorizationScheme + " "
	if !strings.HasPrefix(header, prefix) {
		return data.Device{}, status.Error(codes.Unauthenticated, "invalid authorization scheme")
	}

	credential := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	separator := strings.LastIndexByte(credential, '.')
	if separator <= 0 || separator == len(credential)-1 {
		return data.Device{}, status.Error(codes.Unauthenticated, "invalid credential format")
	}

	// 从 metadata 提取 trace ID, 注入 context.
	traceID := ""
	if tv := md.Get("x-trace-id"); len(tv) > 0 {
		traceID = tv[0]
	}
	ctx, _ = trace.EnsureFromHeader(ctx, traceID)

	registered, err := deps.device.AuthenticateDevice(ctx, credential[:separator], credential[separator+1:])
	if err != nil {
		var domainErr *device.DomainError
		if errors.As(err, &domainErr) && domainErr.Code == device.ErrorCodeInvalidCredentials {
			logger.Warn("gRPC 设备认证失败", zap.String("trace_id", traceID))
			return data.Device{}, status.Error(codes.Unauthenticated, "invalid credentials")
		}
		logger.Error("gRPC 设备认证不可用", zap.String("trace_id", traceID), zap.Error(err))
		return data.Device{}, status.Error(codes.Internal, "auth unavailable")
	}

	logger.Debug("gRPC 设备认证成功",
		zap.String("trace_id", traceID),
		zap.String("device_id", registered.ID),
	)
	return registered, nil
}

// Synthesize 实现 gRPC 非流式 TTS 合成.
// 对应 HTTP POST /api/v1/device/tts.
func (s *TTSServer) Synthesize(ctx context.Context, req *pb.SynthesizeRequest) (*pb.SynthesizeResponse, error) {
	ctx, _ = trace.Ensure(ctx)

	registered, err := authenticateGRPCDevice(ctx, s.deps)
	if err != nil {
		return nil, err
	}

	if s.deps.tts == nil {
		logger.Warn("gRPC TTS: TTS 服务未配置",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", registered.ID),
		)
		return nil, status.Error(codes.Unavailable, "TTS provider not configured")
	}

	text := strings.TrimSpace(req.GetText())
	if text == "" {
		return nil, status.Error(codes.InvalidArgument, "text is required")
	}
	if len(text) > MaxMessageLength {
		return nil, status.Error(codes.InvalidArgument, "text is too long")
	}

	// 语气处理: 和 HTTP handler 逻辑一致.
	// 非流式端点: C++ 发完整含 tags 文本, 用 StripTagsAndTone 提取.
	var ttsText string
	var tone string
	if req.GetTone() != "" {
		ttsText = tts.ApplyTonePrefix(text, req.GetTone())
		tone = req.GetTone()
	} else {
		cleanText, extractedTone := emotion.StripTagsAndTone(text)
		ttsText = tts.ApplyTonePrefix(cleanText, extractedTone)
		tone = extractedTone
	}

	logger.Info("gRPC TTS: 一次性合成",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", registered.ID),
		zap.Int("text_chars", len(text)),
		zap.String("tone", tone),
		zap.String("tts_text_preview", truncStr(ttsText, 80)),
	)

	start := time.Now()
	resp, err := s.deps.tts.Synthesize(ctx, tts.SynthesizeRequest{
		Text: ttsText,
		Tone: tone,
	})
	if err != nil {
		logger.Error("gRPC TTS: 合成失败",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", registered.ID),
			zap.String("tone", tone),
			zap.Error(err),
		)
		return nil, status.Error(codes.Internal, "synthesis failed")
	}

	logger.Info("gRPC TTS: 一次性合成完成",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", registered.ID),
		zap.Int("audio_bytes", len(resp.AudioData)),
		zap.Duration("elapsed", time.Since(start)),
	)

	return &pb.SynthesizeResponse{
		AudioData:   resp.AudioData,
		AudioFormat: resp.AudioFormat,
	}, nil
}

// StreamSynthesize 实现 gRPC 流式 TTS 合成.
// 对应 HTTP POST /api/v1/device/tts/stream.
// 逐块返回 AudioChunk: 第一个是 WAV 头, 后续是裸 PCM16.
func (s *TTSServer) StreamSynthesize(req *pb.SynthesizeRequest, stream pb.TTS_StreamSynthesizeServer) error {
	ctx := stream.Context()
	ctx, _ = trace.Ensure(ctx)

	registered, err := authenticateGRPCDevice(ctx, s.deps)
	if err != nil {
		return err
	}

	if s.deps.tts == nil {
		logger.Warn("gRPC TTS: TTS 服务未配置",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", registered.ID),
		)
		return status.Error(codes.Unavailable, "TTS provider not configured")
	}

	text := strings.TrimSpace(req.GetText())
	if text == "" {
		return status.Error(codes.InvalidArgument, "text is required")
	}
	if len(text) > MaxMessageLength {
		return status.Error(codes.InvalidArgument, "text is too long")
	}

	// 语气处理: 和 HTTP handler 逻辑一致.
	// 流式端点: C++ 发干净句子 + tone 字段, 直接用 tone.
	var ttsText string
	var tone string
	if req.GetTone() != "" {
		ttsText = tts.ApplyTonePrefix(text, req.GetTone())
		tone = req.GetTone()
	} else {
		cleanText, extractedTone := emotion.StripTagsAndTone(text)
		ttsText = tts.ApplyTonePrefix(cleanText, extractedTone)
		tone = extractedTone
	}

	logger.Info("gRPC TTS: 流式合成",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", registered.ID),
		zap.Int("text_chars", len(text)),
		zap.String("tone", tone),
		zap.String("tts_text_preview", truncStr(ttsText, 80)),
	)

	// 检查 provider 是否支持流式.
	streamProvider, canStream := s.deps.tts.(tts.StreamProvider)

	if canStream {
		return s.grpcStreamTTS(ctx, stream, streamProvider, ttsText, tone, registered.ID)
	}
	return s.grpcFallbackStreamTTS(ctx, stream, s.deps.tts, ttsText, tone, registered.ID)
}

// grpcStreamTTS 处理真正支持流式的 provider (如 CosyVoice2).
// 从 StreamSynthesize 拿到 reader, 逐块读取并发送 gRPC AudioChunk.
func (s *TTSServer) grpcStreamTTS(
	ctx context.Context,
	stream pb.TTS_StreamSynthesizeServer,
	streamProvider tts.StreamProvider,
	ttsText, tone, deviceID string,
) error {
	reader, err := streamProvider.StreamSynthesize(ctx, tts.SynthesizeRequest{
		Text: ttsText,
		Tone: tone,
	})
	if err != nil {
		logger.Error("gRPC TTS: 流式合成失败",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", deviceID),
			zap.String("tone", tone),
			zap.Error(err),
		)
		return status.Error(codes.Internal, "stream failed")
	}
	defer reader.Close()

	logger.Info("gRPC TTS: 流式合成已开始",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", deviceID),
		zap.String("tone", tone),
	)

	startTime := time.Now()

	// 上游返回的是 RIFF/WAV，但 WAV 头不是固定 44 字节。硅基流动会在 fmt
	// 和 data 之间插入 LIST/INFO/AIGC 元数据；旧代码固定跳过 44 字节，等于把
	// 元数据当 PCM 播放，因此每个句首都会出现“吱/咔”声。这里完整扫描 RIFF
	// chunk，只保留真正的 data 段。
	format, initialPCM, err := tts.ReadStreamingWAVHeader(reader)
	if err != nil {
		logger.Error("gRPC TTS: WAV 流头解析失败",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", deviceID),
			zap.Error(err),
		)
		return status.Error(codes.Internal, "invalid TTS WAV stream")
	}
	if format.AudioFormat != 1 || format.Channels != 1 ||
		format.BitsPerSample != 16 || format.BlockAlign != 2 {
		logger.Error("gRPC TTS: 不支持的 PCM 格式",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.Uint16("audio_format", format.AudioFormat),
			zap.Uint16("channels", format.Channels),
			zap.Uint16("bits_per_sample", format.BitsPerSample),
			zap.Uint16("block_align", format.BlockAlign),
		)
		return status.Error(codes.Internal, "unsupported TTS PCM format")
	}

	// 对客户端只暴露标准 44 字节头，彻底屏蔽供应商的可变元数据。
	canonicalHeader, err := tts.BuildPCM16MonoWAVHeader(int(format.SampleRate), 0)
	if err != nil {
		return status.Error(codes.Internal, "failed to build TTS WAV header")
	}
	if err := stream.Send(&pb.AudioChunk{
		Data:       canonicalHeader,
		SampleRate: int32(format.SampleRate),
	}); err != nil {
		return err
	}

	logger.Info("gRPC TTS: 标准音频头已发送",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", deviceID),
		zap.Uint32("sample_rate", format.SampleRate),
		zap.Uint16("block_align", format.BlockAlign),
		zap.Int("initial_pcm_bytes", len(initialPCM)),
		zap.Duration("latency", time.Since(startTime)),
	)

	// HTTP Read 和 gRPC message 都只是传输分片，不是音频帧。统一聚合成 8192
	// 字节 PCM16 帧；最后只允许一个对齐尾帧，绝不发送 8B/106B 之类随机小包。
	pcmStats, err := tts.StreamAlignedPCM(
		reader,
		initialPCM,
		int(format.BlockAlign),
		8192,
		func(frame []byte) error {
			return stream.Send(&pb.AudioChunk{Data: frame})
		},
	)
	if err != nil {
		logger.Error("gRPC TTS: PCM 分帧或发送失败",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", deviceID),
			zap.Error(err),
		)
		return err
	}
	if pcmStats.Bytes == 0 {
		logger.Error("gRPC TTS: 上游返回空 PCM",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", deviceID),
			zap.String("tone", tone),
		)
		// 音频头已经发送也没关系：以非 OK 的最终 gRPC status 结束后，
		// 客户端会把它识别为首包后合成失败，而不是把静音误判为成功。
		return status.Error(codes.Internal, "TTS returned empty PCM stream")
	}

	logger.Info("gRPC TTS: 流式合成完成",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", deviceID),
		zap.String("tone", tone),
		zap.Int("pcm_bytes", pcmStats.Bytes),
		zap.Int("pcm_chunks", pcmStats.Chunks),
		zap.Duration("elapsed", time.Since(startTime)),
	)
	return nil
}

// grpcFallbackStreamTTS 处理不支持流式的 provider（如 Edge/Zhipu）。
// 先用 Synthesize 拿完整 WAV，再规范化为标准头和固定 PCM 帧发送。
func (s *TTSServer) grpcFallbackStreamTTS(
	ctx context.Context,
	stream pb.TTS_StreamSynthesizeServer,
	provider tts.Provider,
	ttsText, tone, deviceID string,
) error {
	logger.Info("gRPC TTS: 降级模式（provider 不支持流式）",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", deviceID),
		zap.String("tone", tone),
	)

	startTime := time.Now()
	resp, err := provider.Synthesize(ctx, tts.SynthesizeRequest{
		Text: ttsText,
		Tone: tone,
	})
	if err != nil {
		logger.Error("gRPC TTS: 降级合成失败",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", deviceID),
			zap.String("tone", tone),
			zap.Error(err),
		)
		return status.Error(codes.Internal, "synthesis failed")
	}

	// 降级 provider 返回的也是 WAV，但头部不保证只有 44 字节。这里和真正
	// 的流式路径使用同一协议：扫描任意 RIFF 块、只保留 data PCM，再发送
	// 标准 44 字节头和固定小帧。这样既不会播放 LIST/AIGC 元数据，也不会把
	// 数 MiB 的完整 WAV 塞进单个 gRPC message 触发默认消息大小限制。
	audioReader := bytes.NewReader(resp.AudioData)
	format, initialPCM, err := tts.ReadStreamingWAVHeader(audioReader)
	if err != nil {
		logger.Error("gRPC TTS: 降级 WAV 解析失败",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", deviceID),
			zap.Error(err),
		)
		return status.Error(codes.Internal, "invalid fallback TTS WAV")
	}
	if format.AudioFormat != 1 || format.Channels != 1 ||
		format.BitsPerSample != 16 || format.BlockAlign != 2 {
		logger.Error("gRPC TTS: 降级 WAV PCM 格式不支持",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", deviceID),
			zap.Uint16("audio_format", format.AudioFormat),
			zap.Uint16("channels", format.Channels),
			zap.Uint16("bits_per_sample", format.BitsPerSample),
			zap.Uint16("block_align", format.BlockAlign),
		)
		return status.Error(codes.Internal, "unsupported fallback TTS PCM format")
	}

	canonicalHeader, err := tts.BuildPCM16MonoWAVHeader(int(format.SampleRate), 0)
	if err != nil {
		return status.Error(codes.Internal, "failed to build fallback TTS WAV header")
	}
	if err := stream.Send(&pb.AudioChunk{
		Data:       canonicalHeader,
		SampleRate: int32(format.SampleRate),
	}); err != nil {
		return err
	}

	pcmStats, err := tts.StreamAlignedPCM(
		audioReader,
		initialPCM,
		int(format.BlockAlign),
		8192,
		func(frame []byte) error {
			return stream.Send(&pb.AudioChunk{Data: frame})
		},
	)
	if err != nil {
		logger.Error("gRPC TTS: 降级 PCM 分帧或发送失败",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", deviceID),
			zap.Error(err),
		)
		return err
	}
	if pcmStats.Bytes == 0 {
		logger.Error("gRPC TTS: 降级 provider 返回空 PCM",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", deviceID),
			zap.String("tone", tone),
		)
		return status.Error(codes.Internal, "fallback TTS returned empty PCM stream")
	}

	logger.Info("gRPC TTS: 降级合成完成",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", deviceID),
		zap.Uint32("sample_rate", format.SampleRate),
		zap.Int("pcm_bytes", pcmStats.Bytes),
		zap.Int("pcm_chunks", pcmStats.Chunks),
		zap.Duration("elapsed", time.Since(startTime)),
	)
	return nil
}
