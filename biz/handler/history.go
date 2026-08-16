package handler

// history.go 放 GET /api/history 接口的 handler。
// 客户端调这个接口，传一个 session_id，查这个会话最近 50 条对话记录。

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// GetHistory GET /api/history?session_id=xxx
// 客户端在 URL 里传 session_id，返回这个会话最近 50 条对话记录。
func (d *Deps) GetHistory(ctx context.Context, c *app.RequestContext) {
	// 从 URL 查询参数里取 session_id，比如 /api/history?session_id=abc-123
	// c.Query 返回的是 []byte，用 string() 转成 Go 字符串
	sessionID := string(c.Query("session_id"))

	// 没传 session_id，返回 400，告诉客户端这个参数是必填的
	if sessionID == "" {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "session_id is required"})
		return
	}

	// 从数据库查这个会话最近 50 条对话记录。
	// dialogues 是一个切片，里面每个元素是一条对话记录（包含角色、内容、时间）
	dialogues, err := d.repo.GetRecentDialogues(ctx, sessionID, 50)

	// 数据库查询出错
	if err != nil {
		// 打日志，记录是哪个会话查失败了
		logger.Error("查询历史失败", zap.String("session_id", sessionID), zap.Error(err))
		// 返回 500 + 笼统信息
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	// 把数据库结构体转成给客户端看的 JSON 结构体。
	// make 预分配切片大小，防止频繁扩容，和 telemetry.go 里 consume 函数的做法一样
	items := make([]historyItem, 0, len(dialogues))

	// 遍历每条对话记录，转成 historyItem
	for _, dialogue := range dialogues {
		items = append(items, historyItem{
			Role:    dialogue.Role,    // 角色："user" 或 "assistant"
			Content: dialogue.Content, // 消息内容
			// 把时间格式化成可读字符串，Go 的时间格式化很特殊：
			// 不是用 "YYYY-MM-DD" 而是 "2006-01-02 15:04:05"（Go 的诞生时间做模板）
			Timestamp: dialogue.Timestamp.Format("2006-01-02 15:04:05"),
		})
	}

	// 返回 HTTP 200 + JSON 响应体
	c.JSON(consts.StatusOK, historyResp{
		SessionID: sessionID, // 哪个会话的历史
		Items:     items,     // 对话记录列表
	})
}
