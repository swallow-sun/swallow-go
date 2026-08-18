// history.go 放 GET /api/history 接口的 handler.
//
// 做的事情:
//  1. 从 URL 查询参数取 session_id.
//  2. 调 HistoryService.GetHistory 查询这个会话最近 50 条对话记录.
//  3. 把 service 返回的结果转成 JSON 响应发给客户端.
package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// GetHistory GET /api/history?session_id=xxx
// 客户端在 URL 里传 session_id, 返回这个会话最近 50 条对话记录.
func (d *Deps) GetHistory(ctx context.Context, c *app.RequestContext) {
	// 从 URL 查询参数里取 session_id, 比如 /api/history?session_id=abc-123
	// c.Query 返回的是 []byte, 用 string() 转成 Go 字符串
	// 为什么 c.Query 返回的是 []byte 而不是 string? 因为 Hertz 为了减少内存分配,
	// 直接返回对请求缓冲区的引用, 需要 string 时自己转
	sessionID := string(c.Query("session_id"))

	// 没传 session_id, 返回 400, 告诉客户端这个参数是必填的
	if sessionID == "" {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "session_id is required"})
		return
	}

	// 调 HistoryService 查询对话历史.
	// d.history 是 Deps 里的 HistoryService 指针, 在 NewDeps 时创建好的.
	// 返回两个值: result 是查询结果, err 是错误
	// result 里包含 SessionID 和 Items(对话记录列表)
	result, err := d.history.GetHistory(ctx, sessionID)
	if err != nil {
		// 打日志, 记录是哪个会话查失败了, 方便排查
		logger.Error("query history failed", zap.String("session_id", sessionID), zap.Error(err))
		// 返回 500 + 笼统信息, 不把数据库错误细节泄露给客户端
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	// 把 service 返回的 HistoryItem 转成 handler 的 historyItem.
	// 为什么要转一层? service 层不依赖 HTTP 框架, 它的 HistoryItem 不带 json tag,
	// handler 层的 historyItem 带 json tag, 控制 JSON 输出的字段名.
	// make 先分配好切片容量, 免得 append 时频繁扩容
	// make([]historyItem, 0, len(result.Items)) — 长度 0, 容量等于 result.Items 的长度
	items := make([]historyItem, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, historyItem{
			Role:      item.Role,      // 角色: "user" 或 "assistant"
			Content:   item.Content,   // 消息内容
			Timestamp: item.Timestamp, // 发生时间, 已格式化成可读字符串
		})
	}

	// 返回 HTTP 200 + JSON 响应体.
	// consts.StatusOK 就是 200.
	// historyResp 会被 c.JSON 序列化成 JSON 发给客户端.
	c.JSON(consts.StatusOK, historyResp{
		SessionID: result.SessionID, // 哪个会话的历史
		Items:     items,             // 对话记录列表, 按时间正序排列
	})
}
