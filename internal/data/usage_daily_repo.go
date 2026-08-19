// usage_daily_repo.go 放模型用量日聚合的 SQLite 数据访问方法.
//
// 做的事情:
//  1. 实现 UpsertModelUsageDaily: 把一条原始 model_usages 记录聚合到日表.
//  2. 实现 GetDailyUsage: 按日期范围查日聚合数据, 供看板查询.
//  3. 提供 modelUsageDailyToORM / modelUsageDailyFromORM 转换函数.
//
// 设计要点:
//   - 聚合粒度: date + device_id + user_id + provider + model + operation.
//   - 并发累加: 使用 GORM 的 OnConflict 表达聚合更新，不在 Go 文件中维护完整 SQL 语句.
//   - 聚合任务失败不能丢失原始记录, 修复后可以按日期重新计算.
package data

import (
	"context"
	"fmt"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpsertModelUsageDaily 把一条原始 model_usages 记录聚合到日表.
// 按 date + device_id + user_id + provider + model + operation 聚合,
// 存在就累加 request_count 和 token 数, 不存在就插入新行.
// 方案 15.7 节: 原始用量写入成功后更新日表；原始记录仍是重新聚合的事实来源.
//
// TableName 指定 model_usage_daily 表名。
func (ormModelUsageDaily) TableName() string { return "model_usage_daily" }

func (r *sqliteRepo) UpsertModelUsageDaily(ctx context.Context, usage ModelUsage) error {
	// 从 occurred_at 提取日期部分(YYYY-MM-DD), 转成 UTC 避免时区问题
	dateStr := usage.OccurredAt.UTC().Format("2006-01-02")

	// 判断这次调用是否失败, 失败的话 failed_count +1
	failedCount := int64(0)
	if usage.Status == ModelUsageStatusFailed {
		failedCount = 1
	}

	// 提取 token 数, nil 的字段当 0 处理(聚合时 0 没影响)
	inputTokens := ptrToIntOrZero(usage.InputTokens)
	outputTokens := ptrToIntOrZero(usage.OutputTokens)
	cachedTokens := ptrToIntOrZero(usage.CachedInputTokens)

	row := ormModelUsageDaily{
		Date: dateStr, DeviceID: usage.DeviceID, UserID: usage.UserID,
		Provider: usage.Provider, Model: usage.Model, Operation: usage.Operation,
		RequestCount: 1, FailedCount: failedCount, InputTokens: inputTokens,
		OutputTokens: outputTokens, CachedInputTokens: cachedTokens,
		EstimatedCostMicros: usage.EstimatedCostMicros,
		Currency:            stringToPtr(usage.Currency),
	}
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "date"}, {Name: "device_id"}, {Name: "user_id"},
			{Name: "provider"}, {Name: "model"}, {Name: "operation"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"request_count":         gorm.Expr("model_usage_daily.request_count + excluded.request_count"),
			"failed_count":          gorm.Expr("model_usage_daily.failed_count + excluded.failed_count"),
			"input_tokens":          gorm.Expr("model_usage_daily.input_tokens + excluded.input_tokens"),
			"output_tokens":         gorm.Expr("model_usage_daily.output_tokens + excluded.output_tokens"),
			"cached_input_tokens":   gorm.Expr("model_usage_daily.cached_input_tokens + excluded.cached_input_tokens"),
			"estimated_cost_micros": gorm.Expr("CASE WHEN excluded.estimated_cost_micros IS NULL THEN model_usage_daily.estimated_cost_micros ELSE COALESCE(model_usage_daily.estimated_cost_micros, 0) + excluded.estimated_cost_micros END"),
			"currency":              gorm.Expr("COALESCE(model_usage_daily.currency, excluded.currency)"),
		}),
	}).Create(&row)

	if result.Error != nil {
		logger.Error("model_usage_daily upsert failed",
			zap.String("trace_id", usage.TraceID),
			zap.String("date", dateStr),
			zap.String("provider", usage.Provider),
			zap.String("model", usage.Model),
			zap.Error(result.Error),
		)
		return fmt.Errorf("upsert model usage daily: %w", result.Error)
	}

	return nil
}

// GetDailyUsage 按日期范围查日聚合数据, 供看板查询.
// dateFrom 和 dateTo 格式为 YYYY-MM-DD, 返回按日期倒序排列的记录.
// 方案 15.7 节: 看板由 Go 服务端提供只读聚合接口, 不允许前端直接连数据库.
func (r *sqliteRepo) GetDailyUsage(ctx context.Context, dateFrom, dateTo string) ([]ModelUsageDaily, error) {
	var rows []ormModelUsageDaily

	// 查询条件: date 在指定范围内, 按日期倒序排列.
	// .Select(modelUsageDailyColumns) 只查需要的列, 不用 SELECT *.
	// .Where("date >= ? AND date <= ?", ...) 按日期范围过滤.
	// .Order("date DESC") 按日期倒序, 最新的排最前面.
	err := r.db.WithContext(ctx).
		Select(modelUsageDailyColumns).
		Where("date >= ? AND date <= ?", dateFrom, dateTo).
		Order("date DESC").
		Find(&rows).Error

	if err != nil {
		logger.Error("model_usage_daily query failed",
			zap.String("date_from", dateFrom),
			zap.String("date_to", dateTo),
			zap.Error(err),
		)
		return nil, fmt.Errorf("query daily usage: %w", err)
	}

	// 批量转成业务对象
	result := make([]ModelUsageDaily, 0, len(rows))
	for _, row := range rows {
		result = append(result, modelUsageDailyFromORM(row))
	}

	logger.Debug("model_usage_daily query succeeded",
		zap.String("date_from", dateFrom),
		zap.String("date_to", dateTo),
		zap.Int("row_count", len(result)),
	)

	return result, nil
}

// modelUsageDailyFromORM 把 ORM 模型转回业务对象.
// 指针字段转成普通类型: nil → 零值.
func modelUsageDailyFromORM(model ormModelUsageDaily) ModelUsageDaily {
	return ModelUsageDaily{
		ID:                  model.ID,
		Date:                model.Date,
		DeviceID:            model.DeviceID,
		UserID:              model.UserID,
		Provider:            model.Provider,
		Model:               model.Model,
		Operation:           model.Operation,
		RequestCount:        model.RequestCount,
		FailedCount:         model.FailedCount,
		InputTokens:         model.InputTokens,
		OutputTokens:        model.OutputTokens,
		CachedInputTokens:   model.CachedInputTokens,
		EstimatedCostMicros: model.EstimatedCostMicros,
		Currency:            ptrToString(model.Currency),
	}
}

// ptrToIntOrZero 把 *int 转成 int64, nil 返回 0.
// 聚合时 nil 的 token 数当 0 处理(供应商没返回的值不参与累加).
func ptrToIntOrZero(v *int) int64 {
	if v == nil {
		return 0
	}
	return int64(*v)
}
