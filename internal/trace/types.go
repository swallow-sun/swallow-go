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

// Span 结束状态常量.
// 写入 spans 表和日志时统一使用这些常量, 调用方不直接传状态字符串.
const (
	// SpanStatusOK 表示调用正常完成.
	SpanStatusOK = "ok"
	// SpanStatusError 表示调用因为错误结束.
	SpanStatusError = "error"
	// SpanStatusCancelled 表示调用被主动取消.
	SpanStatusCancelled = "cancelled"
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
