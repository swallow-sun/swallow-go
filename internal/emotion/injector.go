// injector.go 放情绪注入器: 把用户的情绪持续段格式化成 system prompt 文本块.
//
// 做的事情:
//  1. InjectEmotion: 查用户最近的情绪持续段, 格式化成 "[用户情绪状态参考]" 文本块注入 system prompt.
//  2. 降级模式: 查询失败时返回空字符串, 不影响正常对话.
//  3. 限制条数: 超过 maxSessions 的旧段不注入, 只保留最近的.
//
// 方案 16.12.6 节: 情绪持续段作为 system prompt 的参考信息注入,
// 让模型在回复时考虑用户当前的情绪状态, 比如用户很挫败时可以多一些鼓励.
// 和 reminder 包的 InjectReminders 是同一套注入模式.
package emotion

import (
	"context"
	"fmt"
	"strings"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// InjectEmotion 查用户最近的情绪持续段, 格式化成 system prompt 文本块.
// 如果没有情绪段或查询失败, 返回空字符串(降级模式, 不影响正常对话).
//
// 参数:
//   - ctx: 上下文
//   - userID: 哪个用户的情绪
//   - store: 情绪存储, 用来查最近的情绪持续段
//   - maxSessions: 最多注入几条情绪段, 超过的旧段不管
//
// 返回值:
//   - string: 格式化后的情绪参考文本块, 没有就返回空字符串
//
// 输出示例:
//
//	[用户情绪状态参考]
//	最近情绪: frustrated (强度 0.6, 第3-7轮, 持续5.2分钟)
//	之前情绪: neutral (第1-2轮, 持续1.8分钟)
func InjectEmotion(ctx context.Context, userID int64, store *Store, maxSessions int) string {
	// 参数防御: maxSessions 小于等于 0 时直接返回空, 防止限制为 0 还去查库
	if maxSessions <= 0 {
		return ""
	}

	// 查用户最近的情绪持续段, 按 start_at 倒序排列
	sessions, err := store.GetRecentSessions(ctx, userID, maxSessions)
	if err != nil {
		// 查询失败, 降级模式: 打 Error 日志, 返回空字符串, 不阻断对话
		logger.Error("inject emotion: get recent sessions failed",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		return ""
	}

	// 没有情绪段, 返回空字符串
	if len(sessions) == 0 {
		return ""
	}

	// 用 strings.Builder 高效拼接字符串
	// strings.Builder 是 Go 标准库提供的可变字符串构造器, 比直接用 += 拼接快
	var b strings.Builder

	// 写入标题行
	b.WriteString("[用户情绪状态参考]\n")

	// 遍历情绪段, 每段写一行
	// 第一段是最近的情绪, 用 "最近情绪" 标记; 后面的用 "之前情绪" 标记
	for i, sess := range sessions {
		if i == 0 {
			b.WriteString("最近情绪: ")
		} else {
			b.WriteString("之前情绪: ")
		}

		// 写入情绪标签, 比如 "frustrated"
		b.WriteString(sess.Emotion)

		// 括号里写详细信息: 强度, 轮次范围, 持续时长
		b.WriteString(" (")

		// 第一段带上强度信息, 后面的段省略(太长不利于阅读)
		if i == 0 {
			b.WriteString(fmt.Sprintf("强度 %.1f, ", sess.Intensity))
		}

		// 写轮次范围: "第3-7轮" 或 "第3轮"(只有一轮时)
		b.WriteString(formatRoundRange(sess.StartRound, sess.EndRound))

		// 写持续时长, 只有结束的段才有 DurationMinutes
		if sess.DurationMinutes != nil {
			b.WriteString(", ")
			b.WriteString(fmt.Sprintf("持续%.1f分钟", *sess.DurationMinutes))
		}

		b.WriteString(")\n")
	}

	return b.String()
}

// formatRoundRange 把轮次范围格式化成字符串.
// 开始轮和结束轮相同(比如都是 3)时输出 "第3轮",
// 不同时输出 "第3-7轮".
//
// 参数:
//   - startRound: 情绪段的开始轮
//   - endRound: 情绪段的结束轮, 可能为 nil(进行中的段还没结束)
//
// 返回值:
//   - string: 格式化后的轮次范围字符串
func formatRoundRange(startRound int, endRound *int) string {
	if endRound == nil {
		// 进行中的段, 结束轮还没设, 只显示开始轮
		return fmt.Sprintf("第%d轮起", startRound)
	}
	if startRound == *endRound {
		// 开始轮和结束轮相同, 只有一轮
		return fmt.Sprintf("第%d轮", startRound)
	}
	// 开始轮和结束轮不同, 显示范围
	return fmt.Sprintf("第%d-%d轮", startRound, *endRound)
}
