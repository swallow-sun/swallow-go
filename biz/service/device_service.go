// device_service.go 放设备身份相关的业务编排.
//
// 做的事情:
//  1. 使用当前 owner 用户注册嵌入式设备.
//  2. 校验设备 UUID 和设备令牌,返回经过认证的设备身份.
//  3. 隔离 HTTP 参数解析和 internal/device 领域逻辑.
package service

import (
	"context"
	"fmt"

	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/device"
)

// NewDeviceService 创建设备身份业务服务.
func NewDeviceService(deps *Deps) *DeviceService {
	return &DeviceService{
		manager: device.NewManager(deps.repo),
		ownerID: deps.ownerID,
	}
}

// RegisterDevice 为当前 owner 注册一台设备.
// name、platform 和 capabilitiesJSON 来自已经完成主人认证的 handler;
// 返回的 Token 是唯一一次明文交付机会,service 不记录也不再次持久化它.
func (s *DeviceService) RegisterDevice(
	ctx context.Context,
	name, platform, capabilitiesJSON string,
) (RegisterDeviceResult, error) {
	result, err := s.manager.Register(ctx, device.RegisterParams{
		UserID:           s.ownerID,
		Name:             name,
		Platform:         platform,
		CapabilitiesJSON: capabilitiesJSON,
	})
	if err != nil {
		return RegisterDeviceResult{}, fmt.Errorf("register device: %w", err)
	}
	return RegisterDeviceResult{Device: result.Device, Token: result.Token}, nil
}

// AuthenticateDevice 校验设备 UUID 与令牌并返回可信设备身份.
// 上层必须使用返回对象中的 UserID 做数据隔离,不能采用请求体声明的用户 ID.
func (s *DeviceService) AuthenticateDevice(ctx context.Context, deviceID, token string) (data.Device, error) {
	registered, err := s.manager.Authenticate(ctx, deviceID, token)
	if err != nil {
		return data.Device{}, fmt.Errorf("authenticate device: %w", err)
	}
	return registered, nil
}
