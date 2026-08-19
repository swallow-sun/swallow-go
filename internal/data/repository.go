// repository.go 放 data 包的包级文档和辅助函数.
//
// 做的事情:
//  1. 声明 data 包是数据访问层,用 Repository 接口抽象所有数据库操作.
//  2. 提供 EventSinkAdapter:把 Repository 适配成 telemetry.EventSink 接口.
//  3. 提供 SpanSinkAdapter:把 Repository 适配成 trace.SpanSink 接口.
//  4. 提供 repositoryError 辅助函数:把 GORM 的 ErrRecordNotFound 转成 sql.ErrNoRows.
//
// Phase 1-4 用 SQLite,Phase 5+ 换 MySQL/PG 只换实现不改业务代码.
package data

import (
	"context"

	"github.com/swallow-sun/swallow-go/internal/trace"
)

// WriteEvent 实现 telemetry.EventSink 接口.
// telemetry 那边只认 EventSink 接口,不关心数据库怎么存;
// 这个函数把 telemetry 传来的数据翻译成 Repository.InsertEvent 能接受的参数格式,
// 起一个适配器的作用.
// 参数:
//   - eventType: 事件类型,比如 "chat_request","llm_call"
//   - traceID: 链路追踪 ID,方便把多个事件串起来看
//   - data: 事件数据,JSON 字符串
//   - durationMs: 耗时毫秒数
//   - success: 这次事件对应的操作成功没有
func (a EventSinkAdapter) WriteEvent(ctx context.Context, eventType, traceID, data string, durationMs int64, success bool) error {
	// a.Repo 是 EventSinkAdapter 里持有的 Repository 实例
	// 直接调 InsertEvent,把参数透传过去
	// 第三个参数传 nil,意思是这个事件不关联某个具体用户(telemetry 事件大多是系统级的)
	return a.Repo.InsertEvent(ctx, eventType, nil, data, durationMs, success, traceID)
}

// WriteSpan 实现 trace.SpanSink 接口.
// 把 trace.Span 转成 data.Span,再调 Repository.InsertSpan 写库.
func (a SpanSinkAdapter) WriteSpan(ctx context.Context, span trace.Span) error {
	// trace.Span 的 Attributes 是 map[string]any,需要序列化成 JSON 字符串
	attrs, err := span.MarshalAttributes()
	if err != nil {
		return err
	}

	// 构造 data.Span 业务对象
	ds := Span{
		ID:           span.ID,
		TraceID:      span.TraceID,
		ParentSpanID: span.ParentSpanID,
		Component:    span.Component,
		Operation:    span.Operation,
		Status:       span.Status,
		DurationMs:   span.DurationMs,
		StartedAt:    span.StartedAt,
		FinishedAt:   span.FinishedAt,
		Attributes:   attrs,
	}
	return a.Repo.InsertSpan(ctx, ds)
}
