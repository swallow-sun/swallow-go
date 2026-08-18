// types.go 放 telemetry 包的类型定义和常量。
//
// 做的事情：
//  1. 定义事件类型常量：dialogue、memory_query、llm.call、llm.stream、llm.stream.complete。
//  2. 定义通用字段名和状态值常量：status（ok/error/connected）、ms（耗时毫秒）。
//  3. 定义 Event 结构体：一条埋点事件，包含类型、数据、trace ID、时间戳等。
//  4. 定义 EventSink 接口：持久化 Sink，由 data.EventSinkAdapter 实现，免得 telemetry 直接依赖 data 包。
//  5. 定义 Stats 和 recorder：原子计数器统计事件数，用于运行时观察埋点队列状态。
package telemetry

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultBufferSize 是调用 Init 时没传有效容量时用的默认队列长度。
	DefaultBufferSize = 256

	// DefaultSinkWriteTimeout 是单条事件写进持久化 Sink 的最长等待时间。
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
// 实现必须用传进来的 Context 做阻塞操作，Context 取消后赶紧返回。
// 返回 error 说明这次事件没写成功。
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
