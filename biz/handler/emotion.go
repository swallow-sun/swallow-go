// emotion.go 放情绪持续段相关接口的 handler.
//
// 做的事情:
//  1. ListEmotionSessions: GET /api/v1/emotion-sessions, 查情绪持续段列表.
//  2. GetLatestEmotionSession: GET /api/v1/emotion-sessions/latest, 查最近一条情绪段.
//
// 方案 16.12.6 节的 API.
// handler 只做 HTTP 解析和 JSON 序列化, 业务逻辑在 service 层.
// 所有接口需要 owner 令牌认证, 复用 authorizeOwner.
package handler

import (
	"context"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/swallow-sun/swallow-go/internal/apperror"
	"github.com/swallow-sun/swallow-go/internal/trace"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// ListEmotionSessions GET /api/v1/emotion-sessions?user_id=1&limit=20
// 查情绪持续段列表, limit 可选, 默认由 store 层决定.
func (d *Deps) ListEmotionSessions(ctx context.Context, c *app.RequestContext) {
	if !d.authorizeOwner(c) {
		return
	}
	ctx, _ = trace.Ensure(ctx)
	userID, ok := parseUserID(c)
	if !ok {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeMissingUserID, "valid user_id is required", ""))
		return
	}

	// limit 可选, 不传时传 0, 由 store 层用默认值
	limit := 0
	if raw := string(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeInvalidRequestBody, "invalid limit", ""))
			return
		}
		limit = n
	}

	result, err := d.emotion.ListEmotionSessions(ctx, userID, limit)
	if err != nil {
		logger.Error("list emotion sessions failed", zap.Int64("user_id", userID), zap.Error(err))
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}

	c.JSON(consts.StatusOK, result)
}

// GetLatestEmotionSession GET /api/v1/emotion-sessions/latest?user_id=1
// 查最近一条情绪持续段.
func (d *Deps) GetLatestEmotionSession(ctx context.Context, c *app.RequestContext) {
	if !d.authorizeOwner(c) {
		return
	}
	ctx, _ = trace.Ensure(ctx)
	userID, ok := parseUserID(c)
	if !ok {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeMissingUserID, "valid user_id is required", ""))
		return
	}

	result, err := d.emotion.GetLatestEmotionSession(ctx, userID)
	if err != nil {
		logger.Error("get latest emotion session failed", zap.Int64("user_id", userID), zap.Error(err))
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}

	c.JSON(consts.StatusOK, result)
}
