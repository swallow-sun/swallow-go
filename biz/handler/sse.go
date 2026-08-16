package handler

import (
	"encoding/json"
	"fmt"

	"github.com/cloudwego/hertz/pkg/app"
)

// prepareSSE 设置 SSE 响应头。
// 调用 writeSSE 前只需要执行一次。
func prepareSSE(c *app.RequestContext) {
	c.Response.Header.SetContentType("text/event-stream")
	c.Response.Header.Set("Cache-Control", "no-cache")
	c.Response.Header.Set("Connection", "keep-alive")
}

// writeSSE 将任意 Go 数据编码成 JSON，并写成标准 SSE 事件。
//
// 输出格式：
//
//	event: message
//	data: {"content":"你好"}
//
// JSON 编码会将内容中的换行转换成 \n，避免破坏 SSE 数据帧。
func writeSSE(
	c *app.RequestContext,
	event string,
	data any,
) error {
	// 将结构体、map 等 Go 数据转换成 JSON。
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf(
			"marshal SSE event %q: %w",
			event,
			err,
		)
	}

	// 一条标准 SSE 消息由 event 和 data 两行组成，
	// 最后使用两个换行表示该事件结束。
	frame := fmt.Sprintf(
		"event: %s\ndata: %s\n\n",
		event,
		payload,
	)

	// 将数据写入 Hertz 响应体。
	if _, err := c.Write([]byte(frame)); err != nil {
		return fmt.Errorf(
			"write SSE event %q: %w",
			event,
			err,
		)
	}

	// 立即刷新，将事件实时发送给客户端。
	if err := c.Flush(); err != nil {
		return fmt.Errorf(
			"flush SSE event %q: %w",
			event,
			err,
		)
	}

	return nil
}
