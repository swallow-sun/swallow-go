// store.go 放 reminder.Store 的增删改查方法.
//
// 做的事情:
//  1. CreateReminder: 构造 data.Reminder 并调 repo.InsertReminder 写入数据库.
//  2. ListReminders: 按用户 ID 和状态查提醒列表, 空结果返回空切片.
//  3. GetReminder: 按提醒 ID 查单条.
//  4. UpdateStatus: 更新提醒状态, 根据状态自动设置 delivered_at / acknowledged_at.
//  5. MarkDelivered: UpdateStatus("delivered") 的快捷方式.
//  6. GetDueReminders: 查已到期但尚未投递的提醒, 给后台调度器用.
//
// 方案 16.12.6 节: 从对话提取 + 用户确认, 定时扫描到期提醒注入 system prompt.
package reminder

import (
	"context"
	"fmt"
	"time"

	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// CreateReminder 创建一条待办提醒.
// 构造 data.Reminder 结构体, 设置初始状态为 pending, 然后调 repo.InsertReminder 写入数据库.
//
// 参数:
//   - ctx: 上下文, 支持超时取消
//   - userID: 哪个用户的提醒
//   - sessionID: 来源会话 ID
//   - traceID: 链路追踪 ID, 方便回溯提醒来源
//   - content: 提醒内容, 比如 "买牛奶"
//   - remindAt: 提醒触发时间
//   - source: 提醒来源, dialogue(对话提取) 或 manual(手动创建)
//
// 返回值:
//   - data.Reminder: 创建成功后的提醒记录(包含数据库生成的 ID)
//   - error: 写入失败时返回错误
func (s *Store) CreateReminder(
	ctx context.Context,
	userID int64,
	sessionID, traceID, content string,
	remindAt time.Time,
	source string,
) (data.Reminder, error) {
	// 构造 data.Reminder 业务对象
	// Status 初始为 pending, CreatedAt 由数据库自动填
	reminder := data.Reminder{
		UserID:    userID,
		SessionID: sessionID,
		TraceID:   traceID,
		Content:   content,
		RemindAt:  remindAt,
		Status:    data.ReminderStatusPending,
		Source:    source,
	}

	// 调 repo 写入数据库
	created, err := s.repo.InsertReminder(ctx, reminder)
	if err != nil {
		// 写入失败打 Error 日志
		logger.Error("reminder insert failed",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		return data.Reminder{}, fmt.Errorf("create reminder: %w", err)
	}

	// 写入成功打 Debug 日志
	logger.Debug("reminder insert succeeded",
		zap.Int64("reminder_id", created.ID),
		zap.Int64("user_id", userID),
	)
	return created, nil
}

// ListReminders 按用户 ID 和状态查提醒列表.
// status 为空时查所有状态.
// 空结果返回空切片(不是 nil), 方便调用方直接遍历.
//
// 参数:
//   - ctx: 上下文
//   - userID: 哪个用户的提醒
//   - status: 按状态过滤, 空串表示不过滤
//
// 返回值:
//   - []data.Reminder: 提醒列表, 空结果返回空切片
//   - error: 查询失败时返回错误
func (s *Store) ListReminders(ctx context.Context, userID int64, status string) ([]data.Reminder, error) {
	reminders, err := s.repo.GetReminders(ctx, userID, status)
	if err != nil {
		return nil, fmt.Errorf("list reminders: %w", err)
	}

	// 空结果返回空切片, 不是 nil
	if reminders == nil {
		return []data.Reminder{}, nil
	}
	return reminders, nil
}

// GetReminder 按提醒 ID 查单条.
// 查不到时返回错误(包装了 sql.ErrNoRows).
//
// 参数:
//   - ctx: 上下文
//   - id: 提醒 ID
//
// 返回值:
//   - data.Reminder: 查到的提醒记录
//   - error: 查询失败或记录不存在时返回错误
func (s *Store) GetReminder(ctx context.Context, id int64) (data.Reminder, error) {
	reminder, err := s.repo.GetReminder(ctx, id)
	if err != nil {
		return data.Reminder{}, fmt.Errorf("get reminder: %w", err)
	}
	return reminder, nil
}

// UpdateStatus 更新提醒状态, 并根据状态自动设置投递/确认时间.
// 如果 status 是 "delivered", 自动设置 delivered_at 为当前时间.
// 如果 status 是 "acknowledged", 自动设置 acknowledged_at 为当前时间.
//
// 参数:
//   - ctx: 上下文
//   - id: 提醒 ID
//   - status: 新状态, 取值见 data.ReminderStatus* 常量
//
// 返回值:
//   - error: 更新失败时返回错误
func (s *Store) UpdateStatus(ctx context.Context, id int64, status string) error {
	// 构造要更新的字段映射, 交给 repo.UpdateReminder 执行
	// repo 层用 GORM 的 Updates 方法, 只更新 fields 里的字段
	fields := map[string]any{
		"status": status,
	}

	// 根据状态自动设置对应的时间字段
	now := time.Now()
	switch status {
	case data.ReminderStatusDelivered:
		// 投递状态: 记录投递时间
		fields["delivered_at"] = now
	case data.ReminderStatusAcknowledged:
		// 确认状态: 记录确认时间
		fields["acknowledged_at"] = now
	}

	// 调 repo 更新数据库
	if err := s.repo.UpdateReminder(ctx, id, fields); err != nil {
		// 更新失败打 Error 日志
		logger.Error("reminder status update failed",
			zap.Int64("reminder_id", id),
			zap.String("status", status),
			zap.Error(err),
		)
		return fmt.Errorf("update reminder status: %w", err)
	}

	// 更新成功打 Debug 日志
	logger.Debug("reminder status update succeeded",
		zap.Int64("reminder_id", id),
		zap.String("status", status),
	)
	return nil
}

// MarkDelivered 是 UpdateStatus("delivered") 的快捷方式.
// 把提醒标记为已投递, 自动设置 delivered_at.
// 后台调度器扫描到到期提醒后调这个方法.
//
// 参数:
//   - ctx: 上下文
//   - id: 提醒 ID
//
// 返回值:
//   - error: 更新失败时返回错误
func (s *Store) MarkDelivered(ctx context.Context, id int64) error {
	return s.UpdateStatus(ctx, id, data.ReminderStatusDelivered)
}

// GetDueReminders 查已到期但尚未投递的提醒.
// 条件: status=pending AND remind_at <= now.
// 给后台调度器用, 调度器定期调这个方法找到期提醒.
//
// 参数:
//   - ctx: 上下文
//   - now: 当前时间, 只查 remind_at <= now 的记录
//
// 返回值:
//   - []data.Reminder: 到期提醒列表, 空结果返回空切片
//   - error: 查询失败时返回错误
func (s *Store) GetDueReminders(ctx context.Context, now time.Time) ([]data.Reminder, error) {
	reminders, err := s.repo.GetPendingRemindersDue(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("get due reminders: %w", err)
	}

	// 空结果返回空切片, 不是 nil
	if reminders == nil {
		return []data.Reminder{}, nil
	}
	return reminders, nil
}
