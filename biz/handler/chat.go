package handler

// chat.go 放 POST /api/chat 接口的 handler。
// 这是核心接口：客户端发消息，服务端流式返回 LLM 的回答。
// 用 SSE（Server-Sent Events）协议实现流式传输。

import (
	"context"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/swallow-sun/swallow-go/internal/agent"
	"github.com/swallow-sun/swallow-go/internal/trace"
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

	// session_id 和 message 都是必填，缺一个就返回 400
	if req.SessionID == "" || req.Message == "" {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "session_id and message are required"})
		return
	}

	// 拿 session_id 去数据库查，验证这个会话确实存在。
	// 防止客户端传了一个不存在的 session_id 还要走后面一堆逻辑
	session, err := d.repo.GetSession(ctx, req.SessionID)
	if err != nil {
		// 会话不存在，返回 400（客户端的错，不是服务端的错）
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "session not found"})
		return
	}

	// 每个请求动态创建 Agent（sessionID/userID 不同不能复用）。
	// Agent 里面绑定了当前会话的 ID 和用户 ID，所以不能跨请求共用。
	// NewWithDB 传入：LLM 客户端、模型名、系统提示词文件路径、记忆存储、会话 ID、用户 ID
	ag, err := agent.NewWithDB(
		d.llm, d.cfg.LLM.Model, "prompts/system.md",
		d.mem, session.ID, session.UserID,
	)
	if err != nil {
		logger.Error("创建 Agent 失败", zap.Error(err))
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "agent init failed"})
		return
	}

	// 给这次对话设一个 60 秒超时。
	// 60 秒内 LLM 没回完就强制取消，防止客户端无限等待。
	// context.WithTimeout 返回一个新的 context 和一个取消函数
	chatCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	// defer cancel() 保证函数退出时释放超时 context 占的资源
	defer cancel()

	// 调 Agent 的流式对话方法，拿到一个 streamReader（流式读取器）。
	// streamReader 可以一个 chunk 一个 chunk 地读取 LLM 的回复
	streamReader, err := ag.ChatStream(chatCtx, req.Message)
	if err != nil {
		logger.Error("LLM 流式调用失败", zap.Error(err))
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "llm call failed"})
		return
	}

	// 从 streamReader 里掏出这次调用的 trace ID。
	// 流式结束后保存回复要用同一个 trace ID，保证整条链路可追踪
	traceID := agent.GetTraceID(streamReader)

	// 无论后续正常结束还是发生错误，都关闭 LLM 响应体。
	// defer 保证函数退出时执行，类似 telemetry.go 里的 defer logger.Sync()
	defer func() {
		if err := streamReader.Close(); err != nil {
			// 关闭失败只打 Warn 级别日志（不是 Error），因为不影响主流程
			logger.Warn(
				"关闭 LLM 流失败",
				zap.Error(err),
				zap.String("trace_id", traceID),
			)
		}
	}()

	// 设置标准 SSE 响应头，告诉浏览器/客户端"我要流式推数据了"。
	// 调用 writeSSE 前只需要执行一次
	prepareSSE(c)

	// strings.Builder 是 Go 里高效拼接字符串的工具。
	// LLM 一个 chunk 一个 chunk 回复，我们边收边发给客户端，
	// 同时用 replyBuilder 把所有 chunk 拼起来，等流结束后一次性存数据库
	var replyBuilder strings.Builder

	// 主循环：不断从 streamReader 读 chunk
	for {
		// Next() 返回三个值：
		// chunk - 这次读到的一段文本（可能是一个字、一个词、一句话）
		// done  - LLM 是否已经回完了（true 表示流结束）
		// err   - 读取错误（网络断开、LLM 报错等）
		chunk, done, err := streamReader.Next()

		// 读取出错了
		if err != nil {
			logger.Error(
				"读取 LLM 流失败",
				zap.Error(err),
				zap.String("trace_id", traceID),
			)

			// 告诉客户端本次流式响应失败。
			// 用 writeSSE 发一个 error 事件，客户端收到后知道这次对话失败了
			if writeErr := writeSSE(
				c,
				"error",
				map[string]string{
					"message": "stream read failed",
				},
			); writeErr != nil {
				// 发送错误事件本身也失败了（通常客户端已经断开）
				logger.Error(
					"发送 SSE 错误事件失败",
					zap.Error(writeErr),
					zap.String("trace_id", traceID),
				)
			}

			// 失败时不发送 done，也不保存残缺回复。直接 return 结束
			return
		}

		// LLM 已经回完了，跳出循环
		if done {
			break
		}

		// 把这次读到的 chunk 拼到 replyBuilder 里，等会儿存数据库
		replyBuilder.WriteString(chunk)

		// 将这次读到的 chunk 编码成 JSON，通过 SSE 发给客户端。
		// 客户端收到一个 message 事件，就知道 LLM 又回了一段文字
		if err := writeSSE(
			c,
			"message",
			map[string]string{
				"content": chunk,
			},
		); err != nil {
			// 发送失败了，通常表示客户端已经断开连接。
			// 打日志后直接 return，不用再发了
			logger.Error(
				"发送 SSE 消息失败",
				zap.Error(err),
				zap.String("trace_id", traceID),
			)
			return
		}
	}

	// ===== 流式读取结束，下面是收尾工作 =====

	// 完整读取后，取得 token 用量和性能指标。
	// usage 包含输入/输出 token 数等
	// metrics 包含首 token 耗时、总耗时等
	usage := streamReader.Usage()
	metrics := agent.GetStreamMetrics(streamReader)

	// 使用原 trace ID 保存助手回复。
	// 这里新建一个 context.Background() 而不是复用上面的 chatCtx，
	// 因为 chatCtx 有 60 秒超时，可能已经快到期了。
	// 用 trace.WithID 把原来的 trace ID 塞进新 context 里
	finishCtx := trace.WithID(
		context.Background(),
		traceID,
	)

	// 调 Agent 的 FinishStream 方法，把完整回复 + token 用量 + 性能指标存进数据库
	if err := ag.FinishStream(
		finishCtx,
		replyBuilder.String(), // 拼接好的完整回复
		usage,                 // token 用量
		metrics,               // 性能指标
	); err != nil {
		logger.Error(
			"保存助手回复失败",
			zap.Error(err),
			zap.String("trace_id", traceID),
		)

		// 保存失败了也告诉客户端
		if writeErr := writeSSE(
			c,
			"error",
			map[string]string{
				"message": "save assistant reply failed",
			},
		); writeErr != nil {
			logger.Error(
				"发送 SSE 错误事件失败",
				zap.Error(writeErr),
				zap.String("trace_id", traceID),
			)
		}

		return
	}

	// 刷新会话最后活跃时间（更新数据库 sessions 表的 updated_at）
	d.idm.TouchSession(finishCtx, session.ID)

	// 将最终 token 用量和性能指标发送给客户端。
	// 客户端收到 usage 事件后可以展示 token 消耗
	if err := writeSSE(
		c,
		"usage",
		map[string]any{
			"prompt_tokens":     usage.PromptTokens,                       // 输入 token 数
			"completion_tokens": usage.CompletionTokens,                   // 输出 token 数
			"cache_hit_tokens":  usage.CacheHitTokens(),                   // 缓存命中的 token 数
			"cache_miss_tokens": usage.CacheMissTokens(),                  // 缓存未命中的 token 数
			"reasoning_tokens":  usage.CompletionTokensDetails.ReasoningTokens, // 推理 token 数（思维链）
			"total_tokens":      usage.TotalTokens,                        // 总 token 数
			"first_token_ms":    metrics.FirstTokenMs,                     // 首 token 耗时（毫秒）
			"total_duration_ms": metrics.TotalDurationMs,                  // 总耗时（毫秒）
		},
	); err != nil {
		logger.Error(
			"发送 SSE usage 事件失败",
			zap.Error(err),
			zap.String("trace_id", traceID),
		)
		return
	}

	// 所有操作成功后才发送 done 事件。
	// 客户端收到 done 就知道"这次对话完整结束了，可以关闭连接了"
	if err := writeSSE(
		c,
		"done",
		map[string]any{},
	); err != nil {
		logger.Error(
			"发送 SSE done 事件失败",
			zap.Error(err),
			zap.String("trace_id", traceID),
		)
	}
}
