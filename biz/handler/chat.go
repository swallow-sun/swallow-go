// chat.go 放 POST /api/chat 接口的 handler.
//
// 做的事情:
//  1. 解析客户端发来的 JSON 请求体(session_id, client_message_id, message).
//  2. 调 ChatService.Chat 拿到事件 channel(或业务错误).
//  3. 从 channel 读事件, 转成 SSE 帧实时写给客户端.
//  4. 通过 ctx.Done() 检测客户端断开, 停止读 channel.
//
// 幂等管理, LLM 调用, 状态流转等业务逻辑全在 service 层,
// handler 只管 HTTP 解析和 SSE 协议转换.
package handler

	import (
		"context"
		"time"

		"github.com/cloudwego/hertz/pkg/app"
		"github.com/swallow-sun/swallow-go/biz/service"
		"github.com/swallow-sun/swallow-go/internal/apperror"
		"github.com/swallow-sun/swallow-go/internal/metrics"
		"github.com/swallow-sun/swallow-go/internal/trace"
		"github.com/swallow-sun/swallow-go/pkg/logger"
		"go.uber.org/zap"
	)


// Chat POST /api/chat
// Chat 使用 SSE 返回流式对话.
// message 事件传输回答片段, usage 事件传输 Token 用量,
// error 事件表示流式读取失败, done 事件表示正常结束.
func (d *Deps) Chat(ctx context.Context, c *app.RequestContext) {
	// 记录请求开始时间, defer 里算耗时打 Prometheus 指标
	start := time.Now()
	// status 记录这次请求的最终结果(ok/failed/cancelled),
	// 各退出路径修改它, defer 里统一调 RecordRequest
	status := metrics.StatusOK
	defer func() {
		metrics.RecordRequest(metrics.ComponentGo, "chat", status, float64(time.Since(start).Milliseconds()))
	}()

	// 认证: 校验 owner Bearer Token
	if !d.authorizeOwner(c) {
		status = metrics.StatusFailed
		return
	}

	// 确保 context 里有 trace ID, 后面 Span 和所有子调用共用同一个 trace ID.
	ctx, _ = trace.Ensure(ctx)

	// 声明请求结构体, 准备接收客户端传来的 JSON.
	// chatReq 有三个字段: session_id, client_message_id, message
	var req chatReq

// c.BindAndValidate 做两件事:
	//  1. 把请求体里的 JSON 解析到 req 里(反序列化)
	//  2. 调 req 的校验方法(如果有的话)做参数校验
	// &req 是取地址, 因为 BindAndValidate 要往里面写数据
	// 解析失败(JSON 格式不对, 字段类型不对)就返回 400
	if err := c.BindAndValidate(&req); err != nil {
		// c.JSON 写一个 JSON 响应给客户端, 第一个参数是 HTTP 状态码
		// consts.StatusBadRequest 就是 400
		status = metrics.StatusFailed
		writeErrorFromCtx(ctx, c, apperror.BadRequest("invalid_request_body", "invalid request body", ""))
		return
	}

	// 三个字段都不能为空: session_id 告诉服务端这段消息属于哪段对话,
	// client_message_id 做幂等控制(同一条消息重试时不变), message 是用户说的话.
	// 缺任何一个都返回 400
	if req.SessionID == "" || req.ClientMessageID == "" || req.Message == "" {
		status = metrics.StatusFailed
		writeErrorFromCtx(ctx, c, apperror.BadRequest("missing_required_fields", "session_id, client_message_id and message are required", ""))
		return
	}
	// 消息太长会撑爆 LLM 上下文窗口和数据库, 限制最大 64KB
	if len(req.Message) > MaxMessageLength {
		status = metrics.StatusFailed
		writeErrorFromCtx(ctx, c, apperror.BadRequest("message_too_long", "message is too long", ""))
		return
	}
	// client_message_id 太长会撑爆数据库字段, 限制最大 128 字符
	// service.MaxClientMessageIDLength 是 service 层定义的常量
	if len(req.ClientMessageID) > service.MaxClientMessageIDLength {
		status = metrics.StatusFailed
		writeErrorFromCtx(ctx, c, apperror.BadRequest("client_message_id_too_long", "client_message_id is too long", ""))
		return
	}

	// 确保 context 里有 trace ID 已在认证后完成, 这里创建根 Span.
	// trace.StartSpan 从 context 里取 trace ID 和父 Span(这里没有父 Span, 是根),
	// 把 Span 塞进 context 返回, 后面的 service 和 agent 层能通过 context 找到这个 Span 作为父.
	ctx, span := trace.StartSpan(ctx, "handler", "POST /api/chat")
	// defer span.EndOK() 保证无论正常返回还是中途出错都标记 Span 结束
	defer span.EndOK()

	// 调 ChatService.Chat 启动流式对话.
	// 传入 owner user ID, service 层用它做会话归属校验.
	// d.chat 是 Deps 里的 ChatService 指针, 在 NewDeps 时创建好的
	// 返回两个值:
	//   events — 一个 channel, service 层往里面塞 ChatEvent, handler 从里面读
	//   err — 请求还没开始就失败了(幂等冲突, Agent 创建失败等), 直接写 HTTP 错误
	// err == nil 说明流式对话已开始, 后面从 channel 读事件转 SSE
	events, err := d.chat.Chat(ctx, req.SessionID, req.ClientMessageID, req.Message, d.chat.OwnerID())
	if err != nil {
		// service.FromChatError 尝试把 error 转成 *ChatError
		// 转成了说明是业务错误(幂等冲突, 会话不存在等), 有明确的 HTTP 状态码和错误码
		// 转不成(返回 nil)说明是内部错误, 走下面的 500 分支
		if ce := service.FromChatError(err); ce != nil {
			// 业务错误: 用 ChatError 里的 StatusCode 做 HTTP 响应码, Code 做业务错误码
			// 比如 403 表示会话不存在, 409 表示幂等冲突(同一条消息重复发)
			// retryable=false 告诉客户端不要重试
			status = metrics.StatusFailed
			writeErrorFromCtx(ctx, c, apperror.New(ce.Code, "chat request error", ce.StatusCode, false, ce.TraceID))
			return
		}
		// 不是 ChatError 就是内部错误, 打日志后统一返回 500 + 笼统信息
		// 不把内部细节泄露给客户端, 防止暴露系统结构
		status = metrics.StatusFailed
		logger.Error("chat service failed", zap.Error(err))
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}

	// prepareSSE 设置 SSE 响应头, 告诉客户端"我要流式推数据了".
	// 它会设三个头: Content-Type: text/event-stream, Cache-Control: no-cache, Connection: keep-alive
	// 只需要在写 SSE 之前调一次
	prepareSSE(c)

	// for ... range events 从 channel 里循环读事件.
	// channel 关闭 = 所有事件发完了(正常结束或中途出错).
	// 每读到一个 event, 根据 event.Type 转成对应的 SSE 帧写给客户端.
	for event := range events {
		// select 是 Go 的多路复用, 同时监听多个 channel 操作
		// 这里只监听 ctx.Done(): 如果客户端断开了, ctx 会被取消, ctx.Done() 返回的 channel 就可读
		// default 分支表示 ctx 还没取消, 正常往下走处理事件
		select {
		case <-ctx.Done():
			// 客户端断开了, 打日志后直接 return, 不再读 channel
			// service 层那边如果还在往 channel 塞数据, 会因为没人读而阻塞,
			// 但 service 层也会监听 ctx, 最终一起退出
			status = metrics.StatusCancelled
			logger.Info("client disconnected, stop reading events", zap.String("trace_id", event.TraceID))
			return
		default:
		}

		// 根据事件类型转成对应的 SSE 帧
		// service 层定义了四种事件类型: Message, Usage, Done, ReplayDone
		switch event.Type {
		case service.ChatEventMessage:
			// 消息内容片段: LLM 每生成一段文字就往 channel 塞一个 Message 事件
			// data 是要编码进 SSE 帧的 JSON 对象
			data := map[string]any{"content": event.Content}
			// event.Replayed=true 表示这是历史结果的重放(幂等重试时把上次的回答重发一遍)
			// 重放时要告诉客户端这是旧的回答, 不是新生成的
			if event.Replayed {
				data["replayed"] = true
				data["trace_id"] = event.TraceID
			}
			// writeSSE 把 data 编码成 SSE 帧写给客户端
			// 第一个参数是 Hertz 的 RequestContext, 第二个是事件名, 第三个是数据
			if err := writeSSE(c, "message", data); err != nil {
				// 写失败(客户端已断开等), 打日志后直接 return
				status = metrics.StatusCancelled
				logger.Error("failed to send SSE message", zap.Error(err), zap.String("trace_id", event.TraceID))
				return
			}

		case service.ChatEventUsage:
			// token 用量和性能指标: LLM 流式读取结束后发一次
			// event.Usage 是 *UsageData 指针, nil 表示没附带用量数据, 跳过
			if event.Usage != nil {
				// 把 UsageData 的每个字段放进 map, 后面 writeSSE 会转成 JSON
				data := map[string]any{
					"prompt_tokens":     event.Usage.PromptTokens,   // 输入 token 数
					"completion_tokens": event.Usage.CompletionTokens, // 输出 token 数
					"cache_hit_tokens":  event.Usage.CacheHitTokens,   // 缓存命中的 token 数
					"cache_miss_tokens": event.Usage.CacheMissTokens,   // 缓存未命中的 token 数
					"reasoning_tokens":  event.Usage.ReasoningTokens,   // 推理 token 数(思维链)
					"total_tokens":      event.Usage.TotalTokens,       // 总 token 数
					"first_token_ms":    event.Usage.FirstTokenMs,      // 首 token 耗时(毫秒)
					"total_duration_ms": event.Usage.TotalDurationMs,    // 总耗时(毫秒)
				}
				// 重放时也要标记
				if event.Replayed {
					data["replayed"] = true
				}
				if err := writeSSE(c, "usage", data); err != nil {
					status = metrics.StatusCancelled
					logger.Error("failed to send SSE usage event", zap.Error(err), zap.String("trace_id", event.TraceID))
					return
				}
			}

		case service.ChatEventDone:
			// 正常结束: 所有回答片段都发完了, 发一个空的 done 事件告诉客户端"完了"
			// data 是空 map, 因为 done 事件不需要带数据
			if err := writeSSE(c, "done", map[string]any{}); err != nil {
				logger.Error("failed to send SSE done event", zap.Error(err), zap.String("trace_id", event.TraceID))
			}

		case service.ChatEventReplayDone:
			// 幂等重放结束: 客户端重试时, service 发现这条消息之前已经回答过了,
			// 把历史的回答重发一遍, 最后发这个带 replayed 标记的 done 事件
			if err := writeSSE(c, "done", map[string]any{
				"replayed": true,
				"trace_id": event.TraceID,
			}); err != nil {
				logger.Error("failed to send SSE replay done event", zap.Error(err), zap.String("trace_id", event.TraceID))
			}
		}
	}
}
