// Package telemetry 定义和记录 Swallow-Go 的轻量业务埋点。
package telemetry

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultBufferSize 是调用 Init 时未提供有效容量使用的默认队列长度。
	DefaultBufferSize = 256

	// DefaultSinkWriteTimeout 是单条事件写入持久化 Sink 的最长等待时间。
	DefaultSinkWriteTimeout = 3 * time.Second

	// 事件类型。
	EventDialogue          = "dialogue"
	EventMemoryQuery       = "memory_query"
	EventLLMCall           = "llm.call"
	EventLLMStream         = "llm.stream"
	EventLLMStreamComplete = "llm.stream.complete"

	// 通用事件字段名。
	FieldStatus     = "status"
	FieldDurationMS = "ms"

	// 通用事件状态值。
	StatusOK        = "ok"
	StatusError     = "error"
	StatusConnected = "connected"
)

// Event 是一条等待记录的埋点事件。
// TraceID 用于关联同一请求产生的日志、对话和其他事件；
// Fields 只保存该事件特有的扩展字段。
type Event struct {
	Name      string
	TraceID   string
	Timestamp time.Time
	Fields    map[string]any
}

// EventSink 是事件持久化目标。
// 实现必须使用传入的 Context 执行阻塞操作，并在 Context 取消后尽快返回。
// 返回错误表示本次事件没有可靠写入持久化存储。
type EventSink interface {
	WriteEvent(
		ctx context.Context,
		eventType string,
		traceID string,
		data string,
		durationMs int64,
		success bool,
	) error
}

// Stats 是 Telemetry 当前生命周期的统计快照。
type Stats struct {
	Accepted int64
	Consumed int64
	Dropped  int64
	Failed   int64
	Panics   int64
	Queued   int
	Capacity int
}

// recorder 持有 Telemetry 在一个进程生命周期中的全部运行状态。
// 类型定义集中在 types.go；状态初始化、消费和关闭逻辑在 telemetry.go。
type recorder struct {
	stateMu sync.RWMutex

	eventChan   chan Event
	sink        EventSink
	shutdownDone chan struct{}

	dropCount     atomic.Int64
	acceptedCount atomic.Int64
	consumedCount atomic.Int64
	failedCount   atomic.Int64
	panicCount    atomic.Int64

	isRunning  bool
	isShutdown bool
}
