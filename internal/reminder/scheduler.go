// scheduler.go 放后台提醒调度器: 定时扫描到期提醒并标记为已投递.
//
// 做的事情:
//  1. StartScheduler: 启动后台 goroutine, 定时扫描到期提醒.
//  2. 每个扫描周期: 调 store.GetDueReminders 查到期提醒, 逐条标记为 delivered.
//  3. 尊重 context 取消: ctx 被取消时 goroutine 自动退出.
//  4. 使用 time.Ticker 实现周期性执行.
//
// 方案 16.12.6 节: 定时扫描到期提醒注入 system prompt.
// 调度器只负责标记 delivered, 实际注入在下次聊天请求时由 InjectReminders 完成.
package reminder

import (
	"context"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// StartScheduler 启动后台提醒调度器.
// 这是一个阻塞函数, 内部启动一个 goroutine 后立即返回.
// goroutine 会每隔 intervalSeconds 秒扫描一次到期提醒.
//
// 调度器的工作流程:
//  1. 每隔 intervalSeconds 秒, 调 store.GetDueReminders 查到期提醒.
//  2. 对每条到期提醒, 调 store.MarkDelivered 标记为已投递.
//  3. 实际注入发生在下次用户聊天请求时, 由 InjectReminders 把 pending 提醒注入 system prompt.
//  4. ctx 被取消时, goroutine 自动退出.
//
// 参数:
//   - ctx: 上下文, 用于控制 goroutine 生命周期; ctx 取消时 goroutine 退出
//   - store: 提醒存储, 用来查询和更新提醒状态
//   - intervalSeconds: 扫描间隔秒数
func StartScheduler(ctx context.Context, store *Store, intervalSeconds int) {
	// 参数防御: 间隔不能小于等于 0, 否则设为默认值 60 秒
	if intervalSeconds <= 0 {
		intervalSeconds = 60
	}

	// time.Duration 是 Go 标准库里表示时间间隔的类型
	// time.Second 是 1 秒的 Duration 值
	// 乘以 intervalSeconds 得到 N 秒的 Duration
	interval := time.Duration(intervalSeconds) * time.Second

	logger.Info("reminder scheduler started",
		zap.Int("interval_seconds", intervalSeconds),
	)

	// 启动后台 goroutine, 主流程不阻塞
	go func() {
		// time.NewTicker 创建一个定时器, 每隔 interval 时间往 channel 发一个当前时间
		// ticker.C 是一个 <-chan time.Time, 收到值说明又过了一个周期
		ticker := time.NewTicker(interval)
		// defer ticker.Stop() 确保函数退出时停止定时器, 释放资源
		defer ticker.Stop()

		for {
			select {
			// ctx.Done() 在 context 被取消时触发(比如程序收到关闭信号)
			// 到了这里说明要退出了, 打日志后 return
			case <-ctx.Done():
				logger.Info("reminder scheduler stopped")
				return

			// ticker.C 每隔 interval 时间收到一个值, 触发一次扫描
			case <-ticker.C:
				scanOnce(ctx, store)
			}
		}
	}()
}

// scanOnce 执行一次到期提醒扫描.
// 查询所有到期但未投递的提醒, 逐条标记为 delivered.
// 单条标记失败不影响其他提醒, 每条独立处理.
//
// 参数:
//   - ctx: 上下文, 用于超时取消
//   - store: 提醒存储
func scanOnce(ctx context.Context, store *Store) {
	// 调 store.GetDueReminders 查到期提醒
	// time.Now() 返回当前时间, 只查 remind_at <= now 的 pending 提醒
	reminders, err := store.GetDueReminders(ctx, time.Now())
	if err != nil {
		// 查询失败打 Error 日志, 等下个周期再试
		logger.Error("reminder scheduler: scan due reminders failed",
			zap.Error(err),
		)
		return
	}

	// 没有到期提醒, 跳过
	if len(reminders) == 0 {
		return
	}

	// 逐条标记为已投递
	for _, r := range reminders {
		// 调 MarkDelivered 把提醒状态改成 delivered, 同时记录 delivered_at
		if err := store.MarkDelivered(ctx, r.ID); err != nil {
			// 单条标记失败不影响其他提醒, 打 Error 日志后继续处理下一条
			logger.Error("reminder scheduler: mark delivered failed",
				zap.Int64("reminder_id", r.ID),
				zap.Error(err),
			)
			continue
		}

		// 标记成功打 Debug 日志
		logger.Debug("reminder scheduler: reminder marked delivered",
			zap.Int64("reminder_id", r.ID),
			zap.Int64("user_id", r.UserID),
			zap.String("content", r.Content),
		)
	}
}
