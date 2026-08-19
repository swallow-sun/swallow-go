// manager.go 放设备注册和认证领域逻辑.
//
// 做的事情:
//  1. 生成设备 UUID 和高强度随机认证令牌.
//  2. 只保存令牌 SHA-256 摘要,明文只在注册响应中出现一次.
//  3. 使用常量时间比较认证设备,并更新最近在线时间.
package device

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/telemetry"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// Error 返回稳定错误码,使 *DomainError 满足 Go 标准 error 接口.
func (e *DomainError) Error() string { return e.Code }

// NewManager 创建一个设备身份管理器.
func NewManager(repo data.Repository) *Manager {
	return &Manager{repo: repo}
}

// Register 为指定用户注册一台新设备并返回只出现一次的明文令牌.
// 方法会校验设备名称、平台和能力 JSON,随后生成设备 UUID 与 256 位随机令牌.
// 数据库只保存令牌的 SHA-256 摘要;调用方必须把返回的 Token 立即交给设备安全保存.
func (m *Manager) Register(ctx context.Context, params RegisterParams) (RegisterResult, error) {
	params.Name = strings.TrimSpace(params.Name)
	params.Platform = strings.TrimSpace(params.Platform)
	params.CapabilitiesJSON = strings.TrimSpace(params.CapabilitiesJSON)
	if params.CapabilitiesJSON == "" {
		params.CapabilitiesJSON = "{}"
	}
	if params.UserID <= 0 || params.Name == "" || len(params.Name) > MaxDeviceNameLength ||
		len(params.Platform) > MaxPlatformLength || len(params.CapabilitiesJSON) > MaxCapabilitiesLength ||
		!json.Valid([]byte(params.CapabilitiesJSON)) {
		return RegisterResult{}, &DomainError{Code: ErrorCodeInvalidRegistration}
	}

	token, err := generateToken()
	if err != nil {
		return RegisterResult{}, fmt.Errorf("generate device token: %w", err)
	}
	deviceID := uuid.NewString()
	created, err := m.repo.CreateDevice(ctx, data.Device{
		ID:               deviceID,
		UserID:           params.UserID,
		Name:             params.Name,
		Platform:         params.Platform,
		TokenHash:        hashToken(token),
		Status:           data.DeviceStatusActive,
		CapabilitiesJSON: params.CapabilitiesJSON,
		CreatedAt:        time.Now(),
	})
	if err != nil {
		if strings.Contains(err.Error(), data.ErrDuplicatedKey) {
			return RegisterResult{}, &DomainError{Code: ErrorCodeNameConflict}
		}
		return RegisterResult{}, fmt.Errorf("persist device: %w", err)
	}
	telemetry.Emit(ctx, telemetry.EventDeviceRegistered, map[string]any{
		"device_id":           created.ID,
		"user_id":             created.UserID,
		telemetry.FieldStatus: telemetry.StatusOK,
	})
	return RegisterResult{Device: created, Token: token}, nil
}

// Authenticate 校验设备 UUID 和明文令牌.
// 任何失败都返回相同错误码,避免向攻击者暴露设备是否存在或是否已吊销.
// 成功后返回数据库中的可信设备归属,并更新 last_seen_at 与认证审计事件.
func (m *Manager) Authenticate(ctx context.Context, deviceID, token string) (data.Device, error) {
	deviceID = strings.TrimSpace(deviceID)
	token = strings.TrimSpace(token)
	if deviceID == "" || token == "" {
		return data.Device{}, &DomainError{Code: ErrorCodeInvalidCredentials}
	}
	stored, err := m.repo.GetDevice(ctx, deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return data.Device{}, &DomainError{Code: ErrorCodeInvalidCredentials}
		}
		logger.Error("device authentication query failed", zap.Error(err))
		return data.Device{}, fmt.Errorf("query device for authentication: %w", err)
	}
	providedHash := hashToken(token)
	if stored.Status != data.DeviceStatusActive ||
		subtle.ConstantTimeCompare([]byte(providedHash), []byte(stored.TokenHash)) != 1 {
		return data.Device{}, &DomainError{Code: ErrorCodeInvalidCredentials}
	}
	lastSeenAt := time.Now()
	if err := m.repo.UpdateDeviceLastSeen(ctx, stored.ID, lastSeenAt); err != nil {
		return data.Device{}, fmt.Errorf("update authenticated device: %w", err)
	}
	stored.LastSeenAt = &lastSeenAt
	telemetry.Emit(ctx, telemetry.EventDeviceAuthenticated, map[string]any{
		"device_id":           stored.ID,
		"user_id":             stored.UserID,
		telemetry.FieldStatus: telemetry.StatusOK,
	})
	return stored, nil
}

// generateToken 使用操作系统密码学随机源生成不带填充的 URL 安全设备令牌.
// RawURLEncoding 不含等号填充,便于设备把令牌放入 Authorization 请求头.
func generateToken() (string, error) {
	raw := make([]byte, TokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// hashToken 返回设备令牌的 SHA-256 十六进制摘要.
// 摘要只用于服务端认证比对,日志和接口响应都不得输出该值.
func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
