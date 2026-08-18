// types.go 放 agent 包共用的类型定义。
//
// 做的事情：
//  1. 定义 Agent 结构体：对话编排者，持有 LLM Provider、模型名、系统提示词、记忆存储、会话 ID 和用户 ID。
//  2. 定义 StreamMetrics：记录一次流式调用的性能指标（首 token 耗时、总耗时）。
//  3. 定义 tracedReader：为 LLM StreamReader 附加 trace ID 和计时数据，用于关联日志和计算性能指标。
package agent

import (
	"time"

	"github.com/swallow-sun/swallow-go/internal/memory"
	"github.com/swallow-sun/swallow-go/internal/provider/llm"
)

const historyLimit = 20

// Agent 负责拼装上下文、调用模型并持久化对话。
type Agent struct {
	provider     llm.Provider
	providerName string // 供应商名称（deepseek/openai），给 metrics 标签用
	model        string
	systemPrompt string
	mem          *memory.Store
	sessionID    string
	userID       int64
	memMsgs      []llm.ChatMessage
}

// StreamMetrics 记录一次流式调用的性能指标。
type StreamMetrics struct {
	FirstTokenMs    int64
	TotalDurationMs int64
}

// tracedReader 为模型 StreamReader 附加 Trace 和计时数据。
type tracedReader struct {
	streamReader llm.StreamReader
	traceID      string
	startedAt    time.Time
	firstTokenAt time.Time
	finishedAt   time.Time
}
