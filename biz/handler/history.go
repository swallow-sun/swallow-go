// history.go 放 GET /api/history 接口的 handler。
//
// 做的事情：
//  1. 从 URL 查询参数取 session_id。
//  2. 调 HistoryService.GetHistory 查询这个会话最近 50 条对话记录。
//  3. 把 service 返回的结果转成 JSON 响应发给客户端。
package handler

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

	// 调 HistoryService 查询对话历史
	result, err := d.history.GetHistory(ctx, sessionID)
	if err != nil {
		// 打日志，记录是哪个会话查失败了
		logger.Error("查询历史失败", zap.String("session_id", sessionID), zap.Error(err))
		// 返回 500 + 笼统信息
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	// 把 service 返回的 HistoryItem 转成 handler 的 historyItem。
	// make 先分配好切片容量，免得 append 时频繁扩容
	items := make([]historyItem, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, historyItem{
			Role:      item.Role,
			Content:   item.Content,
			Timestamp: item.Timestamp,
		})
	}

	// 返回 HTTP 200 + JSON 响应体
	c.JSON(consts.StatusOK, historyResp{
		SessionID: result.SessionID,
		Items:     items,
	})
}
