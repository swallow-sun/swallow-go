// profile.go 放用户画像和对话标签相关接口的 handler.
//
// 做的事情:
//  1. GetProfile: GET /api/v1/profiles, 查用户画像.
//  2. ListTags: GET /api/v1/tags, 查对话标签列表.
//  3. ListTagStatistics: GET /api/v1/tag-statistics, 查标签统计.
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

// GetProfile GET /api/v1/profiles?user_id=1
// 查用户画像, 返回画像 JSON、已分析轮数和分析次数.
func (d *Deps) GetProfile(ctx context.Context, c *app.RequestContext) {
	if !d.authorizeOwner(c) {
		return
	}
	ctx, _ = trace.Ensure(ctx)
	userID, ok := parseUserID(c)
	if !ok {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeMissingUserID, "valid user_id is required", ""))
		return
	}

	result, err := d.profile.GetProfile(ctx, userID)
	if err != nil {
		logger.Error("get profile failed", zap.Int64("user_id", userID), zap.Error(err))
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}

	c.JSON(consts.StatusOK, result)
}

// ListTags GET /api/v1/tags?user_id=1&limit=100
// 查对话标签列表, limit 可选, 默认由 repo 层决定.
func (d *Deps) ListTags(ctx context.Context, c *app.RequestContext) {
	if !d.authorizeOwner(c) {
		return
	}
	ctx, _ = trace.Ensure(ctx)
	userID, ok := parseUserID(c)
	if !ok {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeMissingUserID, "valid user_id is required", ""))
		return
	}

	// limit 可选, 不传时传 0, 由 repo 层用默认值
	limit := 0
	if raw := string(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeInvalidRequestBody, "invalid limit", ""))
			return
		}
		limit = n
	}

	result, err := d.profile.ListTags(ctx, userID, limit)
	if err != nil {
		logger.Error("list tags failed", zap.Int64("user_id", userID), zap.Error(err))
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}

	c.JSON(consts.StatusOK, result)
}

// ListTagStatistics GET /api/v1/tag-statistics?user_id=1&tag_dim=urgency&since=100
// 查标签统计, 支持按维度过滤和起始轮次过滤.
func (d *Deps) ListTagStatistics(ctx context.Context, c *app.RequestContext) {
	if !d.authorizeOwner(c) {
		return
	}
	ctx, _ = trace.Ensure(ctx)
	userID, ok := parseUserID(c)
	if !ok {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeMissingUserID, "valid user_id is required", ""))
		return
	}

	// tag_dim 可选, 空串表示不过滤
	tagDim := string(c.Query("tag_dim"))

	// since 可选, 不传时传 0, 表示不限制
	since := 0
	if raw := string(c.Query("since")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeInvalidRequestBody, "invalid since", ""))
			return
		}
		since = n
	}

	result, err := d.profile.ListTagStatistics(ctx, userID, tagDim, since)
	if err != nil {
		logger.Error("list tag statistics failed", zap.Int64("user_id", userID), zap.Error(err))
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}

	c.JSON(consts.StatusOK, result)
}
