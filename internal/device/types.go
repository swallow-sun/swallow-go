// types.go 放 device 包的类型和常量定义.
//
// 做的事情:
//  1. 定义设备注册和认证使用的稳定常量.
//  2. 定义 Manager、注册参数、注册结果和领域错误类型.
//  3. 约束设备令牌只返回一次,服务端只持有不可逆摘要.
package device

import "github.com/swallow-sun/swallow-go/internal/data"

const (
	// AuthorizationScheme 是设备 Authorization 请求头使用的认证方案.
	AuthorizationScheme = "Device"
	// TokenBytes 是设备随机令牌的原始字节数,32 字节提供 256 位随机强度.
	TokenBytes = 32
	// MaxDeviceNameLength 是设备名称最大字节数.
	MaxDeviceNameLength = 128
	// MaxPlatformLength 是设备平台名称最大字节数.
	MaxPlatformLength = 128
	// MaxCapabilitiesLength 是设备能力 JSON 最大字节数.
	MaxCapabilitiesLength = 16 * 1024

	// ErrorCodeInvalidRegistration 表示设备注册参数不合法.
	ErrorCodeInvalidRegistration = "invalid_device_registration"
	// ErrorCodeNameConflict 表示同一用户已经注册同名设备.
	ErrorCodeNameConflict = "device_name_conflict"
	// ErrorCodeInvalidCredentials 表示设备 ID、令牌或状态无法通过认证.
	ErrorCodeInvalidCredentials = "invalid_device_credentials"
)

// Manager 管理设备注册、令牌生成和设备身份认证.
// repo 由启动组装层注入,设备模块不直接依赖 SQLite 或 GORM 实现.
type Manager struct {
	repo data.Repository // 设备身份数据仓库
}

// RegisterParams 是注册设备所需的领域参数.
type RegisterParams struct {
	UserID           int64  // 设备所属用户 ID,必须来自主人认证结果
	Name             string // 用户可读设备名称,同一用户下不可重复
	Platform         string // 设备运行平台,例如 linux-arm64
	CapabilitiesJSON string // 设备能力 JSON,为空时按空对象处理
}

// RegisterResult 是设备注册成功结果.
// Token 只在本次结果中出现,服务端数据库不会保存明文.
type RegisterResult struct {
	Device data.Device // 已持久化的设备公开信息
	Token  string      // 只返回一次的设备认证令牌
}

// DomainError 是设备模块返回给上层的稳定领域错误.
type DomainError struct {
	Code string // 稳定业务错误码,供 service/handler 映射 HTTP 响应
}
