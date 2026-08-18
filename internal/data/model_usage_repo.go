// model_usage_repo.go 放模型调用用量记录的 SQLite 数据访问方法.
//
// 做的事情:
//  1. 定义 ormModelUsage 的 TableName 方法,显式指定表名 model_usages.
//  2. 实现 InsertModelUsage:把一次模型调用的 Token 用量和费用估算写进数据库.
//  3. 提供 modelUsageToORM / modelUsageFromORM 转换函数,在业务对象和 ORM 模型之间互转.
//
// 设计要点:
//   - model_usages 独立于 dialogues 表,每次模型调用写一条,不跟对话消息绑定.
//   - NULL 和 0 必须区分:0 = 供应商明确返回没消耗,NULL = 供应商没返回.
//   - 业务对象 ModelUsage 用 *int / *float64 / *int64 指针类型区分零值和 NULL.
package data

import (
	"context"
	"fmt"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// ormModelUsage 对应 model_usages 表:模型调用用量记录.
func (ormModelUsage) TableName() string { return "model_usages" }

// InsertModelUsage 保存一条模型调用的 Token 用量和费用估算记录.
// 每次成功或失败的模型调用都写一条,独立于 dialogues 表,
// 方便按供应商,模型,操作类型聚合统计成本.
//
// 参数 ctx 控制超时,usage 是模型调用的完整用量记录.
// 写入失败返回 error,调用方应记 ERROR 和待补偿标记,不能静默丢失成本数据.
func (r *sqliteRepo) InsertModelUsage(ctx context.Context, usage ModelUsage) error {
	// 业务对象转 ORM 模型
	model := modelUsageToORM(usage)

	// .WithContext(ctx) 挂 context
	// .Create(&model) 执行 INSERT INTO model_usages (...) VALUES (...)
	// 执行完 GORM 自动回填 ID(自增主键)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		logger.Error("model_usages insert failed",
			zap.String("trace_id", model.TraceID),
			zap.Any("row", model),
			zap.Error(err),
		)
		return fmt.Errorf("insert model usage: %w", err)
	}

	// 写库成功后打 Debug 日志,用 zap.Any 打写入后的完整 model
	logger.Debug("model_usages insert succeeded",
		zap.Any("row", model),
	)

	return nil
}

// modelUsageToORM 把业务对象 ModelUsage 转成 ORM 模型.
// string 类型的可空字段转成 *string(空字符串 → nil),
// int/int64/float64 的零值不转 nil(因为 0 是有意义的值,只有指针为 nil 才表示 NULL).
func modelUsageToORM(usage ModelUsage) ormModelUsage {
	return ormModelUsage{
		ID:                  usage.ID,
		RequestID:           stringToPtr(usage.RequestID),
		TraceID:             usage.TraceID,
		SessionID:           stringToPtr(usage.SessionID),
		UserID:              int64ToPtr(usage.UserID),
		DeviceID:            stringToPtr(usage.DeviceID),
		Provider:            usage.Provider,
		Model:               usage.Model,
		Operation:           usage.Operation,
		InputTokens:         usage.InputTokens,
		OutputTokens:        usage.OutputTokens,
		CachedInputTokens:   usage.CachedInputTokens,
		CacheMissTokens:     usage.CacheMissTokens,
		CacheCreationTokens: usage.CacheCreationTokens,
		ReasoningTokens:     usage.ReasoningTokens,
		TotalTokens:         usage.TotalTokens,
		InputAudioSeconds:   usage.InputAudioSeconds,
		OutputAudioSeconds:  usage.OutputAudioSeconds,
		InputImageCount:     usage.InputImageCount,
		Currency:            stringToPtr(usage.Currency),
		EstimatedCostMicros: usage.EstimatedCostMicros,
		ProviderRequestID:   stringToPtr(usage.ProviderRequestID),
		Status:              usage.Status,
		DurationMs:          usage.DurationMs,
		OccurredAt:          usage.OccurredAt,
	}
}

// modelUsageFromORM 把 ORM 模型转回业务对象.
// 指针字段转成普通类型:nil → 零值.
func modelUsageFromORM(model ormModelUsage) ModelUsage {
	return ModelUsage{
		ID:                  model.ID,
		RequestID:           ptrToString(model.RequestID),
		TraceID:             model.TraceID,
		SessionID:           ptrToString(model.SessionID),
		UserID:              ptrToInt64(model.UserID),
		DeviceID:            ptrToString(model.DeviceID),
		Provider:            model.Provider,
		Model:               model.Model,
		Operation:           model.Operation,
		InputTokens:         model.InputTokens,
		OutputTokens:        model.OutputTokens,
		CachedInputTokens:   model.CachedInputTokens,
		CacheMissTokens:     model.CacheMissTokens,
		CacheCreationTokens: model.CacheCreationTokens,
		ReasoningTokens:     model.ReasoningTokens,
		TotalTokens:         model.TotalTokens,
		InputAudioSeconds:   model.InputAudioSeconds,
		OutputAudioSeconds:  model.OutputAudioSeconds,
		InputImageCount:     model.InputImageCount,
		Currency:            ptrToString(model.Currency),
		EstimatedCostMicros: model.EstimatedCostMicros,
		ProviderRequestID:   ptrToString(model.ProviderRequestID),
		Status:              model.Status,
		DurationMs:          model.DurationMs,
		OccurredAt:          model.OccurredAt,
	}
}

// stringToPtr 把 string 转成 *string,空字符串返回 nil.
// 数据库字段允许 NULL 时,空字符串没有意义,统一存 NULL.
func stringToPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// int64ToPtr 把 int64 转成 *int64,0 返回 nil.
// 数据库 user_id 字段允许 NULL,0 表示不关联用户,统一存 NULL.
func int64ToPtr(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

// IntPtr 把 int 转成 *int.
// Token 数为 0 是有意义的值(供应商明确返回没消耗),所以 0 不转 nil.
// 只有传 nil 才表示"供应商没返回",但 Go 里 int 不能为 nil,
// 所以 service 层构造 ModelUsage 时用这个函数把 int 取地址.
func IntPtr(v int) *int {
	return &v
}

// ptrToString 把 *string 转成 string,nil 返回空字符串.
func ptrToString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ptrToInt64 把 *int64 转成 int64,nil 返回 0.
func ptrToInt64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
