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
// 把 data.Repository 传进来，Manager 靠它来读写数据库。
func New(repo data.Repository) *Manager {
	// 构造一个 Manager，把 repo 存进去，后面所有方法都用它操作数据库
	return &Manager{repo: repo}
}

// LoginOrCreateUser 按用户名查找用户，不存在则创建（默认 owner 角色）。
// 返回 user 对象。每次调用都更新 last_active_at。
// ctx 传进来用于超时取消，name 是用户名。
func (m *Manager) LoginOrCreateUser(ctx context.Context, name string) (data.User, error) {
	// 先按用户名去数据库里查一下
	user, err := m.repo.GetUserByName(ctx, name)
	if err == nil {
		// 查到了，说明用户已存在
		// 更新这个用户的活跃时间 last_active_at，方便统计最近活跃用户
		if err := m.repo.UpdateUserActive(ctx, user.ID); err != nil {
			// 更新失败，把原始错误包一层往上抛
			// %w 是 fmt 的动词，意思是把 err 嵌进来，外面用 errors.Is/errors.As 还能匹配到原始错误
			return data.User{}, fmt.Errorf("update user %q activity: %w", name, err)
		}
		// 更新成功，打 Debug 日志
		logger.Debug("用户已存在，已更新活跃时间",
			zap.String("user", name),
			zap.Int64("user_id", user.ID),
		)
		// 返回查到的用户
		return user, nil
	}

	// 查没查到有两种情况：
	//   1. 数据库里真没这个用户 → sql.ErrNoRows（database/sql 标准包的错误，表示查询"一行都没找到"）
	//   2. 数据库本身出错了（连接断了、SQL 语法错误等）→ 其他错误
	// errors.Is(err, sql.ErrNoRows) 判断 err 这条错误链里有没有 sql.ErrNoRows
	//   errors.Is 会沿着 fmt.Errorf("%w") 包装的链条一层一层往下找
	//   如果不是"没找到"，说明是真正的数据库错误，直接返回
	if !errors.Is(err, sql.ErrNoRows) {
		return data.User{}, fmt.Errorf("find user %q: %w", name, err)
	}

	// 走到这里说明 err 是 sql.ErrNoRows，数据库里没有这个用户，需要新建
	// 调 repo 创建用户，角色固定为 "owner"（项目负责人）
	user, err = m.repo.CreateUser(ctx, name, "owner")
	if err != nil {
		// 创建失败，包一层错误往上抛
		return data.User{}, fmt.Errorf("create user %q: %w", name, err)
	}
	// 创建成功，打 Info 日志
	logger.Info("新用户已创建",
		zap.String("user", name),
		zap.Int64("user_id", user.ID),
	)
	// 返回新创建的用户
	return user, nil
}

// NewSession 为用户创建一个新会话，返回 session ID。
// userID 是用户 ID，返回的 session ID 是一个 UUID 字符串。
func (m *Manager) NewSession(ctx context.Context, userID int64) (string, error) {
	// uuid.NewString() 生成一个随机 UUID 字符串，形如 "550e8400-e29b-41d4-a716-446655440000"
	// 36 个字符，带横杠，每次调用结果不同
	// 这是 google/uuid 这个第三方库提供的函数
	sessionID := uuid.NewString()
	// 把 session ID 和用户 ID 存进数据库的 sessions 表
	_, err := m.repo.CreateSession(ctx, sessionID, userID)
	if err != nil {
		// 创建失败，包一层错误往上抛
		return "", fmt.Errorf("create session: %w", err)
	}
	// 创建成功，返回 session ID 给调用的人
	return sessionID, nil
}

// GetSession 查会话信息（含 user_id）。
// sessionID 是会话 UUID，返回 data.Session 结构体（里面有 user_id 等字段）。
func (m *Manager) GetSession(ctx context.Context, sessionID string) (data.Session, error) {
	// 直接调 repo 去数据库里按 session ID 查一条会话记录
	return m.repo.GetSession(ctx, sessionID)
}

// TouchSession 更新会话活跃时间。
// 这是一个 fire-and-forget 操作（打了就不管）：
// 更新失败不影响主流程，但会打 Warn 日志。
// 用途：每次对话或心跳时调用，让 sessions 表的 active_at 字段保持最新。
func (m *Manager) TouchSession(ctx context.Context, sessionID string) {
	// 调 repo 更新 sessions 表里这条会话的活跃时间
	if err := m.repo.UpdateSessionActive(ctx, sessionID); err != nil {
		// 更新失败了，不往上抛错（不影响主流程），只打 Warn 日志
		// zap.Error(err) 把 err 打成结构化字段，zap.String 把 sessionID 也打上方便排查
		logger.Warn("更新会话活跃时间失败",
			zap.Error(err),
			zap.String("session_id", sessionID),
		)
	}
}
