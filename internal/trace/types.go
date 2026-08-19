// types.go 放 trace 包的类型定义.
//
// 做的事情:
//
//	定义 traceKey 类型:trace ID 在 context.Context 中使用的私有键,防止外部包直接访问.
package trace

import (
	"context"
	"time"
)

// traceKey 是 trace ID 在 context.Context 中使用的私有键类型.
type traceKey struct{}

type spanKey struct{}

type SpanSink interface {
	WriteSpan(ctx context.Context, span Span) error
}

type Span struct {
	ID           string         // Span 唯一标识
	TraceID      string         // 整条调用链共享的标识
	ParentSpanID string         // 父 Span；根节点为空
	Component    string         // 执行组件
	Operation    string         // 执行操作
	Status       string         // ok、error 或 cancelled
	DurationMs   int64          // 耗时，单位毫秒
	StartedAt    time.Time      // 开始时间
	FinishedAt   time.Time      // 结束时间
	Attributes   map[string]any // 不含敏感正文的扩展属性
}
