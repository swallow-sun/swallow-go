// chat.go 放 POST /api/chat 接口的 handler。
//
// 做的事情：
//  1. 解析客户端发来的 JSON 请求体（session_id、client_message_id、message）。
//  2. 调 ChatService.Chat 拿到事件 channel（或业务错误）。
//  3. 从 channel 读事件，转成 SSE 帧实时写给客户端。
//  4. 通过 ctx.Done() 检测客户端断开，停止读 channel。
//
// 幂等管理、LLM 调用、状态流转等业务逻辑全在 service 层，
// handler 只管 HTTP 解析和 SSE 协议转换。
package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/swallow-sun/swallow-go/biz/service"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// Chat POST /api/chat
// Chat 使用 SSE 返回流式对话。
// message 事件传输回答片段，usage 事件传输 Token 用量，
// error 事件表示流式读取失败，done 事件表示正常结束。
func (d *Deps) Chat(ctx context.Context, c *app.RequestContext) {
	// 声明请求结构体，准备接收客户端传来的 JSON
	var req chatReq

	// 解析 JSON 到 req 里，格式不对就返回 400
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// client_message_id 由客户端生成；同一条消息重试时必须保持不变。
	if req.SessionID == "" || req.ClientMessageID == "" || req.Message == "" {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "session_id, client_message_id and message are required"})
		return
	}
	if len(req.ClientMessageID) > service.MaxClientMessageIDLength {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "client_message_id is too long"})
		return
	}

	// 调 ChatService.Chat 启动流式对话。
	// 返回 events channel 和 error：
	//   error != nil → 请求还没开始就失败了（幂等冲突、Agent 创建失败等），直接写 HTTP 错误
	//   error == nil → 流式对话已开始，从 channel 读事件转 SSE
	events, err := d.chat.Chat(ctx, req.SessionID, req.ClientMessageID, req.Message)
	if err != nil {
		// 尝试转成 ChatError（业务错误，有状态码和错误码）
		if ce := service.FromChatError(err); ce != nil {
			c.JSON(ce.StatusCode, map[string]any{
				"code":      ce.Code,
				"message":   "chat request cannot be executed again",
				"retryable": false,
				"trace_id":  ce.TraceID,
			})
			return
		}
		// 不是 ChatError 就是内部错误，统一返回 500 + 笼统信息，不把内部细节泄露给客户端
		logger.Error("聊天服务失败", zap.Error(err))
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "chat service failed"})
		return
	}

	// 设置 SSE 响应头，告诉客户端"我要流式推数据了"。
	// 只需要在写 SSE 之前调一次
	prepareSSE(c)

	// 从 channel 读事件，转成 SSE 帧写给客户端。
	// channel 关闭 = 所有事件发完了（正常结束或中途出错）。
	// 同时通过 ctx.Done() 检测客户端是否断开，断了就 cancel 通知 service 停止。
	for event := range events {
		// 检测客户端是否断开
		select {
		case <-ctx.Done():
			logger.Info("客户端断开连接，停止读取事件", zap.String("trace_id", event.TraceID))
			return
		default:
		}

		// 根据事件类型转成对应的 SSE 帧
		switch event.Type {
		case service.ChatEventMessage:
			// 消息内容片段
			data := map[string]any{"content": event.Content}
			if event.Replayed {
				data["replayed"] = true
				data["trace_id"] = event.TraceID
			}
			if err := writeSSE(c, "message", data); err != nil {
				logger.Error("发送 SSE 消息失败", zap.Error(err), zap.String("trace_id", event.TraceID))
				return
			}

		case service.ChatEventUsage:
			// token 用量和性能指标
			if event.Usage != nil {
				data := map[string]any{
					"prompt_tokens":     event.Usage.PromptTokens,
					"completion_tokens": event.Usage.CompletionTokens,
					"cache_hit_tokens":  event.Usage.CacheHitTokens,
					"cache_miss_tokens": event.Usage.CacheMissTokens,
					"reasoning_tokens":  event.Usage.ReasoningTokens,
					"total_tokens":      event.Usage.TotalTokens,
					"first_token_ms":    event.Usage.FirstTokenMs,
					"total_duration_ms": event.Usage.TotalDurationMs,
				}
				if event.Replayed {
					data["replayed"] = true
				}
				if err := writeSSE(c, "usage", data); err != nil {
					logger.Error("发送 SSE usage 事件失败", zap.Error(err), zap.String("trace_id", event.TraceID))
					return
				}
			}

		case service.ChatEventDone:
			// 正常结束
			if err := writeSSE(c, "done", map[string]any{}); err != nil {
				logger.Error("发送 SSE done 事件失败", zap.Error(err), zap.String("trace_id", event.TraceID))
			}

		case service.ChatEventReplayDone:
			// 幂等重放结束
			if err := writeSSE(c, "done", map[string]any{
				"replayed": true,
				"trace_id": event.TraceID,
			}); err != nil {
				logger.Error("发送 SSE replay done 事件失败", zap.Error(err), zap.String("trace_id", event.TraceID))
			}
		}
	}
}
