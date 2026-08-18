// span.go 放 Span 的创建、上下文传递和结束逻辑。
//
// 做的事情：
//  1. StartSpan：创建一个 Span，如果 context 里有父 Span 就挂上去，同时把自己塞进 context 返回。
//  2. Span.End：结束一个 Span，算耗时，通过 Sink 写库（异步）。
//  3. WithSpan / SpanFromContext：在 context 里传递当前 Span，让子调用能找到父 Span。
//
// Span 组成调用链树：
//
//	Handler Span（根）
//	├── ChatService Span
//	    └── ModelProvider Span
//
// 每层调 StartSpan 时从 context 里取父 Span ID，没有就是根 Span。
// End 时算耗时、设状态，把完整 Span 记录异步写进 spans 表。
package trace

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// spanKey 是 Span 在 context.Context 中使用的私有键类型，防止外部包直接访问。
type spanKey struct{}

// SpanSink 是 Span 写库的目标接口。
// 由 data 层实现（把 Span 写进 SQLite spans 表）。
// trace 包不直接依赖 data 包，通过接口传进来，避免循环依赖。
type SpanSink interface {
	// WriteSpan 把一个完整的 Span 记录写进数据库。
	// ctx 控制超时，span 是完整的 Span 数据（已 End）。
	WriteSpan(ctx context.Context, span Span) error
}

// globalSink 是当前进程唯一的 Span 写库目标。
// nil 表示只打日志不写库。
var globalSink SpanSink

// SetSpanSink 设置 Span 写库目标，一般在程序启动时调一次。
func SetSpanSink(s SpanSink) {
	globalSink = s
}

// Span 表示一个处理步骤的追踪记录。
// 一个 Span 对应一次请求经过的一个组件（handler、service、model_provider）。
type Span struct {
	// ID 是这个 Span 的唯一标识（UUID），同时是 spans 表的主键
	ID string
	// TraceID 是链路追踪 ID，同一次请求的所有 Span 共享同一个 trace_id
	TraceID string
	// ParentSpanID 是父 Span 的 ID，根 Span 为空字符串
	ParentSpanID string
	// Component 是组件名：handler / chat_service / model_provider
	Component string
	// Operation 是操作名：POST /api/chat、stream_loop、llm.stream 等
	Operation string
	// Status 是状态：ok / error / cancelled
	Status string
	// DurationMs 是耗时（毫秒），0 表示步骤极快
	DurationMs int64
	// StartedAt 是开始时间
	StartedAt time.Time
	// FinishedAt 是结束时间，零值表示未结束
	FinishedAt time.Time
	// Attributes 是附加属性（model、error_code 等），写库时转成 JSON
	Attributes map[string]any
}

// StartSpan 创建一个 Span 并塞进 context。
// 如果 context 里有父 Span，就把它的 ID 设为 ParentSpanID。
// 返回新 context（含当前 Span）和 Span 指针，调完用 defer span.End() 收尾。
//
// 用法：
//
//	ctx, span := trace.StartSpan(ctx, "handler", "POST /api/chat")
//	defer span.End()
//	// ... 业务逻辑 ...
func StartSpan(ctx context.Context, component, operation string) (context.Context, *Span) {
	// 从 context 里取 trace ID，没有就是空字符串
	traceID := FromContext(ctx)
	// 从 context 里取父 Span，没有就是 nil（说明这是根 Span）
	parent := SpanFromContext(ctx)
	// 父 Span ID，没有父 Span 就是空字符串
	parentID := ""
	if parent != nil {
		parentID = parent.ID
	}

	// 构造一个新 Span
	span := &Span{
		ID:           uuid.NewString(), // 生成新的 Span ID（UUID）
		TraceID:      traceID,           // 从 context 里拿的 trace ID
		ParentSpanID: parentID,          // 父 Span ID，根 Span 为空
		Component:    component,         // 组件名
		Operation:    operation,         // 操作名
		Status:       "ok",              // 默认成功，出错了改成 error 或 cancelled
		StartedAt:    time.Now(),        // 记录开始时间
		Attributes:   make(map[string]any), // 附加属性，初始空 map
	}

	// 把这个 Span 塞进 context，返回新 context
	// 后面子调用调 StartSpan 时，SpanFromContext 就能取到这个 Span 作为父
	return WithSpan(ctx, span), span
}

// WithSpan 把 Span 塞进 context，返回新 context。
func WithSpan(ctx context.Context, span *Span) context.Context {
	return context.WithValue(ctx, spanKey{}, span)
}

// SpanFromContext 从 context 里取当前 Span，没有返回 nil。
func SpanFromContext(ctx context.Context) *Span {
	if v, ok := ctx.Value(spanKey{}).(*Span); ok {
		return v
	}
	return nil
}

// End 结束一个 Span：算耗时，把状态设好，异步写库。
// status 是结束状态："ok"、"error" 或 "cancelled"。
// 调完这个方法后 Span 就是完整的了，可以安全写库。
func (s *Span) End(status string) {
	// 记录结束时间
	s.FinishedAt = time.Now()
	// 算耗时（毫秒），StartedAt 一定不为零值
	s.DurationMs = s.FinishedAt.Sub(s.StartedAt).Milliseconds()
	s.Status = status

	// 把 Span 写库（如果设了 Sink）
	s.flush()
}

// EndOK 是 End("ok") 的简写，正常结束用。
func (s *Span) EndOK() {
	s.End("ok")
}

// EndError 是 End("error") 的简写，出错时用。
// 可以传 attrs 附加属性（如 error_code），会合并到 Span 的 Attributes 里。
func (s *Span) EndError(attrs map[string]any) {
	// 合并附加属性
	for k, v := range attrs {
		s.Attributes[k] = v
	}
	s.End("error")
}

// flush 把 Span 写库（异步）。
// globalSink 为 nil 时只打日志不写库。
func (s *Span) flush() {
	// 打 Debug 日志，方便开发时看 Span 链路
	logger.Debug("span 完成",
		zap.String("span_id", s.ID),
		zap.String("trace_id", s.TraceID),
		zap.String("parent_span_id", s.ParentSpanID),
		zap.String("component", s.Component),
		zap.String("operation", s.Operation),
		zap.String("status", s.Status),
		zap.Int64("duration_ms", s.DurationMs),
	)

	// 没设 Sink 就只打日志，不写库
	if globalSink == nil {
		return
	}

	// 用独立的 context 写库，不依赖业务 ctx（可能已超时取消）
	// context.Background() 不会被取消，保证 Span 一定能写进去
	// 但加 3 秒超时，防止写库卡住 goroutine
	writeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 调 Sink 把 Span 写进数据库
	if err := globalSink.WriteSpan(writeCtx, *s); err != nil {
		// 写库失败只打日志，不影响业务流程
		logger.Error("写入 span 失败",
			zap.String("span_id", s.ID),
			zap.String("trace_id", s.TraceID),
			zap.Error(err),
		)
	}
}

// SetAttr 给 Span 加附加属性，在 End 之前调。
// 比如 SetAttr("model", "deepseek-chat") 或 SetAttr("error_code", "stream_read_failed")。
func (s *Span) SetAttr(key string, value any) {
	if s.Attributes == nil {
		s.Attributes = make(map[string]any)
	}
	s.Attributes[key] = value
}

// MarshalAttributes 把 Attributes 序列化成 JSON 字符串，写库用。
// 返回 JSON 字符串和 error。Attributes 为空时返回空字符串。
func (s *Span) MarshalAttributes() (string, error) {
	if len(s.Attributes) == 0 {
		return "", nil
	}
	data, err := json.Marshal(s.Attributes)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
