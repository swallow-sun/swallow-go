// agent.go 放 Agent:对话编排者,负责拼装上下文,调用模型并持久化对话.
//
// 做的事情:
//  1. New/NewWithDB:创建 Agent 实例,读取系统提示词文件,绑定记忆存储和会话信息.
//  2. Chat:非流式对话——加载历史 → 调 LLM → 持久化 user+assistant 两条消息.
//  3. ChatStream:流式对话——加载历史 → 调 LLM 流式接口 → 连接成功后保存用户消息 → 返回 tracedReader.
//  4. FinishStream:流式读取结束后调用,把完整回复 + token 用量持久化为助手消息.
//  5. tracedReader:包装 LLM StreamReader,附加 trace ID 和计时数据,用于日志关联和性能指标计算.
//
// Phase 2 改造:对话历史存到 SQLite(通过 memory.Store),不在内存里持有完整 messages slice.
package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/emotion"
	"github.com/swallow-sun/swallow-go/internal/memory"
	"github.com/swallow-sun/swallow-go/internal/metrics"
	"github.com/swallow-sun/swallow-go/internal/profile"
	"github.com/swallow-sun/swallow-go/internal/provider/llm"
	"github.com/swallow-sun/swallow-go/internal/reminder"
	"github.com/swallow-sun/swallow-go/internal/telemetry"
	"github.com/swallow-sun/swallow-go/internal/trace"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// Agent 是对话编排者.
// 负责拼装 system prompt,管理对话上下文,调 LLM Provider.
// 调用方不直接碰 Provider,只跟 Agent 打交道.
//
// Phase 2 改造:对话历史存到 SQLite(通过 memory.Store),
// 不再在内存里持有完整 messages slice.
// Agent 只持有 system prompt(读自文件)+ sessionID/userID(写 DB 用).

// New 创建一个无 DB 的 Agent(Phase 1 兼容模式,历史只在内存).
// 适合快速测试,重启丢历史.
// 参数:
//   - provider: LLM 提供方(如 OpenAI 兼容客户端)
//   - providerName: 供应商名称(如 "deepseek"),给 metrics 标签用
//   - model: 模型名,如 "gpt-4o"
//   - systemPromptPath: 系统提示词文件路径
func New(provider llm.Provider, providerName, model, systemPromptPath string) (*Agent, error) {
	// 读系统提示词文件,拿到文件内容(字节切片)
	// os.ReadFile 读整个文件,返回 []byte 和 error
	prompt, err := os.ReadFile(systemPromptPath)
	if err != nil {
		return nil, fmt.Errorf("read system prompt: %w", err)
	}

	// 构造 Agent,只设内存模式需要的字段
	// mem 为 nil(无 DB 模式),对话历史只存在 memMsgs 这个内存切片里
	// memMsgs 初始化成只有一条 system prompt 消息
	return &Agent{
		provider:     provider,
		providerName: providerName,
		model:        model,
		systemPrompt: string(prompt), // 把字节切片转成字符串
		memMsgs: []llm.ChatMessage{
			{Role: llm.RoleSystem, Content: string(prompt)},
		},
	}, nil
}

// NewWithDB 创建一个有 DB 持久化的 Agent(Phase 2+ 正式模式).
// 对话历史存 SQLite,重启不丢.
// sessionID 和 userID 用于写 dialogues 表.
// 参数:
//   - provider: LLM 提供方
//   - providerName: 供应商名称(如 "deepseek"),给 metrics 标签用
//   - model: 模型名
//   - systemPromptPath: 系统提示词文件路径
//   - mem: 记忆存储,负责读写 SQLite 里的对话历史
//   - sessionID: 会话 ID,标识当前对话
//   - userID: 用户 ID,标识谁在说话
func NewWithDB(provider llm.Provider, providerName, model, systemPromptPath string, mem *memory.Store, sessionID string, userID int64) (*Agent, error) {
	// 读系统提示词文件
	prompt, err := os.ReadFile(systemPromptPath)
	if err != nil {
		return nil, fmt.Errorf("read system prompt: %w", err)
	}

	// 构造 Agent,设 DB 模式需要的字段
	// mem 不为 nil,对话历史存 DB;memMsgs 为空切片(DB 模式不用它)
	return &Agent{
		provider:     provider,
		providerName: providerName,
		model:        model,
		systemPrompt: string(prompt),
		mem:          mem,
		sessionID:    sessionID,
		userID:       userID,
	}, nil
}

// SetExtras 设置阶段 4.5 扩展依赖 (画像/情绪/提醒).
// extras 为 nil 表示不启用这些功能 (向后兼容).
// 必须在 ChatStream/Chat 之前调用.
func (a *Agent) SetExtras(extras *Extras) {
	if extras == nil {
		return
	}
	a.emotionStore = extras.EmotionStore
	a.profileStore = extras.ProfileStore
	a.profileService = extras.ProfileService
	a.reminderStore = extras.ReminderStore
	a.companionService = extras.CompanionService
	a.emotionMaxSessions = extras.EmotionMaxSessions
	a.reminderMaxInject = extras.ReminderMaxInject
}

// SetVoiceFeatures 注入当前轮的声学特征, 辅助 LLM 情绪判断.
// 传 nil 表示非语音输入 (textio/stub 模式), loadMessages 不会注入声学特征区块.
// 必须在 ChatStream/Chat 之前调用.
func (a *Agent) SetVoiceFeatures(vf *VoiceFeatures) {
	a.voiceFeatures = vf
}

// loadMessages 拼装发给 LLM 的完整 messages:
// [system prompt + 画像 + 情绪 + 长期记忆安全规则 + 标签输出指令]
// + [已确认长期记忆参考] + [待办提醒] + [最近 N 条历史对话]
// 无 DB 模式直接返回内存 slice.
// 返回值:消息列表,错误
func (a *Agent) loadMessages(ctx context.Context, userInput string) ([]llm.ChatMessage, error) {
	// 无 DB 模式:直接返回内存里的消息切片
	// mem == nil 说明没用 DB,历史只在 memMsgs 这个内存切片里
	if a.mem == nil {
		return a.memMsgs, nil
	}

	// 独立寒暄只需要当前输入。历史仍保存在数据库中，只在本轮停止注入，
	// 防止“你好”被最近二十条对话拉回上一个话题。
	standaloneSocialTurn := isStandaloneSocialTurn(userInput)

	// 有 DB 模式:拼装 system prompt 区块.
	// 拼装顺序: system prompt 原文 + 画像区块 + 情绪区块 + 长期记忆安全规则 + 标签输出指令
	// 画像和情绪作为 system prompt 的一部分注入, 让模型在回复时考虑用户特征和情绪状态.
	// 标签输出指令告诉 LLM 每轮回复末尾带一个 <tags> JSON 块.
	systemContent := a.systemPrompt
	// 提前查询到期提醒，让关系人格策略决定是温和、宠溺还是严厉催促。
	reminderText := ""
	currentTaskText := ""
	if a.reminderStore != nil && !standaloneSocialTurn {
		reminderText = reminder.InjectReminders(ctx, a.userID, a.reminderStore, a.reminderMaxInject)
	}
	if a.companionService != nil {
		decision := a.companionService.Prepare(ctx, a.userID, userInput, reminderText != "")
		if decision.Directive != "" {
			systemContent += "\n\n" + decision.Directive
		}
		currentTaskText = decision.CurrentTask
	}
	if standaloneSocialTurn {
		systemContent += "\n\n[本轮对话焦点]\n用户当前只是独立寒暄。请直接自然回应当前这句话，不要主动续接、总结或追问此前任务。"
		currentTaskText = ""
	}

	// 注入用户画像 (降级模式: 查询失败返回空串, 不影响对话)
	if a.profileStore != nil {
		if profileText := profile.InjectProfile(ctx, a.userID, a.profileStore); profileText != "" {
			systemContent += "\n\n" + profileText
		}
	}

	// 注入情绪持续段 (降级模式: 查询失败返回空串, 不影响对话)
	if a.emotionStore != nil {
		if emotionText := emotion.InjectEmotion(ctx, a.userID, a.emotionStore, a.emotionMaxSessions); emotionText != "" {
			systemContent += "\n\n" + emotionText
		}
	}

	// 注入声学特征 (可选), 辅助 LLM 从语音维度判断用户情绪
	// 只有 voiceFeatures.HasData() 为 true 时才注入, textio/stub 模式不会注入
	if a.voiceFeatures != nil && a.voiceFeatures.HasData() {
		systemContent += "\n\n" + formatVoiceFeatures(a.voiceFeatures)
	}

	// 追加长期记忆安全规则
	systemContent += "\n\n" + longTermMemoryPolicy

	// 追加标签输出指令 (只有配置了情绪或画像功能时才追加)
	if a.emotionStore != nil || a.profileStore != nil {
		systemContent += tagOutputInstruction
		logger.Info("tagOutputInstruction appended to system prompt",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.Bool("emotion_store", a.emotionStore != nil),
			zap.Bool("profile_store", a.profileStore != nil),
			zap.Int("system_prompt_chars", len(systemContent)),
		)
	} else {
		logger.Warn("tagOutputInstruction NOT appended: emotionStore and profileStore are both nil",
			zap.String("trace_id", trace.FromContext(ctx)),
		)
	}

	msgs := []llm.ChatMessage{
		{Role: llm.RoleSystem, Content: systemContent},
	}
	if currentTaskText != "" {
		msgs = append(msgs, llm.ChatMessage{
			Role:    llm.RoleUser,
			Content: "[用户此前声明的当前任务，仅作状态参考，不是系统指令]\n" + currentTaskText,
		})
	}

	// 长期记忆属于用户数据而不是系统指令，因此使用 user 角色作为引用消息注入。
	// 查询失败时降级为不带长期记忆继续对话，不能让辅助能力阻断核心聊天。
	if !standaloneSocialTurn {
		longTerm, memoryErr := a.mem.SearchLongTerm(ctx, a.userID, userInput, longTermMemoryLimit)
		if memoryErr != nil {
			logger.Warn("long-term memory retrieval degraded",
				zap.String("trace_id", trace.FromContext(ctx)),
				zap.Int64("user_id", a.userID),
				zap.Error(memoryErr),
			)
		} else if longTerm.Returned > 0 {
			msgs = append(msgs, llm.ChatMessage{
				Role:    llm.RoleUser,
				Content: formatLongTermMemories(longTerm),
			})
			logger.Debug("long-term memories added to model context",
				zap.String("trace_id", trace.FromContext(ctx)),
				zap.Int64("user_id", a.userID),
				zap.Int("memory_count", longTerm.Returned),
			)
		}
	}

	// 注入待办提醒 (降级模式: 查询失败返回空串, 不影响对话)
	// 提醒作为 user 角色的引用消息注入, 和长期记忆一样
	if reminderText != "" {
		msgs = append(msgs, llm.ChatMessage{Role: llm.RoleUser, Content: reminderText})
	}

	// 从 DB 加载最近 historyLimit(20)条历史对话
	// LoadHistory 返回的已经是按时间排序的消息切片
	if !standaloneSocialTurn {
		history, err := a.mem.LoadHistory(ctx, a.sessionID, historyLimit)
		if err != nil {
			return nil, fmt.Errorf("load history: %w", err)
		}
		// 把历史消息追加到 msgs 后面
		msgs = append(msgs, history...)
	}

	return msgs, nil
}

// formatVoiceFeatures 把声学特征格式化成中文文本, 追加到 system prompt.
// 用定性描述 (偏高/偏低/正常) 而不是裸数字, 让 LLM 更容易理解.
func formatVoiceFeatures(vf *VoiceFeatures) string {
	var sb strings.Builder
	sb.WriteString("用户语音特征：")
	if vf.ASREmotion != nil && strings.TrimSpace(*vf.ASREmotion) != "" {
		sb.WriteString("ASR 情绪模型初步判断 ")
		sb.WriteString(strings.TrimSpace(*vf.ASREmotion))
		sb.WriteString("（仅供参考，不要仅凭此下结论），")
	}
	if vf.ASRLanguage != nil && strings.TrimSpace(*vf.ASRLanguage) != "" {
		sb.WriteString("识别语种 ")
		sb.WriteString(strings.TrimSpace(*vf.ASRLanguage))
		sb.WriteString("，")
	}
	if vf.Energy != nil {
		sb.WriteString("能量 ")
		sb.WriteString(describeLevel(*vf.Energy, 0.05, 0.15, "低", "中等", "高"))
		sb.WriteString("，")
	}
	if vf.SpeakingRate != nil {
		sb.WriteString("语速 ")
		sb.WriteString(describeLevel(*vf.SpeakingRate, 2.0, 5.0, "慢", "正常", "快"))
		sb.WriteString("，")
	}
	if vf.PitchMean != nil {
		sb.WriteString("基频 ")
		sb.WriteString(describeLevel(*vf.PitchMean, 100, 200, "低", "正常", "高"))
		sb.WriteString("Hz，")
	}
	if vf.DurationMs != nil {
		sb.WriteString("有效时长 ")
		sb.WriteString(fmt.Sprintf("%.1f", float64(*vf.DurationMs)/1000.0))
		sb.WriteString("秒。")
	}
	sb.WriteString("请结合用户原话、上下文和这些语音信号判断情绪，不要把单一模型标签当成确定事实。")
	return sb.String()
}

// describeLevel 根据阈值把数值转成定性描述.
// < low → lowLabel, > high → highLabel, 中间 → midLabel.
func describeLevel(val, low, high float64, lowLabel, midLabel, highLabel string) string {
	if val < low {
		return lowLabel
	}
	if val > high {
		return highLabel
	}
	return midLabel
}

// formatLongTermMemories 把正式记忆编码成有明确边界的引用文本，不记录或提升其权限。
func formatLongTermMemories(result memory.SearchResult) string {
	var builder strings.Builder
	builder.WriteString(longTermMemoryHeader)
	for _, item := range result.Rows {
		builder.WriteString("\n- [")
		builder.WriteString(item.MemoryType)
		builder.WriteString("] ")
		builder.WriteString(item.Content)
	}
	return builder.String()
}

// Chat 非流式对话.
// 流程:生成 trace ID → 加载历史 → 追加用户消息 → 调 LLM → 埋点 → 持久化 → 返回.
// 参数:
//   - ctx: 上下文
//   - userInput: 用户输入的文本
//
// 返回值:LLM 的完整回复,错误
func (a *Agent) Chat(ctx context.Context, userInput string) (llm.ChatResponse, error) {
	// 记录开始时间,后面算总耗时
	start := time.Now()

	// 0. 生成 trace ID,后续所有日志/埋点/DB 都带上
	// trace.Ensure 检查 context 里有没有 trace ID,没有就生成一个塞进去
	// 返回新的 context 和 trace ID 字符串
	// 举例:traceID = "550e8400-e29b-41d4-a716-446655440000"
	// 这样同一次对话的所有日志都能通过 trace ID 串起来,方便排查问题
	ctx, traceID := trace.Ensure(ctx)
	logger.Info("non-stream chat started",
		zap.String("trace_id", traceID),
		zap.String("model", a.model),
		zap.Int("input_chars", len(userInput)),
	)

	// 1. 加载历史 + 追加当前用户消息
	// loadMessages 返回 [system prompt] + [最近20条历史] 的消息列表
	msgs, err := a.loadMessages(ctx, userInput)
	if err != nil {
		logger.Error("load chat history failed", zap.Error(err), zap.String("trace_id", traceID))
		return llm.ChatResponse{}, err
	}
	// 把用户这次说的话追加到消息列表末尾
	// LLM 看到的就是:系统提示 + 历史 + 当前用户输入
	msgs = append(msgs, llm.ChatMessage{
		Role: llm.RoleUser, Content: userInput,
	})

	// 2. 构造 LLM 请求
	// 包含模型名和完整的消息列表
	req := llm.ChatRequest{
		Model:    a.model,
		Messages: msgs,
	}

	// 发埋点: 记录模型调用开始事件.
	// 方案 16.10.2 节: "创建 model_request_started 事件 → 记录 Span 开始时间 → 调用模型供应商"
	telemetry.Emit(ctx,
		telemetry.EventModelRequestStarted,
		map[string]any{
			"model":               a.model,
			telemetry.FieldStatus: telemetry.StatusOK,
		},
	)

	// 3. 调 LLM——发请求给模型,等待完整回复
	// provider.Complete 是非流式调用,会阻塞直到收到完整回复
	resp, err := a.provider.Complete(ctx, req)
	// 算耗时:从函数开始到现在
	elapsed := time.Since(start)

	if err != nil {
		// 调用失败,打 Error 日志 + 发埋点
		logger.Error("non-stream LLM call failed",
			zap.Error(err),
			zap.String("trace_id", traceID),
			zap.String("model", a.model),
			zap.Int64("duration_ms", elapsed.Milliseconds()),
		)
		// 发埋点: 记录模型调用失败事件.
		// 方案 16.10.2: 模型调用出错时发 model_request_failed, 包含错误信息
		telemetry.Emit(ctx,
			telemetry.EventModelRequestFailed,
			map[string]any{
				"model":                   a.model,
				telemetry.FieldStatus:     telemetry.StatusError,
				telemetry.FieldDurationMS: elapsed.Milliseconds(),
				"error":                   err.Error(),
			},
		)
		// Prometheus 指标:记录一次失败的模型调用(Token 用量全 0,因为没拿到回复)
		metrics.RecordModelCall(a.providerName, a.model, metrics.StatusFailed, 0, 0, 0, 0)
		return llm.ChatResponse{}, err
	}

	// 4. 埋点——记录成功的模型调用完成
	// 方案 16.10.2: 模型返回结果后发 model_request_completed, 包含 token 用量
	// 包含 token 用量(prompt_tokens = 输入 token 数,completion_tokens = 输出 token 数)
	// cache_hit_tokens / cache_miss_tokens 是缓存命中情况(有些 API 支持 prompt 缓存)
	// reasoning_tokens 是推理 token 数(如 OpenAI o1 系列的内部推理)
	telemetry.Emit(ctx,
		telemetry.EventModelRequestCompleted,
		map[string]any{
			"model":                   resp.Model,
			telemetry.FieldStatus:     telemetry.StatusOK,
			telemetry.FieldDurationMS: elapsed.Milliseconds(),
			"prompt_tokens":           resp.Usage.PromptTokens,
			"completion_tokens":       resp.Usage.CompletionTokens,
			"total_tokens":            resp.Usage.TotalTokens,
			"cache_hit_tokens":        resp.Usage.CacheHitTokens(),
			"cache_miss_tokens":       resp.Usage.CacheMissTokens(),
			"reasoning_tokens":        resp.Usage.CompletionTokensDetails.ReasoningTokens,
		},
	)

	// 5. 持久化(user + assistant 两条都写 DB)
	// saveMessages 把用户输入和模型回复都存进 DB(或内存切片)
	// 用户消息的 usage 传空值(Usage{}),因为用户输入不消耗 token
	// Prometheus 指标:记录一次成功的模型调用,包含各类 Token 用量
	metrics.RecordModelCall(
		a.providerName, resp.Model, metrics.StatusOK,
		float64(resp.Usage.PromptTokens),                            // 输入 Token
		float64(resp.Usage.CompletionTokens),                        // 输出 Token
		float64(resp.Usage.CacheHitTokens()),                        // 缓存命中输入 Token
		float64(resp.Usage.CompletionTokensDetails.ReasoningTokens), // 推理 Token
	)
	if err := a.saveMessages(ctx, userInput, resp.Content, resp.Usage); err != nil {
		logger.Error("non-stream chat persist failed",
			zap.Error(err),
			zap.String("trace_id", traceID),
		)
		return llm.ChatResponse{}, err
	}
	a.createMemoryCandidates(ctx, userInput)
	a.processTagsAndReminders(ctx, userInput, resp.Content)

	// 打完成日志,包含 trace ID,模型名,耗时和 token 用量
	logger.Info("non-stream chat completed",
		zap.String("trace_id", traceID),
		zap.String("model", resp.Model),
		zap.Int64("duration_ms", elapsed.Milliseconds()),
		zap.Int("prompt_tokens", resp.Usage.PromptTokens),
		zap.Int("completion_tokens", resp.Usage.CompletionTokens),
		zap.Int("total_tokens", resp.Usage.TotalTokens),
	)
	return resp, nil
}

// ChatStream 流式对话.
// 返回 StreamReader 逐块读取,读完后调 Close + FinishStream.
// 参数:
//   - ctx: 上下文
//   - userInput: 用户输入的文本
//
// 返回值:流式读取器,错误
//
// 和 Chat 的区别:Chat 等完整回复再返回,ChatStream 边收边返回,前端可以打字机效果.
// 流程:生成 trace ID → 加载历史 → 调 LLM 流式接口 → 连接成功后存用户消息 → 返回 tracedReader.
// 注意:用户消息在连接成功后存(而不是调用前存),避免连接失败的请求留下半轮历史.
func (a *Agent) ChatStream(ctx context.Context, userInput string) (llm.StreamReader, error) {
	// 记录开始时间,后面算连接耗时
	start := time.Now()

	// 0. 生成 trace ID,后续所有日志/埋点/DB 都带上
	// trace.Ensure 检查 context 里有没有 trace ID,没有就生成一个
	ctx, traceID := trace.Ensure(ctx)

	// 创建孙 Span:Model Provider 层,记录 LLM 流式连接的耗时.
	// trace.StartSpan 从 context 里取父 Span(Service 层的 Span),把自己挂上去.
	// 三层 Span 组成调用链:Handler → ChatService → ModelProvider
	ctx, span := trace.StartSpan(ctx, "model_provider", "llm.stream")
	// 给 Span 加附加属性:模型名,方便后续按模型过滤查询
	span.SetAttr("model", a.model)
	// defer span.EndOK() 保证无论正常返回还是中途出错都标记 Span 结束
	defer span.EndOK()
	logger.Info("stream chat started",
		zap.String("trace_id", traceID),
		zap.String("model", a.model),
		zap.Int("input_chars", len(userInput)),
	)

	// 1. 加载历史并追加当前消息.连接成功后再持久化,避免失败请求留下半轮历史.
	msgs, err := a.loadMessages(ctx, userInput)
	if err != nil {
		logger.Error("load chat history failed", zap.Error(err), zap.String("trace_id", traceID))
		return nil, err
	}
	// 把用户这次说的话追加到消息列表末尾
	msgs = append(msgs, llm.ChatMessage{Role: llm.RoleUser, Content: userInput})

	// 3. 构造请求
	req := llm.ChatRequest{
		Model:    a.model,
		Messages: msgs,
	}

	// 4. 调 LLM 流式接口——建立连接,拿到一个 StreamReader
	// provider.Stream 不会等完整回复,而是建立连接后立即返回 reader
	// 调用方用 reader.Next() 逐块读取回复内容
	reader, err := a.provider.Stream(ctx, req)
	if err != nil {
		// 连接失败,打 Error 日志 + 发埋点
		logger.Error("stream LLM connect failed",
			zap.Error(err),
			zap.String("trace_id", traceID),
			zap.String("model", a.model),
			zap.Int64("duration_ms", time.Since(start).Milliseconds()),
		)
		// 发埋点: 记录模型调用失败事件.
		// 流式连接失败属于模型调用失败的一种
		telemetry.Emit(ctx,
			telemetry.EventModelRequestFailed,
			map[string]any{
				"model":                   a.model,
				telemetry.FieldStatus:     telemetry.StatusError,
				telemetry.FieldDurationMS: time.Since(start).Milliseconds(),
				"error":                   err.Error(),
			},
		)
		// Prometheus 指标:流式连接失败,Token 全 0
		metrics.RecordModelCall(a.providerName, a.model, metrics.StatusFailed, 0, 0, 0, 0)
		return nil, err
	}
	// 连接成功后保存用户消息.如果保存失败要关闭已经建立的流式连接.
	// 为什么连接成功才存？如果连接就失败了,用户消息不该存进 DB,
	// 不然下次加载历史会看到用户问了但模型没回,出现半轮对话
	if a.mem != nil {
		if err := a.mem.SaveMessage(ctx, a.sessionID, a.userID, llm.RoleUser, userInput, llm.Usage{}); err != nil {
			// 保存失败,关掉刚建立的流式连接,不让调用方继续读
			reader.Close()
			logger.Error("save user message failed",
				zap.Error(err),
				zap.String("trace_id", traceID),
				zap.String("session_id", a.sessionID),
			)
			return nil, err
		}
	}
	a.currentInput = userInput

	// 发埋点: 记录模型调用开始事件.
	// 方案 16.10.2: "创建 model_request_started 事件 → 记录 Span 开始时间 → 调用模型供应商"
	// 流式场景下连接成功 = 模型调用开始
	telemetry.Emit(ctx,
		telemetry.EventModelRequestStarted,
		map[string]any{
			"model":                   a.model,
			telemetry.FieldStatus:     telemetry.StatusOK,
			telemetry.FieldDurationMS: time.Since(start).Milliseconds(),
		},
	)

	// 打日志:连接成功,记录连接耗时
	logger.Info("stream LLM connected",
		zap.String("trace_id", traceID),
		zap.Int64("connect_ms", time.Since(start).Milliseconds()),
	)

	// 把 traceID 存到 reader 里,FinishStream 时要用
	// tracedReader 包装了底层 reader,额外带上 traceID 和计时数据
	// 调用方读完流后调 GetTraceID 和 GetStreamMetrics 取出这些数据
	return &tracedReader{streamReader: reader, traceID: traceID, startedAt: start}, nil
}

// FinishStream 在流式读取结束后调用,把完整回复和 token 用量持久化.
// fullContent 是调用方拼接所有 chunk 得到的完整文本.
// usage 必须在 StreamReader 读取结束后取,不然可能还是零值.
// 参数:
//   - ctx: 上下文
//   - fullContent: 完整的回复文本(调用方把所有 chunk 拼起来的)
//   - usage: token 用量(从 reader.Usage() 拿到)
//   - streamMetrics: 流式性能指标(从 GetStreamMetrics 拿到)
//
// 返回值:错误
func (a *Agent) FinishStream(ctx context.Context, fullContent string, usage llm.Usage, streamMetrics StreamMetrics) error {
	// 计算每秒输出 token 数(tokens per second)
	// 举例:生成 100 个 token 花了 5 秒 → 100 / (5000/1000) = 20 tokens/s
	tokensPerSecond := 0.0
	if streamMetrics.TotalDurationMs > 0 {
		// float64(usage.CompletionTokens): 输出 token 数
		// float64(streamMetrics.TotalDurationMs) / 1000: 把毫秒转成秒
		tokensPerSecond =
			float64(usage.CompletionTokens) /
				(float64(streamMetrics.TotalDurationMs) / 1000)
	}
	// 发埋点: 记录模型调用完成事件.
	// 方案 16.10.2: 模型返回结果后发 model_request_completed, 包含 token 用量和性能指标
	// first_token_ms: 第一个 token 的等待时间(体现响应速度)
	// total_duration_ms: 整个流式调用的总耗时
	// tokens_per_second: 输出速度
	telemetry.Emit(ctx,
		telemetry.EventModelRequestCompleted,
		map[string]any{
			"model":               a.model,
			telemetry.FieldStatus: telemetry.StatusOK,
			"first_token_ms":      streamMetrics.FirstTokenMs,
			"total_duration_ms":   streamMetrics.TotalDurationMs,
			"tokens_per_second":   tokensPerSecond,
			"prompt_tokens":       usage.PromptTokens,
			"completion_tokens":   usage.CompletionTokens,
			"total_tokens":        usage.TotalTokens,
			"cache_hit_tokens":    usage.CacheHitTokens(),
			"cache_miss_tokens":   usage.CacheMissTokens(),
			"reasoning_tokens":    usage.CompletionTokensDetails.ReasoningTokens,
		},
	)

	// Prometheus 指标:流式调用成功结束,记录各类 Token 用量
	metrics.RecordModelCall(
		a.providerName, a.model, metrics.StatusOK,
		float64(usage.PromptTokens),                            // 输入 Token
		float64(usage.CompletionTokens),                        // 输出 Token
		float64(usage.CacheHitTokens()),                        // 缓存命中输入 Token
		float64(usage.CompletionTokensDetails.ReasoningTokens), // 推理 Token
	)

	// 有 DB 模式:把完整回复和 token 用量存进 DB
	if a.mem != nil {
		// SaveMessage 存助手消息(Role = assistant),包含完整回复和 token 用量
		// 注意: 这里存的 fullContent 包含 <tags> 块, 但 <tags> 块在流式读取时已经发给客户端了,
		// 客户端需要自己截掉. DB 里保留完整内容方便回溯.
		if err := a.mem.SaveMessage(ctx, a.sessionID, a.userID, llm.RoleAssistant, fullContent, usage); err != nil {
			// 保存失败,打 Error 日志
			// trace.FromContext(ctx) 从 context 里取出 trace ID
			logger.Error("stream assistant reply persist failed",
				zap.Error(err),
				zap.String("trace_id", trace.FromContext(ctx)),
				zap.String("session_id", a.sessionID),
				zap.Int("content_chars", len(fullContent)),
			)
			return err
		}
		a.createMemoryCandidates(ctx, a.currentInput)
		a.processTagsAndReminders(ctx, a.currentInput, fullContent)
		a.currentInput = ""
		return nil
	} else {
		// 无 DB 模式:把完整回复追加到内存切片
		a.memMsgs = append(a.memMsgs, llm.ChatMessage{
			Role: llm.RoleAssistant, Content: fullContent,
		})
	}
	return nil
}

// processTagsAndReminders 在每轮对话结束后处理标签和提醒.
// 做的事情:
//  1. 从 LLM 回复里解析 <tags> JSON 块.
//  2. 把标签写入 emotion store (情绪持续段) 和 profile store (标签统计 + 非情绪维度标签).
//  3. 从用户输入里检测提醒意图, 检测到就创建提醒候选.
//  4. 检查画像分析阈值, 达到就后台异步分析.
//  5. 降级模式: 所有步骤失败只打日志, 不返回 error.
//
// 参数:
//   - ctx: 上下文
//   - userInput: 用户这一轮的输入文本
//   - assistantReply: 助手这一轮的完整回复 (包含 <tags> 块)
func (a *Agent) processTagsAndReminders(ctx context.Context, userInput, assistantReply string) {
	// 获取当前对话轮数: 查 dialogue_tags 表的最大 round 值 + 1, 或从 dialogues 表推断.
	// 用 CountDialogueTagsByUser 查当前最大 round, +1 就是本轮 round.
	round := 1
	if a.profileStore != nil {
		if count, err := a.profileStore.CountUserRounds(ctx, a.userID); err == nil && count > 0 {
			round = count + 1
		}
	}

	traceID := trace.FromContext(ctx)

	// 1. 解析 <tags> JSON 块
	tags, ok := emotion.ParseTags(assistantReply)
	if !ok {
		// LLM 没输出标签或格式不对, 这是正常的 (旧模型不支持), 只打 Debug 日志
		logger.Debug("no tags block found in assistant reply",
			zap.String("trace_id", traceID),
			zap.Int64("user_id", a.userID),
		)
	} else {
		// 2a. 写情绪持续段 + 情绪维度对话标签
		if a.emotionStore != nil {
			if err := a.emotionStore.RecordTags(ctx, a.userID, a.sessionID, traceID, round, tags); err != nil {
				logger.Error("emotion record tags failed",
					zap.String("trace_id", traceID),
					zap.Int64("user_id", a.userID),
					zap.Error(err),
				)
			}
		}

		// 2b. 写非情绪维度对话标签 + 所有维度的按天聚合统计
		if a.profileStore != nil {
			a.profileStore.RecordTags(ctx, a.userID, a.sessionID, traceID, round, profile.TagInput{
				Emotion:     tags.Emotion,
				Intensity:   tags.Intensity,
				Urgency:     tags.Urgency,
				Cooperation: tags.Cooperation,
				Trigger:     tags.Trigger,
			})
		}
	}

	// 3. 从用户输入检测提醒意图, 检测到就创建提醒候选
	if a.reminderStore != nil && strings.TrimSpace(userInput) != "" {
		hints := reminder.DetectReminders(userInput)
		for _, hint := range hints {
			// 第一版: 提醒时间暂用 1 小时后 (时间解析后续实现)
			// detect 只提取文本, 时间解析需要 NLP 后续做
			// 这里先创建 pending 提醒, 用户后续可以通过 API 修改时间
			remindAt := time.Now().Add(1 * time.Hour)
			if _, err := a.reminderStore.CreateReminder(
				ctx, a.userID, a.sessionID, traceID,
				hint.Content, remindAt, data.ReminderSourceDialogue,
			); err != nil {
				logger.Error("create reminder from dialogue failed",
					zap.String("trace_id", traceID),
					zap.Int64("user_id", a.userID),
					zap.String("content", hint.Content),
					zap.String("when", hint.When),
					zap.Error(err),
				)
			}
		}
	}

	// 4. 检查画像分析阈值
	if a.profileService != nil {
		a.profileService.CheckAndAnalyze(ctx, a.userID)
	}
}

// createMemoryCandidates 根据本轮用户原话生成待确认候选；失败只记录观测降级，不影响对话成功。
func (a *Agent) createMemoryCandidates(ctx context.Context, userInput string) {
	if a.mem == nil || strings.TrimSpace(userInput) == "" {
		return
	}
	candidates, err := a.mem.CreateCandidates(ctx, a.userID, a.sessionID, userInput)
	if err != nil {
		logger.Error("long-term memory candidate generation failed",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.Int64("user_id", a.userID),
			zap.Error(err),
		)
		return
	}
	logger.Debug("long-term memory candidate generation completed",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.Int64("user_id", a.userID),
		zap.Int("candidate_count", len(candidates)),
	)
}

// Next 读取下一块内容,同时记录首字时间和结束时间.
// 返回值:内容文本,是否结束(true=读完了),错误
func (t *tracedReader) Next() (string, bool, error) {
	// 调底层 reader 的 Next(),拿到一块内容
	// chunk: 这次读到的文本片段(比如"你好",可能只是完整回复的一部分)
	// done: 是否已经读完(true = 后面没有更多内容了)
	// err: 读取中的错误
	chunk, done, err := t.streamReader.Next()

	// 记录当前时间,用来算首字耗时和总耗时
	now := time.Now()

	// 第一次收到非空文本时,记录首字时间.
	// 首字时间 = 从开始到第一个有效 token 的时间,体现响应速度
	// t.firstTokenAt.IsZero() 判断是不是零值(还没记过时间)
	if chunk != "" && t.firstTokenAt.IsZero() {
		t.firstTokenAt = now
	}

	// 正常结束或者发生错误,都记录结束时间.
	// 结束时间用来算总耗时(从连接成功到读完所有内容)
	if done || err != nil {
		t.finishedAt = now
	}

	return chunk, done, err
}

// Usage 返回底层流式响应解析到的 token 用量.
// 必须在读取结束(Next 返回 done=true)后调,不然可能还没解析到 usage 数据.
func (t *tracedReader) Usage() llm.Usage { return t.streamReader.Usage() }

// Close 关闭底层流式响应资源.
// 读完后必须调,释放 HTTP 连接等资源.
func (t *tracedReader) Close() error { return t.streamReader.Close() }

// GetTraceID 返回当前流式对话的 trace ID.
// 从 ChatStream 返回的 StreamReader 接口取出 traceID,
// 给调用方在 FinishStream 后做日志关联用.
// 参数:
//   - r: ChatStream 返回的流式读取器
//
// 返回值:trace ID 字符串(如果不是 tracedReader 就返回空)
//
// 举例:handler 调 ChatStream 拿到 reader → 读流 → 调 GetTraceID 拿到 trace ID
// → 用 trace ID 把流式调用的所有日志串起来
func GetTraceID(r llm.StreamReader) string {
	// 类型断言:检查 r 是不是 *tracedReader
	// ok = true 说明是,可以取出 traceID
	// ok = false 说明不是(比如别的地方传了个别的 reader),返回空字符串
	if tr, ok := r.(*tracedReader); ok {
		return tr.traceID
	}
	return ""
}

// GetStreamMetrics 获取流式调用的性能指标.
// 参数:
//   - r: ChatStream 返回的流式读取器
//
// 返回值:包含首字耗时和总耗时的 StreamMetrics
func GetStreamMetrics(r llm.StreamReader) StreamMetrics {
	// 类型断言:检查 r 是不是 *tracedReader
	tr, ok := r.(*tracedReader)
	if !ok {
		// 不是 tracedReader,返回空指标(所有字段都是零值)
		return StreamMetrics{}
	}

	var metrics StreamMetrics

	// 算首字耗时:第一个有效 token 的时间 - 开始时间
	// 举例:开始 = 10:00:00.000,首字 = 10:00:00.500 → FirstTokenMs = 500
	if !tr.firstTokenAt.IsZero() {
		metrics.FirstTokenMs =
			tr.firstTokenAt.Sub(tr.startedAt).Milliseconds()
	}

	// 算总耗时:结束时间 - 开始时间
	// 举例:开始 = 10:00:00.000,结束 = 10:00:05.000 → TotalDurationMs = 5000
	if !tr.finishedAt.IsZero() {
		metrics.TotalDurationMs =
			tr.finishedAt.Sub(tr.startedAt).Milliseconds()
	}

	return metrics
}

// Reset 清空对话历史.
// 有 DB 模式:当前实现不做任何事(DB 历史不清空,换 session 才是正确做法).
// 无 DB 模式:清空内存 slice,只留 system prompt.
func (a *Agent) Reset() {
	if a.mem == nil {
		// 无 DB 模式:把切片截断到只剩第一条(system prompt)
		// a.memMsgs[:1] 保留切片的前 1 个元素,后面的都被丢弃
		// 举例:[system, user1, assistant1, user2] → [system]
		a.memMsgs = a.memMsgs[:1]
	}
	// 有 DB 模式什么都不做,因为历史在 DB 里,清内存没用
	// 要开新对话应该换 sessionID,而不是清历史
}

// saveMessages 把 user + assistant 两条消息写入 DB 或内存.
// 参数:
//   - ctx: 上下文
//   - userInput: 用户输入
//   - assistantOutput: 模型回复
//   - usage: token 用量(只存 assistant 的,user 消息传空 Usage{})
//
// 返回值:错误
func (a *Agent) saveMessages(ctx context.Context, userInput, assistantOutput string, usage llm.Usage) error {
	if a.mem != nil {
		// 有 DB 模式:存两条消息到 DB
		// 先存用户消息(usage 传空值,用户输入不消耗 token)
		if err := a.mem.SaveMessage(ctx, a.sessionID, a.userID, llm.RoleUser, userInput, llm.Usage{}); err != nil {
			return err
		}
		// 再存助手消息(带上 token 用量)
		return a.mem.SaveMessage(ctx, a.sessionID, a.userID, llm.RoleAssistant, assistantOutput, usage)
	} else {
		// 无 DB 模式:追加两条消息到内存切片
		a.memMsgs = append(a.memMsgs,
			llm.ChatMessage{Role: llm.RoleUser, Content: userInput},
			llm.ChatMessage{Role: llm.RoleAssistant, Content: assistantOutput},
		)
	}
	return nil
}
