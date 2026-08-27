// reminder.go 放待办提醒相关接口的 handler.
//
// 做的事情:
//  1. CreateReminder: POST /api/v1/reminders, 手动创建一条提醒.
//  2. ListReminders: GET /api/v1/reminders, 按状态查提醒列表.
//  3. GetReminder: GET /api/v1/reminders/:id, 查单条提醒.
//  4. UpdateReminder: PATCH /api/v1/reminders/:id, 更新提醒.
//  5. AcknowledgeReminder: POST /api/v1/reminders/:id/ack, 确认提醒已收到.
//  6. CancelReminder: POST /api/v1/reminders/:id/cancel, 取消提醒.
//
// 方案 16.12.6 节的 API.
// handler 只做 HTTP 解析和 JSON 序列化, 业务逻辑在 service 层.
// 所有接口需要 owner 令牌认证, 复用 authorizeOwner.
package handler

import (
	"context"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/swallow-sun/swallow-go/internal/apperror"
	"github.com/swallow-sun/swallow-go/internal/trace"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// CreateReminder POST /api/v1/reminders
// 手动创建一条待办提醒.
// 需要在 URL query 里带 user_id, 在请求体里带提醒内容和提醒时间.
func (d *Deps) CreateReminder(ctx context.Context, c *app.RequestContext) {
	if !d.authorizeOwner(c) {
		return
	}
	ctx, _ = trace.Ensure(ctx)
	userID, ok := parseUserID(c)
	if !ok {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeMissingUserID, "valid user_id is required", ""))
		return
	}

	var req createReminderReq
	if err := c.BindAndValidate(&req); err != nil {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeInvalidRequestBody, "invalid request body", ""))
		return
	}

	if req.Content == "" || req.RemindAt == "" {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeMissingRequiredFields, "content and remind_at are required", ""))
		return
	}

	// 解析 RFC3339 时间
	remindAt, err := time.Parse(time.RFC3339, req.RemindAt)
	if err != nil {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeInvalidRequestBody, "remind_at must be RFC3339 format", ""))
		return
	}

	result, err := d.reminder.CreateReminder(ctx, userID, req.Content, remindAt)
	if err != nil {
		logger.Error("create reminder failed", zap.Int64("user_id", userID), zap.Error(err))
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}

	c.JSON(consts.StatusOK, result)
}

// ListReminders GET /api/v1/reminders?user_id=1&status=pending
// 按用户 ID 和状态查提醒列表.
// status 为空时查所有状态.
func (d *Deps) ListReminders(ctx context.Context, c *app.RequestContext) {
	if !d.authorizeOwner(c) {
		return
	}
	ctx, _ = trace.Ensure(ctx)
	userID, ok := parseUserID(c)
	if !ok {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeMissingUserID, "valid user_id is required", ""))
		return
	}

	// status 可选, 不传时查所有状态
	status := string(c.Query("status"))

	result, err := d.reminder.ListReminders(ctx, userID, status)
	if err != nil {
		logger.Error("list reminders failed", zap.Int64("user_id", userID), zap.Error(err))
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}

	c.JSON(consts.StatusOK, result)
}

// GetReminder GET /api/v1/reminders/:id
// 查单条提醒.
func (d *Deps) GetReminder(ctx context.Context, c *app.RequestContext) {
	if !d.authorizeOwner(c) {
		return
	}
	ctx, _ = trace.Ensure(ctx)

	reminderID, ok := parsePathID(c, "id")
	if !ok {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeInvalidReminderID, "valid reminder id is required", ""))
		return
	}

	result, err := d.reminder.GetReminder(ctx, reminderID)
	if err != nil {
		logger.Error("get reminder failed", zap.Int64("reminder_id", reminderID), zap.Error(err))
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}

	c.JSON(consts.StatusOK, result)
}

// UpdateReminder PATCH /api/v1/reminders/:id
// 更新提醒, 可以改状态、内容或提醒时间.
func (d *Deps) UpdateReminder(ctx context.Context, c *app.RequestContext) {
	if !d.authorizeOwner(c) {
		return
	}
	ctx, _ = trace.Ensure(ctx)

	reminderID, ok := parsePathID(c, "id")
	if !ok {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeInvalidReminderID, "valid reminder id is required", ""))
		return
	}

	var req updateReminderReq
	if err := c.BindAndValidate(&req); err != nil {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeInvalidRequestBody, "invalid request body", ""))
		return
	}

	var remindAt time.Time
	if req.RemindAt != "" {
		t, err := time.Parse(time.RFC3339, req.RemindAt)
		if err != nil {
			writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeInvalidRequestBody, "remind_at must be RFC3339 format", ""))
			return
		}
		remindAt = t
	}

	if err := d.reminder.UpdateReminder(ctx, reminderID, req.Status, req.Content, remindAt); err != nil {
		logger.Error("update reminder failed", zap.Int64("reminder_id", reminderID), zap.Error(err))
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}

	c.JSON(consts.StatusOK, map[string]string{"status": "updated"})
}

// AcknowledgeReminder POST /api/v1/reminders/:id/ack
// 确认提醒已收到.
func (d *Deps) AcknowledgeReminder(ctx context.Context, c *app.RequestContext) {
	if !d.authorizeOwner(c) {
		return
	}
	ctx, _ = trace.Ensure(ctx)

	reminderID, ok := parsePathID(c, "id")
	if !ok {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeInvalidReminderID, "valid reminder id is required", ""))
		return
	}

	if err := d.reminder.AcknowledgeReminder(ctx, reminderID); err != nil {
		logger.Error("acknowledge reminder failed", zap.Int64("reminder_id", reminderID), zap.Error(err))
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}

	c.JSON(consts.StatusOK, map[string]string{"status": "acknowledged"})
}

// CancelReminder POST /api/v1/reminders/:id/cancel
// 取消提醒.
func (d *Deps) CancelReminder(ctx context.Context, c *app.RequestContext) {
	if !d.authorizeOwner(c) {
		return
	}
	ctx, _ = trace.Ensure(ctx)

	reminderID, ok := parsePathID(c, "id")
	if !ok {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(apperror.CodeInvalidReminderID, "valid reminder id is required", ""))
		return
	}

	if err := d.reminder.CancelReminder(ctx, reminderID); err != nil {
		logger.Error("cancel reminder failed", zap.Int64("reminder_id", reminderID), zap.Error(err))
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}

	c.JSON(consts.StatusOK, map[string]string{"status": "cancelled"})
}
