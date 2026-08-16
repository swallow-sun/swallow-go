// Package trace 提供轻量链路追踪 ID。
// 用 context.Context 传递 trace_id，贯穿日志、埋点、DB。
// Phase 4 升级 OpenTelemetry 时，trace ID 格式兼容（UUID/十六进制）。
package trace

import (
	"context"

	"github.com/google/uuid"
)

// New 生成一个新的 trace ID（UUID 去掉横杠，32 字符十六进制）。
func New() string {
	return uuid.NewString()
}

// WithID 把 trace ID 放入 context，返回新 context。
func WithID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceKey{}, traceID)
}

// FromContext 从 context 提取 trace ID。
// 不存在返回空字符串。
func FromContext(ctx context.Context) string {
	if v, ok := ctx.Value(traceKey{}).(string); ok {
		return v
	}
	return ""
}

// Ensure 从 context 取 trace ID，不存在则生成一个新的并放入 context。
// 返回（新 context, trace ID）。方便在入口处调用：
//
//	ctx, traceID := trace.Ensure(ctx)
func Ensure(ctx context.Context) (context.Context, string) {
	if id := FromContext(ctx); id != "" {
		return ctx, id
	}
	id := New()
	return WithID(ctx, id), id
}
