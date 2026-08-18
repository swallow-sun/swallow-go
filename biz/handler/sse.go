// sse.go 放 SSE（Server-Sent Events）协议的工具函数。
//
// 做的事情：
//  1. prepareSSE：设置 SSE 响应头（text/event-stream、no-cache、keep-alive）。
//  2. writeSSE：把事件名和任意数据编码成标准 SSE 帧（event 行 + data 行），写入 HTTP 响应并立即 flush。
package handler

import (
	"encoding/json"
	"fmt"

	"github.com/cloudwego/hertz/pkg/app"
)

// prepareSSE 设置 SSE 响应头。
// 写 SSE 之前调一次就行。
// SSE（Server-Sent Events）是 HTTP 协议上的服务器推送机制，
// 服务端可以持续往同一个连接发数据，客户端不需要反复发请求。
func prepareSSE(c *app.RequestContext) {
	// c.Response.Header 是 Hertz 响应头对象，能读写 HTTP 头
	// SetContentType 设 Content-Type 头
	// "text/event-stream" 是 SSE 协议规定的 MIME 类型，
	// 告诉客户端"这是一条服务器推送的事件流，请按 SSE 协议解析"
	c.Response.Header.SetContentType("text/event-stream")

	// Set 方法设一个响应头，参数是头名和头值
	// "no-cache" 告诉浏览器和中间代理不要缓存这个响应，
	// 因为 SSE 是实时推送的，缓存没有意义
	c.Response.Header.Set("Cache-Control", "no-cache")

	// "keep-alive" 告诉 HTTP 层保持 TCP 连接不断开，
	// 这样服务端可以随时往同一个连接里写新事件
	c.Response.Header.Set("Connection", "keep-alive")
}

// writeSSE 把任意 Go 数据编码成 JSON，写成一条标准 SSE 事件。
//
// 输出格式：
//
//	event: message
//	data: {"content":"你好"}
//
// JSON 编码会把内容里的换行转成 \n，不会破坏 SSE 数据帧。
func writeSSE(
	c *app.RequestContext,
	event string,
	data any,
) error {
	// json.Marshal 是 Go 标准库 encoding/json 里的函数，
	// 把任意 Go 数据（结构体、map、切片等）转成 JSON 格式的字节切片 []byte
	// 举个例子，map[string]string{"content":"你好"} 会变成 {"content":"你好"}
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf(
			"marshal SSE event %q: %w",
			event,
			err,
		)
	}

	// 一条标准 SSE 消息由 event 和 data 两行组成，最后用两个换行表示该事件结束。
	// 举个例子，假设 event 是 "message"，payload 是 {"content":"你好"}，
	// 拼出来的字符串就是：
	//   event: message
	//   data: {"content":"你好"}
	//   （空行，即第二个换行）
	// 两个换行 \n\n 是 SSE 协议规定的"一条事件结束"的标记
	// fmt.Sprintf 是 Go 标准库 fmt 包里的函数，按格式字符串拼接内容
	// %s 是占位符，会被后面的参数依次替换
	frame := fmt.Sprintf(
		"event: %s\ndata: %s\n\n",
		event,
		payload,
	)

	// c.Write 是 Hertz 的方法，把字节切片写进 HTTP 响应体
	// []byte(frame) 把字符串转成字节切片，因为 c.Write 接收的是 []byte
	// 第一个返回值是写入的字节数，这里不需要，用 _ 丢弃
	if _, err := c.Write([]byte(frame)); err != nil {
		return fmt.Errorf(
			"write SSE event %q: %w",
			event,
			err,
		)
	}

	// c.Flush 是 Hertz 的方法，把缓冲区里的数据立刻发给客户端
	// SSE 要求实时推送，写完一条就得 flush，否则数据攒在缓冲区里客户端收不到
	if err := c.Flush(); err != nil {
		return fmt.Errorf(
			"flush SSE event %q: %w",
			event,
			err,
		)
	}

	return nil
}
