// chat.go 放 ChatService: 核心对话业务逻辑.
//
// 做的事情:
//  1. 幂等校验: 用 client_message_id 防止网络重试导致重复调用模型.
//  2. 创建 Agent: 每个请求动态创建, 绑定当前会话的 sessionID 和 userID.
//  3. 调 LLM 流式: 拿到 streamReader 逐块读取模型回复.
//  4. 逐块发 channel: 把 chunk 通过 ChatEvent channel 发给 handler, handler 转 SSE 写给客户端.
//  5. 收尾持久化: 保存助手回复 + 完成幂等请求 + 刷新会话活跃时间.
//  6. 幂等重放: 相同幂等键的已完成请求, 读出历史结果通过 channel 重放给客户端.
//
// 设计要点:
//   - Chat 返回 (<-chan ChatEvent, error). error 非 nil 表示请求还没开始就失败了(幂等冲突, Agent 创建失败等),
//     handler 直接写 HTTP 错误响应; error 为 nil 表示流式已开始, handler 从 channel 读事件转 SSE.
//   - channel 关闭表示所有事件发完了(正常结束或中途出错).
//   - service 监听 ctx, 客户端断了就停止读 LLM 流并清理.
package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/swallow-sun/swallow-go/internal/agent"
	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/provider/llm"
	"github.com/swallow-sun/swallow-go/internal/trace"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// ChatService 负责对话业务逻辑.
type ChatService struct {
	deps *Deps
}

// NewChatService 创建一个 ChatService.
// deps 是底层依赖(repo/idm/mem/llm/cfg), 由 handler 层的 NewDeps 传进来.
func NewChatService(deps *Deps) *ChatService {
	return &ChatService{deps: deps}
}

// Chat 启动一轮流式对话.
//
// 返回值:
//   - chan ChatEvent: 事件流, handler 从里面读事件转 SSE. channel 关了就是所有事件发完了.
//   - error: 不是 nil 说明请求还没开始就失败了(幂等冲突, Agent 创建失败等), handler 直接返回 HTTP 错误.
//
// ctx 由 handler 传进来, service 用它检测客户端有没有断开.
func (s *ChatService) Chat(ctx context.Context, sessionID, clientMessageID, message string) (<-chan ChatEvent, error) {
	// 创建子 Span: Service 层, 记录对话业务逻辑的耗时.
	// trace.StartSpan 从 context 里取父 Span(Handler 层的根 Span), 把自己挂上去.
	// 返回的 ctx 里塞了当前 Span, 后面 ChatStream 调 trace.StartSpan 时能从 ctx 里找到这个 Span 作为父.
	ctx, span := trace.StartSpan(ctx, "chat_service", "chat")
	// 给 Span 加附加属性: 会话 ID, 方便后续按会话查询 Span
	span.SetAttr("session_id", sessionID)
	// defer span.EndOK() 保证无论正常返回还是中途出错都标记 Span 结束
	defer span.EndOK()

	// 拿 session_id 去数据库查, 确认这个会话确实存在.
	// 防止客户端传了个不存在的 session_id 还往下走一堆逻辑
	session, err := s.deps.repo.GetSession(ctx, sessionID)
	if err != nil {
		// 会话不存在, 返回 400(客户端的错)
		return nil, NewChatError(400, "session_not_found", "")
	}

	// 整轮聊天共用同一个超时上下文和 trace ID.
	// context.WithTimeout 基于父 ctx 派生一个子 ctx, 到 60 秒自动取消
	// 返回值 cancel 是取消函数, 用完要调, 不然 60 秒的定时器不会释放
	chatCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	// trace.Ensure 检查 chatCtx 里有没有 trace ID, 没有就生成一个塞进去
	// 返回值 chatCtx 是塞了 trace ID 的上下文, traceID 是取出来的 ID 字符串
	chatCtx, traceID := trace.Ensure(chatCtx)

	// 在调模型前先占用幂等键. 如果记录已经存在了, 绝不能再调一次模型.
	chatRequest, created, err := s.deps.repo.BeginChatRequest(
		chatCtx, clientMessageID, session.ID, session.UserID, traceID,
	)
	if err != nil {
		logger.Error("Failed to create chat request record", zap.Error(err), zap.String("trace_id", traceID))
		cancel()
		return nil, NewChatError(500, "chat_request_init_failed", traceID)
	}

	// 幂等键已经存在 → 不调模型, 走重放/拒绝逻辑
	if !created {
		events, err := s.handleExistingChatRequest(chatCtx, chatRequest, traceID)
		// err != nil 表示幂等冲突(running/failed), 返回 ChatError 给 handler
		if err != nil {
			cancel()
			return nil, err
		}
		// events 是重放事件流(3 个事件), 用独立的 channel 发给 handler
		// 重放不需要后面调 LLM, cancel 掉超时上下文
		cancel()
		return events, nil
	}

	// 每个请求动态创建一个 Agent(sessionID/userID 不同不能复用).
	// Agent 里面绑了当前会话的 ID 和用户 ID, 所以不能跨请求共用.
	// NewWithDB 传入: LLM 客户端, 供应商名, 模型名, 系统提示词文件路径, 记忆存储, 会话 ID, 用户 ID
	ag, err := agent.NewWithDB(
		s.deps.llm, s.deps.cfg.LLM.Provider, s.deps.cfg.LLM.Model, "prompts/system.md",
		s.deps.mem, session.ID, session.UserID,
	)
	if err != nil {
		s.failChatRequest(chatRequest.ID, traceID, ChatErrorAgentInit)
		cancel()
		return nil, NewChatError(500, ChatErrorAgentInit, traceID)
	}

	// 调 Agent 的流式对话方法, 拿到一个 streamReader(流式读取器).
	// streamReader 可以一个 chunk 一个 chunk 地读 LLM 的回复
	streamReader, err := ag.ChatStream(chatCtx, message)
	if err != nil {
		s.failChatRequest(chatRequest.ID, traceID, ChatErrorConnect)
		cancel()
		return nil, NewChatError(500, ChatErrorConnect, traceID)
	}

	// ChatStream 连上模型后已经保存了用户消息, 这里把它关联到幂等请求并标记为运行中.
	userDialogue, err := s.deps.repo.GetDialogueByTraceAndRole(chatCtx, traceID, string(llm.RoleUser))
	if err != nil {
		s.failChatRequest(chatRequest.ID, traceID, ChatErrorUserMissing)
		streamReader.Close()
		cancel()
		return nil, NewChatError(500, ChatErrorUserMissing, traceID)
	}
	if err := s.deps.repo.MarkChatRequestRunning(chatCtx, chatRequest.ID, userDialogue.ID); err != nil {
		s.failChatRequest(chatRequest.ID, traceID, ChatErrorRequestState)
		streamReader.Close()
		cancel()
		return nil, NewChatError(500, ChatErrorRequestState, traceID)
	}

	// 创建事件 channel, handler 从这里读事件转 SSE.
	// make(chan ChatEvent, 64) 开一个带 64 个位置缓冲的 channel
	// 缓冲大小 64: LLM 的 chunk 通常比较小但频率高, 适当缓冲免得 handler 写 SSE 慢了把 service 堵住
	events := make(chan ChatEvent, 64)

	// go 关键字启动一个新协程(轻量级线程), 在后台跑 streamLoop
	// 主协程直接返回 events channel 给 handler, handler 和 streamLoop 并行工作
	go s.streamLoop(chatCtx, ag, streamReader, chatRequest, events, traceID, cancel)

	return events, nil
}

// streamLoop 是流式读取的主循环, 在独立的协程里跑.
// 不停地从 streamReader 读 chunk, 发到 events channel,
// 读完后做收尾持久化(保存助手回复 + 完成幂等请求 + 刷新会话活跃时间),
// 最后关闭 channel 告诉 handler 事件发完了.
func (s *ChatService) streamLoop(
	chatCtx context.Context,
	ag *agent.Agent,
	streamReader llm.StreamReader,
	chatRequest data.ChatRequest,
	events chan<- ChatEvent,
	traceID string,
	cancel context.CancelFunc,
) {
	// defer 把这个匿名函数推迟到 streamLoop return 时执行
	// 无论下面是正常结束还是中途出错, defer 里的代码一定会跑
	// 这里做三件事: 关 streamReader, cancel 超时上下文, 关 channel
	defer func() {
		// 关掉 LLM 流式读取器, 释放底层 HTTP 连接
		if err := streamReader.Close(); err != nil {
			// 关闭失败只打 Warn(不是 Error), 因为不影响主流程
			logger.Warn("Failed to close LLM stream", zap.Error(err), zap.String("trace_id", traceID))
		}
		// cancel() 取消 60 秒的超时上下文, 释放定时器资源
		cancel()
		// close(events) 关闭 channel, 告诉 handler 事件全发完了
		// handler 那边 for range events 会自动退出循环
		close(events)
	}()

	// strings.Builder 是 Go 里高效拼接字符串的工具.
	// LLM 一个 chunk 一个 chunk 回复, 我们边收边发给客户端,
	// 同时用 replyBuilder 把所有 chunk 拼起来, 等流结束了一次性存数据库
	var replyBuilder strings.Builder

	// for {} 是一个死循环, 不停从 LLM 读 chunk, 直到读完或出错才跳出
	for {
		// select 是 Go 的多路复用, 同时监听多个 channel
		// 这里只监听一个 chatCtx.Done(), 配合 default 实现非阻塞检查
		select {
		// chatCtx.Done() 返回一个 channel, ctx 被取消时这个 channel 会被关闭
		// <-chatCtx.Done() 从这个 channel 读, 读到了说明客户端断了或超时了
		case <-chatCtx.Done():
			s.failChatRequest(chatRequest.ID, traceID, ChatErrorClientClosed)
			return
		// default 表示上面的 case 没就绪就立即执行
		// 也就是说客户端没断就继续往下读 LLM
		default:
		}

		// Next() 返回三个值:
		// chunk - 这次读到的一段文本(可能是一个字, 一个词, 一句话)
		// done  - LLM 是否已经回完了(true 表示流结束)
		// err   - 读取错误(网络断开, LLM 报错等)
		chunk, done, err := streamReader.Next()

		// 读取出错了
		if err != nil {
			s.failChatRequest(chatRequest.ID, traceID, ChatErrorStreamRead)
			logger.Error("Failed to read LLM stream", zap.Error(err), zap.String("trace_id", traceID))
			// 告诉客户端这次流式响应失败了
			// sendEvent 把事件发到 channel, handler 读到后转 SSE 写给客户端
			// 这里 Content 为空, 客户端收到后知道这次回复失败了
			sendEvent(events, ChatEvent{Type: ChatEventMessage, Content: "", TraceID: traceID})
			return
		}

		// LLM 已经回完了, 跳出循环
		if done {
			break
		}

		// WriteString 把 chunk 追加到 replyBuilder 内部的 buffer 里
		replyBuilder.WriteString(chunk)

		// 把 chunk 发到 channel, handler 读到后转 SSE 写给客户端
		sendEvent(events, ChatEvent{Type: ChatEventMessage, Content: chunk, TraceID: traceID})
	}

	// ===== 流式读取结束, 下面是收尾工作 =====

	// 完整读取后, 取 token 用量和性能指标.
	// Usage() 返回 LLM 报告的 token 用量(输入/输出/缓存等)
	usage := streamReader.Usage()
	// GetStreamMetrics 从 streamReader 里提取性能指标(首 token 耗时, 总耗时等)
	metrics := agent.GetStreamMetrics(streamReader)

	// 用原来的 trace ID 保存助手回复.
	// 这里新建一个 context.Background() 而不是复用上面的 chatCtx,
	// 因为 chatCtx 有 60 秒超时, 可能快到期了.
	// context.Background() 返回一个空的 context, 没有超时, 不会被取消
	// trace.WithID 把 traceID 塞进这个空 context 里, 这样日志还能关联到这次请求
	finishCtx := trace.WithID(context.Background(), traceID)

	// 调 Agent 的 FinishStream 方法, 把完整回复 + token 用量 + 性能指标存进数据库
	if err := ag.FinishStream(finishCtx, replyBuilder.String(), usage, metrics); err != nil {
		s.failChatRequest(chatRequest.ID, traceID, ChatErrorAssistantSave)
		logger.Error("Failed to save assistant reply", zap.Error(err), zap.String("trace_id", traceID))
		return
	}

	// 写一条 model_usages 记录, 独立保存这次模型调用的 Token 用量和费用估算.
	// 方案要求: model_usages 写入失败应记 ERROR 和待补偿标记, 不能静默丢失成本数据.
	// 当前阶段没有价格快照表, estimated_cost_micros 留空(nil), 等 model_price_snapshots 建好后再填.
	// llm.Usage 里的 int 字段转成 *int 指针: 供应商返回了 0 就是 &0, 没返回的字段传 nil.
	modelUsage := data.ModelUsage{
		RequestID:         traceID,                           // 内部请求标识, 目前用 trace_id
		TraceID:           traceID,                           // 链路追踪 ID
		SessionID:         chatRequest.SessionID,             // 哪个会话产生的
		UserID:            chatRequest.UserID,                // 哪个用户产生的
		Provider:          s.deps.cfg.LLM.Provider,          // 供应商名称, 如 "deepseek"
		Model:             s.deps.cfg.LLM.Model,              // 模型名, 如 "deepseek-chat"
		Operation:         data.ModelOperationChat,           // 操作类型: 文字对话
		InputTokens:       data.IntPtr(usage.PromptTokens),              // 输入 Token 总量
		OutputTokens:      data.IntPtr(usage.CompletionTokens),           // 输出 Token 数
		CachedInputTokens: data.IntPtr(usage.CacheHitTokens()),            // 缓存命中输入 Token
		CacheMissTokens:   data.IntPtr(usage.CacheMissTokens()),           // 缓存未命中输入 Token(DeepSeek 返回 prompt_cache_miss_tokens)
		// CacheCreationTokens 留 nil: 当前用 DeepSeek/OpenAI, 不返回缓存创建 Token(Anthropic 才有)
		// ReasoningTokens 只有推理模型才返回, 普通对话模型返回 0, 这里如实保存
		ReasoningTokens:   data.IntPtr(usage.CompletionTokensDetails.ReasoningTokens),
		TotalTokens:       data.IntPtr(usage.TotalTokens),               // 总 Token 数
		// 音频和图片相关字段留 nil: 当前只有文字对话, 没有 ASR/TTS/视觉
		Currency:          "",                               // 无费用估算, 币种留空
		// EstimatedCostMicros 留 nil: 等价格快照表建好后再填
		ProviderRequestID: "",                               // 供应商请求 ID, 有些供应商不返回
		Status:            data.ModelUsageStatusOK,          // 调用成功
		DurationMs:        metrics.TotalDurationMs,          // 总耗时(毫秒)
		OccurredAt:        time.Now(),                       // 调用发生时间
	}
	if err := s.deps.repo.InsertModelUsage(finishCtx, modelUsage); err != nil {
		// 写入失败不能静默丢失, 记 ERROR 日志, 但不阻断主流程(对话已经成功了)
		// 方案说"普通文字对话可返回成功并报告观测降级"
		logger.Error("Failed to insert model usage record, cost data may be lost",
			zap.Error(err),
			zap.String("trace_id", traceID),
			zap.String("provider", modelUsage.Provider),
			zap.String("model", modelUsage.Model),
		)
	}

	// 助手消息已经存进数据库后再完成幂等请求. 两步之间如果进程断了, 重试时会按 trace 恢复状态.
	// GetDialogueByTraceAndRole 按 trace ID 和角色查对话记录
	// string(llm.RoleAssistant) 把枚举转成字符串 "assistant"
	assistantDialogue, err := s.deps.repo.GetDialogueByTraceAndRole(finishCtx, traceID, string(llm.RoleAssistant))
	if err != nil {
		logger.Error("Failed to read saved assistant message", zap.Error(err), zap.String("trace_id", traceID))
		return
	}
	// CompleteChatRequest 把幂等请求状态从 running 改成 completed, 同时记录助手消息 ID
	if err := s.deps.repo.CompleteChatRequest(finishCtx, chatRequest.ID, assistantDialogue.ID); err != nil {
		logger.Error("Failed to complete chat request status", zap.Error(err), zap.String("trace_id", traceID))
		return
	}

	// 刷新会话最后活跃时间(更新数据库 sessions 表的 updated_at)
	s.deps.idm.TouchSession(finishCtx, chatRequest.SessionID)

	// 把最终的 token 用量和性能指标组装成 UsageData, 发给客户端
	// &UsageData{...} 取结构体的指针, 这样 Usage 字段引用的是同一份数据
	usageData := &UsageData{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		CacheHitTokens:    usage.CacheHitTokens(),   // 缓存命中算出来的
		CacheMissTokens:   usage.CacheMissTokens(),   // 缓存未命中算出来的
		ReasoningTokens:   usage.CompletionTokensDetails.ReasoningTokens, // 思维链 token 数
		TotalTokens:       usage.TotalTokens,
		FirstTokenMs:      metrics.FirstTokenMs,    // 首 token 耗时(毫秒)
		TotalDurationMs:   metrics.TotalDurationMs, // 总耗时(毫秒)
	}
	// 通过 ChatEventUsage 事件发给 handler, handler 转成 SSE usage 帧写给客户端
	sendEvent(events, ChatEvent{Type: ChatEventUsage, Usage: usageData, TraceID: traceID})

	// 所有操作成功后才发送 done 事件.
	// 客户端收到 done 就知道"这次对话完整结束了, 可以关闭连接了"
	sendEvent(events, ChatEvent{Type: ChatEventDone, TraceID: traceID})
}

// handleExistingChatRequest 处理相同幂等键的重试.
// 返回两种结果:
//   - (chan ChatEvent, nil): 请求已完成, 返回重放事件流(3 个事件: message + usage + replayDone)
//   - (nil, *ChatError): 幂等冲突(running/failed), handler 直接返回 HTTP 错误
func (s *ChatService) handleExistingChatRequest(ctx context.Context, request data.ChatRequest, traceID string) (<-chan ChatEvent, *ChatError) {
	// 请求状态是 running 但没有助手消息 ID, 说明进程在保存助手消息后,
	// 更新请求状态前崩了. 这里尝试修复这个中间状态.
	if request.Status == data.ChatRequestStatusRunning && request.AssistantDialogueID == nil {
		// 按 trace ID 和 assistant 角色查助手消息
		dialogue, err := s.deps.repo.GetDialogueByTraceAndRole(ctx, request.TraceID, string(llm.RoleAssistant))
		if err == nil {
			// 找到了助手消息, 把请求状态补完为 completed
			if completeErr := s.deps.repo.CompleteChatRequest(ctx, request.ID, dialogue.ID); completeErr == nil {
				logger.Info("Successfully recovered interrupted chat request",
					zap.Int64("request_id", request.ID),
					zap.String("trace_id", request.TraceID),
				)
				request.Status = data.ChatRequestStatusCompleted
				request.AssistantDialogueID = &dialogue.ID
			} else {
				// 补完也失败了, 记录日志后继续走默认分支返回 running 冲突
				logger.Error("Failed to recover interrupted chat request",
					zap.Error(completeErr),
					zap.Int64("request_id", request.ID),
					zap.String("trace_id", request.TraceID),
				)
			}
			// errors.Is(err, sql.ErrNoRows) 判断 err 是不是 "查不到记录" 这个错误
			// sql.ErrNoRows 是 Go 标准库 database/sql 里定义的, 表示查询结果为空
			// !errors.Is(...) 取反: 不是 "查不到", 说明是真正的数据库错误(连接断了等)
			} else if !errors.Is(err, sql.ErrNoRows) {
			// 不是"没找到"而是真正的数据库错误
			logger.Error("Failed to query assistant message for chat request recovery",
				zap.Error(err),
				zap.Int64("request_id", request.ID),
				zap.String("trace_id", request.TraceID),
			)
		}
		// err == sql.ErrNoRows 时静默: 说明模型还没回完就崩了, 走 default 返回 running
	}

	// switch 根据请求状态走不同分支, 跟 if-else 链类似但更清晰
	switch request.Status {
	case data.ChatRequestStatusCompleted:
		// 标记已完成但找不到助手消息 ID, 数据不一致
		if request.AssistantDialogueID == nil {
			logger.Error("Idempotent request marked completed but missing assistant message ID",
				zap.Int64("request_id", request.ID),
				zap.String("trace_id", request.TraceID),
			)
			return nil, NewChatError(409, ChatErrorResultMissing, request.TraceID)
		}
		// 按 ID 读出上次的助手回复, 通过 SSE 重放给客户端
		// *request.AssistantDialogueID 是指针解引用, 把 *int64 取出 int64 值
		dialogue, err := s.deps.repo.GetDialogue(ctx, *request.AssistantDialogueID)
		if err != nil {
			logger.Error("Failed to read idempotent chat result",
				zap.Error(err),
				zap.Int64("request_id", request.ID),
				zap.String("trace_id", request.TraceID),
			)
			return nil, NewChatError(409, ChatErrorResultMissing, request.TraceID)
		}
		logger.Info("Replaying completed idempotent chat result",
			zap.Int64("request_id", request.ID),
			zap.String("trace_id", request.TraceID),
		)
		// 用缓冲 channel 装 3 个重放事件, 缓冲大小 3 刚好够放
		// make(chan ChatEvent, 3) 开一个带 3 个位置缓冲的 channel
		events := make(chan ChatEvent, 3)
		// <- 是往 channel 里塞值, 塞满 3 个位置后不会阻塞(因为缓冲够用)
		events <- ChatEvent{Type: ChatEventMessage, Content: dialogue.Content, Replayed: true, TraceID: request.TraceID}
		events <- ChatEvent{
			Type: ChatEventUsage,
			Usage: &UsageData{
				PromptTokens:     dialogue.PromptTokens,
				CompletionTokens: dialogue.CompletionTokens,
				CacheHitTokens:    dialogue.CacheHitTokens,
				CacheMissTokens:   dialogue.CacheMissTokens,
				ReasoningTokens:   dialogue.ReasoningTokens,
				TotalTokens:       dialogue.TotalTokens,
			},
			Replayed: true,
			TraceID:  request.TraceID,
		}
		events <- ChatEvent{Type: ChatEventReplayDone, Replayed: true, TraceID: request.TraceID}
		// close(events) 关闭 channel, 告诉 handler 重放事件发完了
		// handler 那边 for range events 读完后自动退出
		close(events)
		return events, nil

	case data.ChatRequestStatusFailed:
		// 上次失败了, 返回上次的错误码
		code := ChatErrorRequestFailed
		// request.ErrorCode 是 *string(指针), 可能为 nil(没存错误码)
		// 先判 nil 再解引用, 避免空指针 panic
		if request.ErrorCode != nil && *request.ErrorCode != "" {
			code = *request.ErrorCode
		}
		logger.Info("Idempotent chat request previously failed, refusing re-execution",
			zap.Int64("request_id", request.ID),
			zap.String("trace_id", request.TraceID),
			zap.String("error_code", code),
		)
		return nil, NewChatError(409, code, request.TraceID)

	default:
		// accepted 或 running 状态, 说明有另一个请求正在执行
		logger.Info("Idempotent chat request in progress, refusing concurrent execution",
			zap.Int64("request_id", request.ID),
			zap.String("trace_id", request.TraceID),
			zap.String("status", request.Status),
		)
		return nil, NewChatError(409, ChatErrorRequestRunning, request.TraceID)
	}
}

// failChatRequest 把幂等请求标记为失败, 记录稳定错误码.
// 用 context.Background() 而不是 chatCtx, 因为 chatCtx 可能在超时后已被取消,
// 但失败标记必须写入数据库, 否则重试时无法知道上次是失败的.
func (s *ChatService) failChatRequest(requestID int64, traceID, code string) {
	// FailChatRequest 把幂等请求状态改成 failed, 同时记录错误码
	// 用 context.Background() 而不是传进来的 ctx, 因为 ctx 可能已经超时取消了
	if err := s.deps.repo.FailChatRequest(context.Background(), requestID, code); err != nil {
		logger.Error("Failed to record chat request failure status",
			zap.Error(err),
			zap.Int64("request_id", requestID),
			zap.String("trace_id", traceID),
			zap.String("error_code", code),
		)
		return
	}
	logger.Info("Chat request marked as failed",
		zap.Int64("request_id", requestID),
		zap.String("trace_id", traceID),
		zap.String("error_code", code),
	)
}

// sendEvent 向 channel 发送事件, 带 panic 保护.
// channel 关闭后发送会 panic, 用 recover 兜住防止协程崩溃.
func sendEvent(events chan<- ChatEvent, event ChatEvent) {
	// defer + recover 兜住 panic
	// 如果 events channel 已经被 close 了, 往里面塞值会 panic
	// recover() 把 panic 接住, 防止协程崩溃导致整个程序挂掉
	defer func() { _ = recover() }()
	// events <- event 往 channel 里塞一个事件
	events <- event
}
