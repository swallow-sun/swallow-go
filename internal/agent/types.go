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
