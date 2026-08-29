// price_snapshot_repo.go 放模型价格快照的 SQLite 数据访问方法.
//
// 做的事情:
//  1. 实现 GetPriceSnapshot: 查询指定供应商+模型在指定时间点的有效价格快照.
//  2. 提供 priceSnapshotFromORM 转换函数, 在 ORM 模型和业务对象之间转换.
//
// 设计要点:
//   - 价格快照是按时间版本管理的, 同一个供应商+模型可以有多条记录, 每条有一个 effective_from 时间.
//   - 查询时找到 effective_from <= 指定时间 的最新一条, 就是那个时间点的有效价格.
//   - 找不到价格时不报错, 返回 ErrPriceNotFound, 调用方决定是跳过费用估算还是报错.
package data

import (
	"context"
	"errors"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// GetPriceSnapshot 查询指定供应商+模型在指定时间点的有效价格快照.
// 按 effective_from <= at 的条件找最新一条, 就是那个时间点的有效价格.
// 找不到返回 ModelPriceSnapshot 零值和 ErrPriceNotFound, 调用方决定是跳过费用估算还是报错.
// TableName 指定 model_price_snapshots 表名。
func (ormModelPriceSnapshot) TableName() string { return "model_price_snapshots" }

func (r *gormRepo) GetPriceSnapshot(ctx context.Context, provider, model string, at time.Time) (ModelPriceSnapshot, error) {
	var orm ormModelPriceSnapshot

	// 查询条件: provider 和 model 匹配, effective_from <= 指定时间, 按 effective_from 倒序取第一条.
	// .Select(modelPriceSnapshotColumns) 只查需要的列, 不用 SELECT *.
	// .Where("provider = ? AND model = ? AND effective_from <= ?", ...) 按供应商+模型+时间过滤.
	// .Order("effective_from DESC") 按生效时间倒序, 最新的排最前面.
	// .Take(&orm) 取一条记录, 找不到返回 gorm.ErrRecordNotFound.
	err := r.db.WithContext(ctx).
		Select(modelPriceSnapshotColumns).
		Where("provider = ? AND model = ? AND effective_from <= ?", provider, model, at).
		Order("effective_from DESC").
		Take(&orm).Error

	// 没找到价格快照, 返回 ErrPriceNotFound
	if errors.Is(err, gorm.ErrRecordNotFound) {
		logger.Debug("price snapshot not found, skip cost estimation",
			zap.String("provider", provider),
			zap.String("model", model),
			zap.Time("at", at),
		)
		return ModelPriceSnapshot{}, errors.New(ErrPriceNotFound)
	}

	// 其他数据库错误, 打日志返回
	if err != nil {
		logger.Error("price snapshot query failed",
			zap.String("provider", provider),
			zap.String("model", model),
			zap.Time("at", at),
			zap.Error(err),
		)
		return ModelPriceSnapshot{}, err
	}

	// 只记录价格版本标识，不输出完整价格记录。
	logger.Debug("price snapshot found",
		zap.Int64("price_snapshot_id", orm.ID),
		zap.String("provider", orm.Provider),
		zap.String("model", orm.Model),
	)

	return priceSnapshotFromORM(orm), nil
}

// priceSnapshotFromORM 把 ORM 模型转回业务对象.
// 指针字段直接搬过去(业务对象里也是指针).
func priceSnapshotFromORM(model ormModelPriceSnapshot) ModelPriceSnapshot {
	return ModelPriceSnapshot{
		ID:                 model.ID,
		Provider:           model.Provider,
		Model:              model.Model,
		EffectiveFrom:      model.EffectiveFrom,
		InputPrice:         model.InputPrice,
		OutputPrice:        model.OutputPrice,
		CachedInputPrice:   model.CachedInputPrice,
		CacheCreationPrice: model.CacheCreationPrice,
		Unit:               model.Unit,
		Currency:           model.Currency,
		SourceVersion:      ptrToString(model.SourceVersion),
		CreatedAt:          model.CreatedAt,
	}
}
