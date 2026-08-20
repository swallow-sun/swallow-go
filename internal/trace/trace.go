// trace.go 放轻量链路追踪 ID 的工具函数.
//
// 做的事情:
//  1. New: 生成一个新的 trace ID (UUID 带横杠, 36 字符).
//  2. Ensure: 从 context 中取 trace ID, 没有就生成一个新的并塞进 context 返回.
//  3. EnsureFromHeader: 先从 HTTP 请求头读 trace ID, 没有再调 Ensure.
//  4. WithID: 把指定的 trace ID 塞进 context 返回 (用于跨函数传递已有 trace ID).
//  5. FromContext: 从 context 中取 trace ID, 没有返回空字符串.
//
// 用 context.Context 传递 trace_id, 贯穿日志, 埋点, DB.
// 设备端通过 X-Trace-Id 请求头把 trace_id 传给服务端,
// 确保录音-ASR-Go-TTS 整条链路用同一个 trace ID.
// Phase 4 升级 OpenTelemetry 时, trace ID 格式兼容 (UUID/十六进制).
package trace

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// New 生成一个新的 trace ID(UUID v4,带横杠,36 字符).
func New() string {
	return uuid.NewString()
}

// WithID 把 trace ID 放入 context,返回新 context.
func WithID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceKey{}, traceID)
}

// FromContext 从 context 提取 trace ID.
// 不存在返回空字符串.
func FromContext(ctx context.Context) string {
	if v, ok := ctx.Value(traceKey{}).(string); ok {
		return v
	}
	return ""
}

// Ensure 从 context 取 trace ID,不存在则生成一个新的并放入 context.
// 返回(新 context, trace ID).方便在入口处调用:
//
//	ctx, traceID := trace.Ensure(ctx)
func Ensure(ctx context.Context) (context.Context, string) {
	if id := FromContext(ctx); id != "" {
		return ctx, id
	}
	id := New()
	return WithID(ctx, id), id
}

// EnsureFromHeader 先从 HTTP 请求头 "X-Trace-Id" 读取 trace ID,
// 有就用它 (跨进程传递), 没有就调 Ensure 生成一个新的.
// 设备端在录音-ASR-Go-TTS 整条链路里用同一个 trace ID,
// 通过请求头把它带给服务端, 服务端不能自己重新生成导致断链.
func EnsureFromHeader(ctx context.Context, headerValue string) (context.Context, string) {
	if id := strings.TrimSpace(headerValue); id != "" {
		return WithID(ctx, id), id
	}
	return Ensure(ctx)
}
