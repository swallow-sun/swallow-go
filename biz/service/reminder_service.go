// reminder_service.go 放待办提醒相关的业务编排层.
//
// 做的事情:
//  1. 定义 NewReminderService 工厂函数.
//  2. CreateReminder: 手动创建一条提醒.
//  3. ListReminders: 按状态查提醒列表.
//  4. GetReminder: 查单条提醒.
//  5. UpdateReminder: 更新提醒状态/内容/时间.
//  6. AcknowledgeReminder: 确认提醒已收到.
//  7. CancelReminder: 取消提醒.
//  8. StartScheduler: 启动后台提醒调度器.
//
// 方案 16.12.6 节的 API:
//   POST   /api/v1/reminders          → CreateReminder
//   GET    /api/v1/reminders          → ListReminders
//   GET    /api/v1/reminders/:id      → GetReminder
//   PATCH  /api/v1/reminders/:id      → UpdateReminder
//   POST   /api/v1/reminders/:id/ack  → AcknowledgeReminder
//   POST   /api/v1/reminders/:id/cancel → CancelReminder
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/reminder"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// NewReminderService 创建一个 ReminderService.
func NewReminderService(deps *Deps) *ReminderService {
	return &ReminderService{store: deps.reminderStore, repo: deps.repo}
}

// CreateReminder 手动创建一条待办提醒.
// 方案 16.12.6 节: POST /api/v1/reminders.
//
// 参数:
//   - ctx: 上下文
//   - userID: 哪个用户的提醒
//   - content: 提醒内容
//   - remindAt: 提醒触发时间
//
// 返回值:
//   - CreateReminderResult: 创建结果
//   - error: 创建失败时返回错误
func (s *ReminderService) CreateReminder(ctx context.Context, userID int64, content string, remindAt time.Time) (CreateReminderResult, error) {
	reminder, err := s.store.CreateReminder(
		ctx, userID, "", "", content, remindAt, data.ReminderSourceManual,
	)
	if err != nil {
		return CreateReminderResult{}, fmt.Errorf("create reminder: %w", err)
	}
	return CreateReminderResult{Reminder: reminder}, nil
}

// ListReminders 按状态查提醒列表.
// 方案 16.12.6 节: GET /api/v1/reminders.
//
// 参数:
//   - ctx: 上下文
//   - userID: 哪个用户的提醒
//   - status: 按状态过滤, 空串表示不过滤
//
// 返回值:
//   - ListRemindersResult: 提醒列表结果
//   - error: 查询失败时返回错误
func (s *ReminderService) ListReminders(ctx context.Context, userID int64, status string) (ListRemindersResult, error) {
	reminders, err := s.store.ListReminders(ctx, userID, status)
	if err != nil {
		return ListRemindersResult{}, fmt.Errorf("list reminders: %w", err)
	}
	return ListRemindersResult{Items: reminders}, nil
}

// GetReminder 查单条提醒.
// 方案 16.12.6 节: GET /api/v1/reminders/:id.
//
// 参数:
//   - ctx: 上下文
//   - id: 提醒 ID
//
// 返回值:
//   - ReminderResult: 单条提醒结果
//   - error: 查询失败时返回错误
func (s *ReminderService) GetReminder(ctx context.Context, id int64) (ReminderResult, error) {
	r, err := s.store.GetReminder(ctx, id)
	if err != nil {
		return ReminderResult{}, fmt.Errorf("get reminder: %w", err)
	}
	return ReminderResult{Reminder: r}, nil
}

// UpdateReminder 更新提醒.
// 可以改状态, 内容, 或提醒时间.
// 方案 16.12.6 节: PATCH /api/v1/reminders/:id.
//
// 参数:
//   - ctx: 上下文
//   - id: 提醒 ID
//   - status: 新状态, 空串表示不改
//   - content: 新内容, 空串表示不改
//   - remindAt: 新提醒时间, 零值表示不改
//
// 返回值:
//   - error: 更新失败时返回错误
func (s *ReminderService) UpdateReminder(ctx context.Context, id int64, status, content string, remindAt time.Time) error {
	fields := map[string]any{}
	if status != "" {
		fields["status"] = status
	}
	if content != "" {
		fields["content"] = content
	}
	if !remindAt.IsZero() {
		fields["remind_at"] = remindAt
	}
	if len(fields) == 0 {
		return nil
	}

	// 如果改了状态, 需要自动设置对应时间字段
	now := time.Now()
	switch status {
	case data.ReminderStatusDelivered:
		fields["delivered_at"] = now
	case data.ReminderStatusAcknowledged:
		fields["acknowledged_at"] = now
	}

	if err := s.repo.UpdateReminder(ctx, id, fields); err != nil {
		return fmt.Errorf("update reminder: %w", err)
	}
	return nil
}

// AcknowledgeReminder 确认提醒已收到.
// 方案 16.12.6 节: POST /api/v1/reminders/:id/ack.
//
// 参数:
//   - ctx: 上下文
//   - id: 提醒 ID
//
// 返回值:
//   - error: 更新失败时返回错误
func (s *ReminderService) AcknowledgeReminder(ctx context.Context, id int64) error {
	if err := s.store.UpdateStatus(ctx, id, data.ReminderStatusAcknowledged); err != nil {
		return fmt.Errorf("acknowledge reminder: %w", err)
	}
	return nil
}

// CancelReminder 取消提醒.
// 方案 16.12.6 节: POST /api/v1/reminders/:id/cancel.
//
// 参数:
//   - ctx: 上下文
//   - id: 提醒 ID
//
// 返回值:
//   - error: 更新失败时返回错误
func (s *ReminderService) CancelReminder(ctx context.Context, id int64) error {
	if err := s.store.UpdateStatus(ctx, id, data.ReminderStatusCancelled); err != nil {
		return fmt.Errorf("cancel reminder: %w", err)
	}
	return nil
}

// StartScheduler 启动后台提醒调度器.
// 在 main.go 启动时调一次, 后台 goroutine 定时扫描到期提醒.
//
// 参数:
//   - ctx: 上下文, 用于控制 goroutine 生命周期
func (s *ReminderService) StartScheduler(ctx context.Context, scanIntervalSeconds int) {
	reminder.StartScheduler(ctx, s.store, scanIntervalSeconds)
	logger.Info("reminder scheduler started from service layer",
		zap.Int("interval_seconds", scanIntervalSeconds),
	)
}
