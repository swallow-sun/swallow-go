// Package data 是数据访问层。
// 用 Repository 接口抽象所有数据库操作，
// Phase 1-4 用 SQLite，Phase 5+ 换 MySQL/PG 只换实现不改业务代码。
package data

import (
	"context"
)

// WriteEvent 实现 telemetry.EventSink 接口
// 把 telemetry 传来的数据翻译成 Repository 能接受的参数格式
func (a EventSinkAdapter) WriteEvent(ctx context.Context, eventType, traceID, data string, durationMs int64, success bool) error {
	return a.Repo.InsertEvent(ctx, eventType, nil, data, durationMs, success, traceID)
}
