// injector.go 放提醒注入器: 把待办提醒格式化成 system prompt 文本块.
//
// 做的事情:
//  1. InjectReminders: 查用户的 pending 提醒, 格式化成 "[待办提醒]" 文本块注入 system prompt.
//  2. 降级模式: 查询失败时返回空字符串, 不影响正常对话.
//  3. 限制条数: 超过 maxReminders 的不注入, 只保留最近的.
//
// 方案 16.12.6 节: 定时扫描到期提醒注入 system prompt.
// 投递时机: 后台调度器把到期提醒标记为 delivered 后,
// 下一次用户发起聊天请求时, InjectReminders 把 pending 提醒注入 system prompt.
package reminder

import (
	"context"
	"strings"

	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// InjectReminders 查用户已到期但未确认的提醒, 格式化成 system prompt 文本块.
// 调度器到期后把提醒标记为 delivered, 这里查 delivered 且未 acknowledged 的提醒,
// 注入 system prompt 让 LLM 在回复时提醒用户, 用户确认(acknowledge)后停止注入.
//
// 参数:
//   - ctx: 上下文
//   - userID: 哪个用户的提醒
//   - store: 提醒存储, 用来查 delivered 提醒
//   - maxReminders: 最多注入几条提醒, 超过的不管
//
// 返回值:
//   - string: 格式化后的提醒文本块, 没有就返回空字符串
//
// 输出示例:
//
//	[待办提醒]
//	以下事项已到期, 请在回复中自然地提醒用户:
//	- 08-22 15:00: 买牛奶
//	- 08-23 09:00: 提交周报
func InjectReminders(ctx context.Context, userID int64, store *Store, maxReminders int) string {
	// 参数防御: maxReminders 小于等于 0 时直接返回空, 防止限制为 0 还去查库
	if maxReminders <= 0 {
		return ""
	}

	// 查用户的 delivered 提醒 (到期已投递, 但用户还没确认)
	// 调度器到期后把 pending → delivered, 这里查 delivered 注入给 LLM
	// 用户 acknowledge 后状态变成 acknowledged, 不再注入
	reminders, err := store.ListReminders(ctx, userID, data.ReminderStatusDelivered)
	if err != nil {
		// 查询失败, 降级模式: 打 Error 日志, 返回空字符串, 不阻断对话
		logger.Error("inject reminders: list delivered reminders failed",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		return ""
	}

	// 没有已投递的提醒, 返回空字符串
	if len(reminders) == 0 {
		return ""
	}

	// 限制注入条数, 只取前 maxReminders 条
	// 如果提醒数量不超过限制, limit 就等于总条数, 不截断
	limit := maxReminders
	if len(reminders) < limit {
		limit = len(reminders)
	}

	// 用 strings.Builder 高效拼接字符串
	// strings.Builder 是 Go 标准库提供的可变字符串构造器, 比直接用 += 拼接快
	var b strings.Builder

	// 写入标题行和引导语, 告诉 LLM 这些提醒已到期, 需要自然地提醒用户
	b.WriteString("[待办提醒]\n")
	b.WriteString("以下事项已到期, 请在回复中自然地提醒用户:\n")

	// 遍历提醒, 每条写成一行: "- 时间: 内容"
	for i := 0; i < limit; i++ {
		r := reminders[i]
		// 把提醒时间格式化成易读的字符串
		// time.Format 是 Go 标准库的时间格式化函数, 用参考时间 "2006-01-02 15:04:05" 指定格式
		// 这里用 "01-02 15:04" 格式, 省略年份, 比如 "08-22 15:00"
		when := r.RemindAt.Format("01-02 15:04")
		// 写入一行: "- 08-22 15:00: 买牛奶"
		b.WriteString("- ")
		b.WriteString(when)
		b.WriteString(": ")
		b.WriteString(r.Content)
		b.WriteString("\n")
	}

	return b.String()
}
