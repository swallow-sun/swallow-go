// agent.go 放 Agent：对话编排者，负责拼装上下文、调用模型并持久化对话。
//
// 做的事情：
//  1. New/NewWithDB：创建 Agent 实例，读取系统提示词文件，绑定记忆存储和会话信息。
//  2. Chat：非流式对话——加载历史 → 调 LLM → 持久化 user+assistant 两条消息。
//  3. ChatStream：流式对话——加载历史 → 调 LLM 流式接口 → 连接成功后保存用户消息 → 返回 tracedReader。
//  4. FinishStream：流式读取结束后调用，把完整回复 + token 用量持久化为助手消息。
//  5. tracedReader：包装 LLM StreamReader，附加 trace ID 和计时数据，用于日志关联和性能指标计算。
//
// Phase 2 改造：对话历史存到 SQLite（通过 memory.Store），不在内存里持有完整 messages slice。
package agent

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/swallow-sun/swallow-go/internal/memory"
	"github.com/swallow-sun/swallow-go/internal/metrics"
	"github.com/swallow-sun/swallow-go/internal/provider/llm"
	"github.com/swallow-sun/swallow-go/internal/telemetry"
	"github.com/swallow-sun/swallow-go/internal/trace"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// Agent 是对话编排者。
// 负责拼装 system prompt、管理对话上下文、调 LLM Provider。
// 调用方不直接碰 Provider，只跟 Agent 打交道。
//
// Phase 2 改造：对话历史存到 SQLite（通过 memory.Store），
// 不再在内存里持有完整 messages slice。
// Agent 只持有 system prompt（读自文件）+ sessionID/userID（写 DB 用）。

// New 创建一个无 DB 的 Agent（Phase 1 兼容模式，历史只在内存）。
// 适合快速测试，重启丢历史。
// 参数：
//   - provider: LLM 提供方（如 OpenAI 兼容客户端）
//   - providerName: 供应商名称（如 "deepseek"），给 metrics 标签用
//   - model: 模型名，如 "gpt-4o"
//   - systemPromptPath: 系统提示词文件路径
func New(provider llm.Provider, providerName, model, systemPromptPath string) (*Agent, error) {
	// 读系统提示词文件，拿到文件内容（字节切片）
	// os.ReadFile 读整个文件，返回 []byte 和 error
	prompt, err := os.ReadFile(systemPromptPath)
	if err != nil {
		return nil, fmt.Errorf("read system prompt: %w", err)
	}

	// 构造 Agent，只设内存模式需要的字段
	// mem 为 nil（无 DB 模式），对话历史只存在 memMsgs 这个内存切片里
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

// NewWithDB 创建一个有 DB 持久化的 Agent（Phase 2+ 正式模式）。
// 对话历史存 SQLite，重启不丢。
// sessionID 和 userID 用于写 dialogues 表。
// 参数：
//   - provider: LLM 提供方
//   - providerName: 供应商名称（如 "deepseek"），给 metrics 标签用
//   - model: 模型名
//   - systemPromptPath: 系统提示词文件路径
//   - mem: 记忆存储，负责读写 SQLite 里的对话历史
//   - sessionID: 会话 ID，标识当前对话
//   - userID: 用户 ID，标识谁在说话
func NewWithDB(provider llm.Provider, providerName, model, systemPromptPath string, mem *memory.Store, sessionID string, userID int64) (*Agent, error) {
	// 读系统提示词文件
	prompt, err := os.ReadFile(systemPromptPath)
	if err != nil {
		return nil, fmt.Errorf("read system prompt: %w", err)
	}

	// 构造 Agent，设 DB 模式需要的字段
	// mem 不为 nil，对话历史存 DB；memMsgs 为空切片（DB 模式不用它）
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

// loadMessages 拼装发给 LLM 的完整 messages：
// [system prompt] + [最近 N 条历史对话]
// 无 DB 模式直接返回内存 slice。
// 返回值：消息列表、错误
func (a *Agent) loadMessages(ctx context.Context) ([]llm.ChatMessage, error) {
	// 无 DB 模式：直接返回内存里的消息切片
	// mem == nil 说明没用 DB，历史只在 memMsgs 这个内存切片里
	if a.mem == nil {
		return a.memMsgs, nil
	}

	// 有 DB 模式：先放一条 system prompt，再从 DB 加载历史
	// 举例：发给 LLM 的 messages 长这样：
	// [
	//   {Role: "system", Content: "你是贾维斯..."},
	//   {Role: "user", Content: "你好"},        ← 历史里的
	//   {Role: "assistant", Content: "你好主人"}, ← 历史里的
	//   {Role: "user", Content: "今天天气怎么样"}, ← 本轮用户输入（在调用方加）
	// ]
	msgs := []llm.ChatMessage{
		{Role: llm.RoleSystem, Content: a.systemPrompt},
	}

	// 从 DB 加载最近 historyLimit（20）条历史对话
	// LoadHistory 返回的已经是按时间排序的消息切片
	history, err := a.mem.LoadHistory(ctx, a.sessionID, historyLimit)
	if err != nil {
		return nil, fmt.Errorf("load history: %w", err)
	}
	// 把历史消息追加到 msgs 后面
	// append 会在 msgs 后面加上 history 里的所有消息
	msgs = append(msgs, history...)

	return msgs, nil
}

// Chat 非流式对话。
// 流程：生成 trace ID → 加载历史 → 追加用户消息 → 调 LLM → 埋点 → 持久化 → 返回。
// 参数：
//   - ctx: 上下文
//   - userInput: 用户输入的文本
// 返回值：LLM 的完整回复、错误
func (a *Agent) Chat(ctx context.Context, userInput string) (llm.ChatResponse, error) {
	// 记录开始时间，后面算总耗时
	start := time.Now()

	// 0. 生成 trace ID，后续所有日志/埋点/DB 都带上
	// trace.Ensure 检查 context 里有没有 trace ID，没有就生成一个塞进去
	// 返回新的 context 和 trace ID 字符串
	// 举例：traceID = "550e8400-e29b-41d4-a716-446655440000"
	// 这样同一次对话的所有日志都能通过 trace ID 串起来，方便排查问题
	ctx, traceID := trace.Ensure(ctx)
	logger.Info("非流式对话开始",
		zap.String("trace_id", traceID),
		zap.String("model", a.model),
		zap.Int("input_chars", len(userInput)),
	)

	// 1. 加载历史 + 追加当前用户消息
	// loadMessages 返回 [system prompt] + [最近20条历史] 的消息列表
	msgs, err := a.loadMessages(ctx)
	if err != nil {
		logger.Error("加载对话历史失败", zap.Error(err), zap.String("trace_id", traceID))
		return llm.ChatResponse{}, err
	}
	// 把用户这次说的话追加到消息列表末尾
	// LLM 看到的就是：系统提示 + 历史 + 当前用户输入
	msgs = append(msgs, llm.ChatMessage{
		Role: llm.RoleUser, Content: userInput,
	})

	// 2. 构造 LLM 请求
	// 包含模型名和完整的消息列表
	req := llm.ChatRequest{
		Model:    a.model,
		Messages: msgs,
	}

	// 3. 调 LLM——发请求给模型，等待完整回复
	// provider.Complete 是非流式调用，会阻塞直到收到完整回复
	resp, err := a.provider.Complete(ctx, req)
	// 算耗时：从函数开始到现在
	elapsed := time.Since(start)

	if err != nil {
		// 调用失败，打 Error 日志 + 发埋点
		logger.Error("非流式 LLM 调用失败",
			zap.Error(err),
			zap.String("trace_id", traceID),
			zap.String("model", a.model),
			zap.Int64("duration_ms", elapsed.Milliseconds()),
		)
		// telemetry.Emit 发一条埋点事件，记录这次调用失败了
		// 埋点数据会被异步收集，用于监控和告警
		telemetry.Emit(ctx, telemetry.EventLLMCall, map[string]any{
			"model":  a.model,
			telemetry.FieldStatus:     telemetry.StatusError,
			telemetry.FieldDurationMS: elapsed.Milliseconds(),
			"error":  err.Error(),
		})
		// Prometheus 指标：记录一次失败的模型调用（Token 用量全 0，因为没拿到回复）
		metrics.RecordModelCall(a.providerName, a.model, metrics.StatusFailed, 0, 0, 0, 0)
		return llm.ChatResponse{}, err
	}

	// 4. 埋点——记录成功的 LLM 调用
	// 包含 token 用量（prompt_tokens = 输入 token 数，completion_tokens = 输出 token 数）
	// cache_hit_tokens / cache_miss_tokens 是缓存命中情况（有些 API 支持 prompt 缓存）
	// reasoning_tokens 是推理 token 数（如 OpenAI o1 系列的内部推理）
	telemetry.Emit(ctx, telemetry.EventLLMCall, map[string]any{
		"model":             resp.Model,
		telemetry.FieldStatus:     telemetry.StatusOK,
		telemetry.FieldDurationMS: elapsed.Milliseconds(),
		"prompt_tokens":     resp.Usage.PromptTokens,
		"completion_tokens": resp.Usage.CompletionTokens,
		"total_tokens":      resp.Usage.TotalTokens,
		"cache_hit_tokens":  resp.Usage.CacheHitTokens(),
		"cache_miss_tokens": resp.Usage.CacheMissTokens(),
		"reasoning_tokens":  resp.Usage.CompletionTokensDetails.ReasoningTokens,
	})

	// 5. 持久化（user + assistant 两条都写 DB）
	// saveMessages 把用户输入和模型回复都存进 DB（或内存切片）
	// 用户消息的 usage 传空值（Usage{}），因为用户输入不消耗 token
	// Prometheus 指标：记录一次成功的模型调用，包含各类 Token 用量
	metrics.RecordModelCall(
		a.providerName, resp.Model, metrics.StatusOK,
		float64(resp.Usage.PromptTokens),     // 输入 Token
		float64(resp.Usage.CompletionTokens),  // 输出 Token
		float64(resp.Usage.CacheHitTokens()),   // 缓存命中输入 Token
		float64(resp.Usage.CompletionTokensDetails.ReasoningTokens), // 推理 Token
	)
	if err := a.saveMessages(ctx, userInput, resp.Content, resp.Usage); err != nil {
		logger.Error("非流式对话消息持久化失败",
			zap.Error(err),
			zap.String("trace_id", traceID),
		)
		return llm.ChatResponse{}, err
	}

	// 打完成日志，包含 trace ID、模型名、耗时和 token 用量
	logger.Info("非流式对话完成",
		zap.String("trace_id", traceID),
		zap.String("model", resp.Model),
		zap.Int64("duration_ms", elapsed.Milliseconds()),
		zap.Int("prompt_tokens", resp.Usage.PromptTokens),
		zap.Int("completion_tokens", resp.Usage.CompletionTokens),
		zap.Int("total_tokens", resp.Usage.TotalTokens),
	)
	return resp, nil
}

// ChatStream 流式对话。
// 返回 StreamReader 逐块读取，读完后调 Close + FinishStream。
// 参数：
//   - ctx: 上下文
//   - userInput: 用户输入的文本
// 返回值：流式读取器、错误
//
// 和 Chat 的区别：Chat 等完整回复再返回，ChatStream 边收边返回，前端可以打字机效果。
// 流程：生成 trace ID → 加载历史 → 调 LLM 流式接口 → 连接成功后存用户消息 → 返回 tracedReader。
// 注意：用户消息在连接成功后存（而不是调用前存），避免连接失败的请求留下半轮历史。
func (a *Agent) ChatStream(ctx context.Context, userInput string) (llm.StreamReader, error) {
	// 记录开始时间，后面算连接耗时
	start := time.Now()

	// 0. 生成 trace ID，后续所有日志/埋点/DB 都带上
	// trace.Ensure 检查 context 里有没有 trace ID，没有就生成一个
	ctx, traceID := trace.Ensure(ctx)

	// 创建孙 Span：Model Provider 层，记录 LLM 流式连接的耗时。
	// trace.StartSpan 从 context 里取父 Span（Service 层的 Span），把自己挂上去。
	// 三层 Span 组成调用链：Handler → ChatService → ModelProvider
	ctx, span := trace.StartSpan(ctx, "model_provider", "llm.stream")
	// 给 Span 加附加属性：模型名，方便后续按模型过滤查询
	span.SetAttr("model", a.model)
	// defer span.EndOK() 保证无论正常返回还是中途出错都标记 Span 结束
	defer span.EndOK()
	logger.Info("流式对话开始",
		zap.String("trace_id", traceID),
		zap.String("model", a.model),
		zap.Int("input_chars", len(userInput)),
	)

	// 1. 加载历史并追加当前消息。连接成功后再持久化，避免失败请求留下半轮历史。
	msgs, err := a.loadMessages(ctx)
	if err != nil {
		logger.Error("加载对话历史失败", zap.Error(err), zap.String("trace_id", traceID))
		return nil, err
	}
	// 把用户这次说的话追加到消息列表末尾
	msgs = append(msgs, llm.ChatMessage{Role: llm.RoleUser, Content: userInput})

	// 3. 构造请求
	req := llm.ChatRequest{
		Model:    a.model,
		Messages: msgs,
	}

	// 4. 调 LLM 流式接口——建立连接，拿到一个 StreamReader
	// provider.Stream 不会等完整回复，而是建立连接后立即返回 reader
	// 调用方用 reader.Next() 逐块读取回复内容
	reader, err := a.provider.Stream(ctx, req)
	if err != nil {
		// 连接失败，打 Error 日志 + 发埋点
		logger.Error("流式 LLM 连接失败",
			zap.Error(err),
			zap.String("trace_id", traceID),
			zap.String("model", a.model),
			zap.Int64("duration_ms", time.Since(start).Milliseconds()),
		)
		telemetry.Emit(ctx, telemetry.EventLLMStream, map[string]any{
			"model":  a.model,
			telemetry.FieldStatus:     telemetry.StatusError,
			telemetry.FieldDurationMS: time.Since(start).Milliseconds(),
			"error":  err.Error(),
		})
		// Prometheus 指标：流式连接失败，Token 全 0
		metrics.RecordModelCall(a.providerName, a.model, metrics.StatusFailed, 0, 0, 0, 0)
		return nil, err
	}
	// 连接成功后保存用户消息。如果保存失败要关闭已经建立的流式连接。
	// 为什么连接成功才存？如果连接就失败了，用户消息不该存进 DB，
	// 不然下次加载历史会看到用户问了但模型没回，出现半轮对话
	if a.mem != nil {
		if err := a.mem.SaveMessage(ctx, a.sessionID, a.userID, llm.RoleUser, userInput, llm.Usage{}); err != nil {
			// 保存失败，关掉刚建立的流式连接，不让调用方继续读
			reader.Close()
			logger.Error("保存用户消息失败",
				zap.Error(err),
				zap.String("trace_id", traceID),
				zap.String("session_id", a.sessionID),
			)
			return nil, err
		}
	}

	// 发埋点：记录流式连接成功
	telemetry.Emit(ctx, telemetry.EventLLMStream, map[string]any{
		"model":  a.model,
		telemetry.FieldStatus:     telemetry.StatusConnected,
		telemetry.FieldDurationMS: time.Since(start).Milliseconds(),
	})

	// 打日志：连接成功，记录连接耗时
	logger.Info("流式 LLM 连接成功",
		zap.String("trace_id", traceID),
		zap.Int64("connect_ms", time.Since(start).Milliseconds()),
	)

	// 把 traceID 存到 reader 里，FinishStream 时要用
	// tracedReader 包装了底层 reader，额外带上 traceID 和计时数据
	// 调用方读完流后调 GetTraceID 和 GetStreamMetrics 取出这些数据
	return &tracedReader{streamReader: reader, traceID: traceID, startedAt: start}, nil
}

// FinishStream 在流式读取结束后调用，把完整回复和 token 用量持久化。
// fullContent 是调用方拼接所有 chunk 得到的完整文本。
// usage 必须在 StreamReader 读取结束后取，不然可能还是零值。
// 参数：
//   - ctx: 上下文
//   - fullContent: 完整的回复文本（调用方把所有 chunk 拼起来的）
//   - usage: token 用量（从 reader.Usage() 拿到）
//   - streamMetrics: 流式性能指标（从 GetStreamMetrics 拿到）
//
// 返回值：错误
func (a *Agent) FinishStream(ctx context.Context, fullContent string, usage llm.Usage, streamMetrics StreamMetrics) error {
	// 计算每秒输出 token 数（tokens per second）
	// 举例：生成 100 个 token 花了 5 秒 → 100 / (5000/1000) = 20 tokens/s
	tokensPerSecond := 0.0
	if streamMetrics.TotalDurationMs > 0 {
		// float64(usage.CompletionTokens): 输出 token 数
		// float64(streamMetrics.TotalDurationMs) / 1000: 把毫秒转成秒
		tokensPerSecond =
			float64(usage.CompletionTokens) /
				(float64(streamMetrics.TotalDurationMs) / 1000)
	}
	// 发埋点：记录流式调用完成，包含性能指标和 token 用量
	// first_token_ms: 第一个 token 的等待时间（体现响应速度）
	// total_duration_ms: 整个流式调用的总耗时
	// tokens_per_second: 输出速度
	telemetry.Emit(ctx, telemetry.EventLLMStreamComplete, map[string]any{
		"model":             a.model,
		telemetry.FieldStatus: telemetry.StatusOK,
		"first_token_ms":    streamMetrics.FirstTokenMs,
		"total_duration_ms": streamMetrics.TotalDurationMs,
		"tokens_per_second": tokensPerSecond,
		"prompt_tokens":     usage.PromptTokens,
		"completion_tokens": usage.CompletionTokens,
		"total_tokens":      usage.TotalTokens,
		"cache_hit_tokens":  usage.CacheHitTokens(),
		"cache_miss_tokens": usage.CacheMissTokens(),
		"reasoning_tokens":  usage.CompletionTokensDetails.ReasoningTokens,
	})

	// Prometheus 指标：流式调用成功结束，记录各类 Token 用量
	metrics.RecordModelCall(
		a.providerName, a.model, metrics.StatusOK,
		float64(usage.PromptTokens),     // 输入 Token
		float64(usage.CompletionTokens),  // 输出 Token
		float64(usage.CacheHitTokens()),   // 缓存命中输入 Token
		float64(usage.CompletionTokensDetails.ReasoningTokens), // 推理 Token
	)

	// 有 DB 模式：把完整回复和 token 用量存进 DB
	if a.mem != nil {
		// SaveMessage 存助手消息（Role = assistant），包含完整回复和 token 用量
		if err := a.mem.SaveMessage(ctx, a.sessionID, a.userID, llm.RoleAssistant, fullContent, usage); err != nil {
			// 保存失败，打 Error 日志
			// trace.FromContext(ctx) 从 context 里取出 trace ID
			logger.Error("流式对话助手回复持久化失败",
				zap.Error(err),
				zap.String("trace_id", trace.FromContext(ctx)),
				zap.String("session_id", a.sessionID),
				zap.Int("content_chars", len(fullContent)),
			)
			return err
		}
		return nil
	} else {
		// 无 DB 模式：把完整回复追加到内存切片
		a.memMsgs = append(a.memMsgs, llm.ChatMessage{
			Role: llm.RoleAssistant, Content: fullContent,
		})
	}
	return nil
}


// Next 读取下一块内容，同时记录首字时间和结束时间。
// 返回值：内容文本、是否结束（true=读完了）、错误
func (t *tracedReader) Next() (string, bool, error) {
	// 调底层 reader 的 Next()，拿到一块内容
	// chunk: 这次读到的文本片段（比如"你好"，可能只是完整回复的一部分）
	// done: 是否已经读完（true = 后面没有更多内容了）
	// err: 读取中的错误
	chunk, done, err := t.streamReader.Next()

	// 记录当前时间，用来算首字耗时和总耗时
	now := time.Now()

	// 第一次收到非空文本时，记录首字时间。
	// 首字时间 = 从开始到第一个有效 token 的时间，体现响应速度
	// t.firstTokenAt.IsZero() 判断是不是零值（还没记过时间）
	if chunk != "" && t.firstTokenAt.IsZero() {
		t.firstTokenAt = now
	}

	// 正常结束或者发生错误，都记录结束时间。
	// 结束时间用来算总耗时（从连接成功到读完所有内容）
	if done || err != nil {
		t.finishedAt = now
	}

	return chunk, done, err
}

// Usage 返回底层流式响应解析到的 token 用量。
// 必须在读取结束（Next 返回 done=true）后调，不然可能还没解析到 usage 数据。
func (t *tracedReader) Usage() llm.Usage { return t.streamReader.Usage() }

// Close 关闭底层流式响应资源。
// 读完后必须调，释放 HTTP 连接等资源。
func (t *tracedReader) Close() error { return t.streamReader.Close() }

// GetTraceID 返回当前流式对话的 trace ID。
// 从 ChatStream 返回的 StreamReader 接口取出 traceID，
// 给调用方在 FinishStream 后做日志关联用。
// 参数：
//   - r: ChatStream 返回的流式读取器
//
// 返回值：trace ID 字符串（如果不是 tracedReader 就返回空）
//
// 举例：handler 调 ChatStream 拿到 reader → 读流 → 调 GetTraceID 拿到 trace ID
// → 用 trace ID 把流式调用的所有日志串起来
func GetTraceID(r llm.StreamReader) string {
	// 类型断言：检查 r 是不是 *tracedReader
	// ok = true 说明是，可以取出 traceID
	// ok = false 说明不是（比如别的地方传了个别的 reader），返回空字符串
	if tr, ok := r.(*tracedReader); ok {
		return tr.traceID
	}
	return ""
}

// GetStreamMetrics 获取流式调用的性能指标。
// 参数：
//   - r: ChatStream 返回的流式读取器
//
// 返回值：包含首字耗时和总耗时的 StreamMetrics
func GetStreamMetrics(r llm.StreamReader) StreamMetrics {
	// 类型断言：检查 r 是不是 *tracedReader
	tr, ok := r.(*tracedReader)
	if !ok {
		// 不是 tracedReader，返回空指标（所有字段都是零值）
		return StreamMetrics{}
	}

	var metrics StreamMetrics

	// 算首字耗时：第一个有效 token 的时间 - 开始时间
	// 举例：开始 = 10:00:00.000，首字 = 10:00:00.500 → FirstTokenMs = 500
	if !tr.firstTokenAt.IsZero() {
		metrics.FirstTokenMs =
			tr.firstTokenAt.Sub(tr.startedAt).Milliseconds()
	}

	// 算总耗时：结束时间 - 开始时间
	// 举例：开始 = 10:00:00.000，结束 = 10:00:05.000 → TotalDurationMs = 5000
	if !tr.finishedAt.IsZero() {
		metrics.TotalDurationMs =
			tr.finishedAt.Sub(tr.startedAt).Milliseconds()
	}

	return metrics
}

// Reset 清空对话历史。
// 有 DB 模式：当前实现不做任何事（DB 历史不清空，换 session 才是正确做法）。
// 无 DB 模式：清空内存 slice，只留 system prompt。
func (a *Agent) Reset() {
	if a.mem == nil {
		// 无 DB 模式：把切片截断到只剩第一条（system prompt）
		// a.memMsgs[:1] 保留切片的前 1 个元素，后面的都被丢弃
		// 举例：[system, user1, assistant1, user2] → [system]
		a.memMsgs = a.memMsgs[:1]
	}
	// 有 DB 模式什么都不做，因为历史在 DB 里，清内存没用
	// 要开新对话应该换 sessionID，而不是清历史
}

// saveMessages 把 user + assistant 两条消息写入 DB 或内存。
// 参数：
//   - ctx: 上下文
//   - userInput: 用户输入
//   - assistantOutput: 模型回复
//   - usage: token 用量（只存 assistant 的，user 消息传空 Usage{}）
//
// 返回值：错误
func (a *Agent) saveMessages(ctx context.Context, userInput, assistantOutput string, usage llm.Usage) error {
	if a.mem != nil {
		// 有 DB 模式：存两条消息到 DB
		// 先存用户消息（usage 传空值，用户输入不消耗 token）
		if err := a.mem.SaveMessage(ctx, a.sessionID, a.userID, llm.RoleUser, userInput, llm.Usage{}); err != nil {
			return err
		}
		// 再存助手消息（带上 token 用量）
		return a.mem.SaveMessage(ctx, a.sessionID, a.userID, llm.RoleAssistant, assistantOutput, usage)
	} else {
		// 无 DB 模式：追加两条消息到内存切片
		a.memMsgs = append(a.memMsgs,
			llm.ChatMessage{Role: llm.RoleUser, Content: userInput},
			llm.ChatMessage{Role: llm.RoleAssistant, Content: assistantOutput},
		)
	}
	return nil
}
