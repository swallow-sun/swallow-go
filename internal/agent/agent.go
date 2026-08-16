// Package agent 负责对话上下文组装、LLM 调用和消息持久化编排。
package agent

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/swallow-sun/swallow-go/internal/memory"
	"github.com/swallow-sun/swallow-go/internal/provider/llm"
	"github.com/swallow-sun/swallow-go/internal/telemetry"
	"github.com/swallow-sun/swallow-go/internal/trace"
)

// Agent 是对话编排者。
// 负责拼装 system prompt、管理对话上下文、调用 LLM Provider。
// 调用方不直接碰 Provider，只跟 Agent 对话。
//
// Phase 2 改造：对话历史持久化到 SQLite（通过 memory.Store），
// 不再在内存里持有完整 messages slice。
// Agent 只持有 system prompt（读自文件）+ sessionID/userID（写 DB 用）。

// New 创建一个无 DB 的 Agent（Phase 1 兼容模式，历史只在内存）。
// 适合快速测试，重启丢失历史。
func New(provider llm.Provider, model string, systemPromptPath string) (*Agent, error) {
	prompt, err := os.ReadFile(systemPromptPath)
	if err != nil {
		return nil, fmt.Errorf("read system prompt: %w", err)
	}

	return &Agent{
		provider:     provider,
		model:        model,
		systemPrompt: string(prompt),
		memMsgs: []llm.ChatMessage{
			{Role: llm.RoleSystem, Content: string(prompt)},
		},
	}, nil
}

// NewWithDB 创建一个有 DB 持久化的 Agent（Phase 2+ 正式模式）。
// 对话历史存 SQLite，重启不丢。
// sessionID 和 userID 用于写 dialogues 表。
func NewWithDB(provider llm.Provider, model string, systemPromptPath string, mem *memory.Store, sessionID string, userID int64) (*Agent, error) {
	prompt, err := os.ReadFile(systemPromptPath)
	if err != nil {
		return nil, fmt.Errorf("read system prompt: %w", err)
	}

	return &Agent{
		provider:     provider,
		model:        model,
		systemPrompt: string(prompt),
		mem:          mem,
		sessionID:    sessionID,
		userID:       userID,
	}, nil
}

// loadMessages 拼装发送给 LLM 的完整 messages：
// [system prompt] + [最近 N 条历史对话]
// 无 DB 模式直接返回内存 slice。
func (a *Agent) loadMessages(ctx context.Context) ([]llm.ChatMessage, error) {
	// 无 DB 模式：直接返回内存 slice
	if a.mem == nil {
		return a.memMsgs, nil
	}

	// 有 DB 模式：system prompt + 从 DB 加载的历史
	msgs := []llm.ChatMessage{
		{Role: llm.RoleSystem, Content: a.systemPrompt},
	}

	history, err := a.mem.LoadHistory(ctx, a.sessionID, historyLimit)
	if err != nil {
		return nil, fmt.Errorf("load history: %w", err)
	}
	msgs = append(msgs, history...)

	return msgs, nil
}

// Chat 非流式对话。
func (a *Agent) Chat(ctx context.Context, userInput string) (llm.ChatResponse, error) {
	start := time.Now()

	// 0. 生成 trace ID，后续所有日志/埋点/DB 都带上
	ctx, traceID := trace.Ensure(ctx)

	// 1. 加载历史 + 追加当前用户消息
	msgs, err := a.loadMessages(ctx)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	msgs = append(msgs, llm.ChatMessage{
		Role: llm.RoleUser, Content: userInput,
	})

	// 2. 构造请求
	req := llm.ChatRequest{
		Model:    a.model,
		Messages: msgs,
	}

	// 3. 调 LLM
	resp, err := a.provider.Complete(ctx, req)
	elapsed := time.Since(start)

	if err != nil {
		telemetry.Emit(ctx, telemetry.EventLLMCall, map[string]any{
			"model":  a.model,
			telemetry.FieldStatus:     telemetry.StatusError,
			telemetry.FieldDurationMS: elapsed.Milliseconds(),
			"error":  err.Error(),
		})
		return llm.ChatResponse{}, err
	}

	// 4. 埋点
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
	if err := a.saveMessages(ctx, userInput, resp.Content, resp.Usage); err != nil {
		return llm.ChatResponse{}, err
	}

	_ = traceID // TODO: 返回给调用方供日志关联
	return resp, nil
}

// ChatStream 流式对话。
// 返回 StreamReader 逐块读取，读完后调 Close + FinishStream。
func (a *Agent) ChatStream(ctx context.Context, userInput string) (llm.StreamReader, error) {
	start := time.Now()

	// 0. 生成 trace ID，后续所有日志/埋点/DB 都带上
	ctx, traceID := trace.Ensure(ctx)

	// 1. 加载历史并追加当前消息。连接成功后再持久化，避免失败请求留下半轮历史。
	msgs, err := a.loadMessages(ctx)
	if err != nil {
		return nil, err
	}
	msgs = append(msgs, llm.ChatMessage{Role: llm.RoleUser, Content: userInput})

	// 3. 构造请求
	req := llm.ChatRequest{
		Model:    a.model,
		Messages: msgs,
	}

	// 4. 调 LLM 流式
	reader, err := a.provider.Stream(ctx, req)
	if err != nil {
		telemetry.Emit(ctx, telemetry.EventLLMStream, map[string]any{
			"model":  a.model,
			telemetry.FieldStatus:     telemetry.StatusError,
			telemetry.FieldDurationMS: time.Since(start).Milliseconds(),
			"error":  err.Error(),
		})
		return nil, err
	}
	if a.mem != nil {
		if err := a.mem.SaveMessage(ctx, a.sessionID, a.userID, llm.RoleUser, userInput, llm.Usage{}); err != nil {
			reader.Close()
			return nil, err
		}
	}

	telemetry.Emit(ctx, telemetry.EventLLMStream, map[string]any{
		"model":  a.model,
		telemetry.FieldStatus:     telemetry.StatusConnected,
		telemetry.FieldDurationMS: time.Since(start).Milliseconds(),
	})

	// 把 traceID 存到 reader 里，FinishStream 时要用
	return &tracedReader{streamReader: reader, traceID: traceID, startedAt: start}, nil
}

// FinishStream 在流式读取结束后调用，把完整回复和 token 用量持久化。
// fullContent 是调用方拼接所有 chunk 得到的完整文本。
// usage 必须在 StreamReader 读取结束后取得，否则可能仍为零值。
func (a *Agent) FinishStream(ctx context.Context, fullContent string, usage llm.Usage, metrics StreamMetrics) error {
	tokensPerSecond := 0.0
	if metrics.TotalDurationMs > 0 {
		tokensPerSecond =
			float64(usage.CompletionTokens) /
				(float64(metrics.TotalDurationMs) / 1000)
	}
	telemetry.Emit(ctx, telemetry.EventLLMStreamComplete, map[string]any{
		"model":             a.model,
		telemetry.FieldStatus: telemetry.StatusOK,
		"first_token_ms":    metrics.FirstTokenMs,
		"total_duration_ms": metrics.TotalDurationMs,
		"tokens_per_second": tokensPerSecond,
		"prompt_tokens":     usage.PromptTokens,
		"completion_tokens": usage.CompletionTokens,
		"total_tokens":      usage.TotalTokens,
		"cache_hit_tokens":  usage.CacheHitTokens(),
		"cache_miss_tokens": usage.CacheMissTokens(),
		"reasoning_tokens":  usage.CompletionTokensDetails.ReasoningTokens,
	})

	if a.mem != nil {
		return a.mem.SaveMessage(ctx, a.sessionID, a.userID, llm.RoleAssistant, fullContent, usage)
	} else {
		a.memMsgs = append(a.memMsgs, llm.ChatMessage{
			Role: llm.RoleAssistant, Content: fullContent,
		})
	}
	return nil
}


// Next 将增量读取操作转发给底层 StreamReader。
// Next 读取下一块内容，同时记录首字时间和结束时间。
func (t *tracedReader) Next() (string, bool, error) {
	chunk, done, err := t.streamReader.Next()

	now := time.Now()

	// 第一次收到非空文本时，记录首字时间。
	if chunk != "" && t.firstTokenAt.IsZero() {
		t.firstTokenAt = now
	}

	// 正常结束或者发生错误，都记录结束时间。
	if done || err != nil {
		t.finishedAt = now
	}

	return chunk, done, err
}

// Usage 返回底层流式响应解析到的 token 用量。
func (t *tracedReader) Usage() llm.Usage { return t.streamReader.Usage() }

// Close 关闭底层流式响应资源。
func (t *tracedReader) Close() error { return t.streamReader.Close() }

// GetTraceID 返回当前流式对话的 trace ID。
// 从 ChatStream 返回的 StreamReader 接口取出 traceID，
// 供调用方在 FinishStream 后做日志关联。
func GetTraceID(r llm.StreamReader) string {
	if tr, ok := r.(*tracedReader); ok {
		return tr.traceID
	}
	return ""
}

// GetStreamMetrics 获取流式调用的性能指标。
func GetStreamMetrics(r llm.StreamReader) StreamMetrics {
	tr, ok := r.(*tracedReader)
	if !ok {
		return StreamMetrics{}
	}

	var metrics StreamMetrics

	if !tr.firstTokenAt.IsZero() {
		metrics.FirstTokenMs =
			tr.firstTokenAt.Sub(tr.startedAt).Milliseconds()
	}

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
		a.memMsgs = a.memMsgs[:1]
	}
}

// saveMessages 把 user + assistant 两条消息写入 DB 或内存。
func (a *Agent) saveMessages(ctx context.Context, userInput, assistantOutput string, usage llm.Usage) error {
	if a.mem != nil {
		if err := a.mem.SaveMessage(ctx, a.sessionID, a.userID, llm.RoleUser, userInput, llm.Usage{}); err != nil {
			return err
		}
		return a.mem.SaveMessage(ctx, a.sessionID, a.userID, llm.RoleAssistant, assistantOutput, usage)
	} else {
		a.memMsgs = append(a.memMsgs,
			llm.ChatMessage{Role: llm.RoleUser, Content: userInput},
			llm.ChatMessage{Role: llm.RoleAssistant, Content: assistantOutput},
		)
	}
	return nil
}
