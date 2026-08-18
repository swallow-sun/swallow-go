// span_repo.go 放 Span 追踪记录的 SQLite 数据访问方法。
//
// 做的事情：
//  1. 定义 ormSpan 的 TableName 方法，显式指定表名 spans。
//  2. 实现 InsertSpan：把一个完整的 Span 记录写进数据库。
//  3. 提供 SpanToORM 转换函数，在业务对象和 ORM 模型之间互转。
package data

import (
	"context"
	"fmt"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// InsertSpan 保存一条 Span 追踪记录。
// Span 在调用链里表示一个处理步骤（handler、service、model_provider），
// 多个 Span 共享同一个 trace_id，通过 parent_span_id 组成父子树。
//
// 参数 ctx 控制超时，span 是完整的 Span 数据（已 End）。
// 写入失败返回 error，调用方应记 ERROR 但不阻断主流程（Span 是观测数据，不是业务数据）。
func (r *sqliteRepo) InsertSpan(ctx context.Context, span Span) error {
	// 业务对象转 ORM 模型
	model := spanToORM(span)

	// .WithContext(ctx) 挂 context
	// .Create(&model) 执行 INSERT INTO spans (...) VALUES (...)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		logger.Error("spans 写入失败",
			zap.String("span_id", model.ID),
			zap.String("trace_id", model.TraceID),
			zap.Any("row", model),
			zap.Error(err),
		)
		return fmt.Errorf("insert span: %w", err)
	}

	// 写库成功后打 Debug 日志
	logger.Debug("spans 写入成功",
		zap.Any("row", model),
	)
	return nil
}

// spanToORM 把业务对象 Span 转成 ORM 模型。
// ParentSpanID 空字符串转成 nil（根 Span 的 parent 为 NULL）。
// Attributes 空字符串转成 nil。
// FinishedAt 零值转成 nil。
func spanToORM(span Span) ormSpan {
	// ParentSpanID：空字符串 → nil（根 Span）
	var parentID *string
	if span.ParentSpanID != "" {
		parentID = &span.ParentSpanID
	}

	// Attributes：空字符串 → nil
	var attrs *string
	if span.Attributes != "" {
		attrs = &span.Attributes
	}

	// FinishedAt：零值 → nil（异常退出没来得及标记）
	var finishedAt *time.Time
	if !span.FinishedAt.IsZero() {
		finishedAt = &span.FinishedAt
	}

	return ormSpan{
		ID:           span.ID,
		TraceID:      span.TraceID,
		ParentSpanID: parentID,
		Component:    span.Component,
		Operation:    span.Operation,
		Status:       span.Status,
		DurationMs:   span.DurationMs,
		StartedAt:    span.StartedAt,
		FinishedAt:   finishedAt,
		Attributes:   attrs,
	}
}
