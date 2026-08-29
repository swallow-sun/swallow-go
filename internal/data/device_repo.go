// device_repo.go 放设备身份的 SQLite 数据访问方法.
//
// 做的事情:
//  1. 创建设备记录,数据库只接收设备令牌摘要.
//  2. 按设备 UUID 查询完整设备身份和状态.
//  3. 设备认证成功后更新最近在线时间.
package data

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// TableName 指定设备 ORM 模型对应 devices 表.
func (ormDevice) TableName() string { return "devices" }

// CreateDevice 创建一台设备并返回数据库中的完整记录.
// 日志只记录公开标识,不记录令牌摘要.
func (r *gormRepo) CreateDevice(ctx context.Context, device Device) (Device, error) {
	model := deviceToORM(device)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		if isUniqueConstraintError(err) {
			return Device{}, fmt.Errorf("create device: %w", errors.New(ErrDuplicatedKey))
		}
		return Device{}, fmt.Errorf("create device: %w", repositoryError(err))
	}
	logger.Info("device registered",
		zap.String("device_id", model.ID),
		zap.Int64("user_id", model.UserID),
		zap.String("platform", model.Platform),
	)
	return deviceFromORM(model), nil
}

// GetDevice 按设备 UUID 查询完整认证记录.
// 返回值包含 TokenHash,只能在服务端认证链路内部使用,不得直接序列化给客户端.
func (r *gormRepo) GetDevice(ctx context.Context, id string) (Device, error) {
	var model ormDevice
	if err := r.db.WithContext(ctx).Select(deviceColumns).First(&model, "id = ?", id).Error; err != nil {
		return Device{}, fmt.Errorf("get device: %w", repositoryError(err))
	}
	return deviceFromORM(model), nil
}

// UpdateDeviceLastSeen 更新设备最近认证成功时间.
// WHERE 同时限制 active 状态,避免设备在认证查询后被吊销却仍被刷新在线时间.
func (r *gormRepo) UpdateDeviceLastSeen(ctx context.Context, id string, at time.Time) error {
	result := r.db.WithContext(ctx).Model(&ormDevice{}).
		Where("id = ? AND status = ?", id, DeviceStatusActive).
		Update("last_seen_at", at)
	if result.Error != nil {
		return fmt.Errorf("update device last seen: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("update device last seen: device is not active")
	}
	return nil
}

// deviceToORM 把设备业务对象转换成 ORM 模型.
func deviceToORM(device Device) ormDevice {
	return ormDevice{
		ID:               device.ID,
		UserID:           device.UserID,
		Name:             device.Name,
		Platform:         device.Platform,
		TokenHash:        device.TokenHash,
		Status:           device.Status,
		CapabilitiesJSON: device.CapabilitiesJSON,
		CreatedAt:        device.CreatedAt,
		LastSeenAt:       device.LastSeenAt,
		RevokedAt:        device.RevokedAt,
	}
}

// deviceFromORM 把设备 ORM 模型转换成业务对象.
func deviceFromORM(model ormDevice) Device {
	return Device{
		ID:               model.ID,
		UserID:           model.UserID,
		Name:             model.Name,
		Platform:         model.Platform,
		TokenHash:        model.TokenHash,
		Status:           model.Status,
		CapabilitiesJSON: model.CapabilitiesJSON,
		CreatedAt:        model.CreatedAt,
		LastSeenAt:       model.LastSeenAt,
		RevokedAt:        model.RevokedAt,
	}
}
