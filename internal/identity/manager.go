// manager.go 放 identity.Manager：用户身份和会话管理。
//
// 做的事情：
//  1. LoginOrCreateUser：按用户名查库，有则更新活跃时间，没有则新建（默认 owner 角色）。
//  2. NewSession：为用户创建一条新会话，返回 UUID。
//  3. GetSession：查会话信息（含 user_id）。
//  4. TouchSession：更新会话活跃时间（fire-and-forget，失败只打 Warn 不影响主流程）。
//
// Phase 2：CLI 启动时指定用户名，自动创建或查找用户。
// Phase 5+：加声纹/人脸识别，自动确定用户身份。
package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// New 创建一个 identity Manager。
func New(repo data.Repository) *Manager {
	return &Manager{repo: repo}
}

// LoginOrCreateUser 按用户名查找用户，不存在则创建（默认 owner 角色）。
// 返回 user 对象。每次调用都更新 last_active_at。
func (m *Manager) LoginOrCreateUser(ctx context.Context, name string) (data.User, error) {
	// 先查
	user, err := m.repo.GetUserByName(ctx, name)
	if err == nil {
		// 找到了，更新活跃时间
		if err := m.repo.UpdateUserActive(ctx, user.ID); err != nil {
			return data.User{}, fmt.Errorf("update user %q activity: %w", name, err)
		}
		logger.Debug("用户已存在，已更新活跃时间",
			zap.String("user", name),
			zap.Int64("user_id", user.ID),
		)
		return user, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return data.User{}, fmt.Errorf("find user %q: %w", name, err)
	}

	// 查不到 → 创建（sql.ErrNoRows 会被 data 层包成 fmt.Errorf）
	user, err = m.repo.CreateUser(ctx, name, "owner")
	if err != nil {
		return data.User{}, fmt.Errorf("create user %q: %w", name, err)
	}
	logger.Info("新用户已创建",
		zap.String("user", name),
		zap.Int64("user_id", user.ID),
	)
	return user, nil
}

// NewSession 为用户创建一个新会话，返回 session ID。
func (m *Manager) NewSession(ctx context.Context, userID int64) (string, error) {
	sessionID := uuid.NewString()
	_, err := m.repo.CreateSession(ctx, sessionID, userID)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return sessionID, nil
}

// GetSession 查会话信息（含 user_id）。
func (m *Manager) GetSession(ctx context.Context, sessionID string) (data.Session, error) {
	return m.repo.GetSession(ctx, sessionID)
}

// TouchSession 更新会话活跃时间。
// 这是一个 fire-and-forget 操作：更新失败不影响主流程，但会打日志。
func (m *Manager) TouchSession(ctx context.Context, sessionID string) {
	if err := m.repo.UpdateSessionActive(ctx, sessionID); err != nil {
		logger.Warn("更新会话活跃时间失败",
			zap.Error(err),
			zap.String("session_id", sessionID),
		)
	}
}
