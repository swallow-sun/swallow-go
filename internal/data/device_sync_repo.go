// device_sync_repo.go 放设备同步日志的 SQLite 数据访问方法.
//
// 做的事情:
//  1. InsertDeviceSyncLog: 原子插入一条同步日志, 用 ON CONFLICT DO NOTHING 做幂等.
//     设备重试同一批次时, 已存在的条目不会重复入库.
package data

import (
	"context"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// InsertDeviceSyncLog 原子插入一条设备同步日志.
// 用 (device_id, item_id) 唯一索引做幂等: 重复条目不会插入.
// 返回 created=true 表示这是首次接收, created=false 表示已存在.
func (r *gormRepo) InsertDeviceSyncLog(ctx context.Context, log DeviceSyncLog) (bool, error) {
	model := ormDeviceSyncLog{
		DeviceID:   log.DeviceID,
		UserID:     log.UserID,
		ItemID:     log.ItemID,
		ItemType:   log.ItemType,
		Payload:    log.Payload,
		ReceivedAt: time.Now(),
	}

	// clause.OnConflict{DoNothing: true} 对应 INSERT ... ON CONFLICT DO NOTHING.
	// SQLite 唯一索引冲突时跳过插入, 不报错.
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		DoNothing: true,
	}).Create(&model)

	if result.Error != nil {
		// gorm.ErrDuplicatedKey 在 OnConflict DoNothing 时不触发,
		// 但防御性处理一下.
		if result.Error == gorm.ErrDuplicatedKey {
			return false, nil
		}
		logger.Error("device_sync_log insert failed",
			zap.String("device_id", log.DeviceID),
			zap.String("item_id", log.ItemID),
			zap.Error(result.Error),
		)
		return false, result.Error
	}

	// RowsAffected == 0 表示冲突跳过了, == 1 表示成功插入.
	created := result.RowsAffected > 0

	logger.Debug("device_sync_log insert succeeded",
		zap.String("device_id", log.DeviceID),
		zap.String("item_id", log.ItemID),
		zap.String("item_type", log.ItemType),
		zap.Bool("created", created),
	)

	return created, nil
}

// DeleteDeviceSyncLog 删除尚未成功处理的同步占位记录。
// 业务处理失败时必须删除它，否则设备重试会被误判为“已经处理”。
func (r *gormRepo) DeleteDeviceSyncLog(ctx context.Context, deviceID, itemID string) error {
	return r.db.WithContext(ctx).
		Where("device_id = ? AND item_id = ?", deviceID, itemID).
		Delete(&ormDeviceSyncLog{}).Error
}
