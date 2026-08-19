// session.go 放 SessionService: 用户登录/会话创建业务逻辑.
//
// 做的事情:
//  1. 接收 handler 传来的用户名(为空时默认 "owner").
//  2. 调 identity.Manager.LoginOrCreateUser: 按用户名查库, 有则更新活跃时间, 没有则新建.
//  3. 调 identity.Manager.NewSession: 给用户创建一条新会话, 返回 UUID.
//  4. 返回 CreateSessionResult 给 handler 序列化成 JSON 响应.
package service

import (
	"context"
	"fmt"

	"github.com/swallow-sun/swallow-go/internal/telemetry"
	"github.com/swallow-sun/swallow-go/internal/trace"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// NewSessionService 创建一个 SessionService.
func NewSessionService(deps *Deps) *SessionService {
	return &SessionService{deps: deps}
}

// CreateSession 处理用户登录 + 会话创建.
// userName 为空时默认 "owner"(私人助手默认主人).
// 返回 createSessionResult, handler 直接转成 JSON 响应.
func (s *SessionService) CreateSession(ctx context.Context, userName string) (CreateSessionResult, error) {
	// 客户端没传用户名(或者传了空字符串), 给一个默认值 "owner".
	// 这是你的私人助手, 默认就是主人本人
	if userName == "" {
		userName = "owner"
	}

	// 调身份管理器: 拿用户名去数据库查, 有就更新活跃时间返回, 没有就新建一条返回.
	// s.deps.idm 是 identity.Manager 的实例, 负责用户和会话的身份数据管理
	user, err := s.deps.idm.LoginOrCreateUser(ctx, userName)
	if err != nil {
		// 底层 fmt.Errorf 往上抛, 入口层(handler)统一打日志
		return CreateSessionResult{}, fmt.Errorf("failed to init user: %w", err)
	}

	// 给这个用户创建一条新会话.
	// NewSession 往 sessions 表插一条记录, 返回 UUID 格式的会话 ID
	sessionID, err := s.deps.idm.NewSession(ctx, user.ID)
	if err != nil {
		return CreateSessionResult{}, fmt.Errorf("failed to create session: %w", err)
	}

	// 成功了打一条 Info 日志
	logger.Info("Session created",
		zap.String("user", user.Name),
		zap.String("session_id", sessionID),
	)

	// 发埋点: 记录会话创建事件.
	// 方案 16.10.1 节要求的 6 种事件之一, 在会话创建成功后发出.
	// trace.Ensure 检查 context 里有没有 trace ID, 没有就生成一个塞进去
	// 返回的 ctx 带上了 trace ID, telemetry.Emit 从 ctx 里取出来带上
	ctx, _ = trace.Ensure(ctx)
	telemetry.Emit(ctx,
		telemetry.EventSessionCreated,
		map[string]any{
			"session_id":          sessionID,
			"user_id":             user.ID,
			"user_name":           user.Name,
			telemetry.FieldStatus: telemetry.StatusOK,
		},
	)

	return CreateSessionResult{
		SessionID: sessionID, // 新创建的会话 ID(UUID)
		UserName:  user.Name, // 用户名
		UserID:    user.ID,   // 用户在数据库里的自增 ID
	}, nil
}
