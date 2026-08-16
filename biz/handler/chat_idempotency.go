package handler

import (
	"context"
	"database/sql"
	"errors"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/provider/llm"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// handleExistingChatRequest 处理相同幂等键的重试。
// 返回 true 表示响应已经完成，调用方必须立即结束，不能再次调用模型。
func (d *Deps) handleExistingChatRequest(ctx context.Context, c *app.RequestContext, request data.ChatRequest) bool {
	if request.Status == data.ChatRequestStatusRunning && request.AssistantDialogueID == nil {
		// 进程可能在保存助手消息后、更新请求状态前退出。这里尝试收敛该中间状态。
		dialogue, err := d.repo.GetDialogueByTraceAndRole(ctx, request.TraceID, string(llm.RoleAssistant))
		if err == nil {
			if completeErr := d.repo.CompleteChatRequest(ctx, request.ID, dialogue.ID); completeErr == nil {
				request.Status = data.ChatRequestStatusCompleted
				request.AssistantDialogueID = &dialogue.ID
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			logger.Error("恢复聊天请求状态失败", zap.Error(err), zap.Int64("request_id", request.ID))
		}
	}

	switch request.Status {
	case data.ChatRequestStatusCompleted:
		if request.AssistantDialogueID == nil {
			writeChatConflict(c, chatErrorResultMissing, request.TraceID)
			return true
		}
		dialogue, err := d.repo.GetDialogue(ctx, *request.AssistantDialogueID)
		if err != nil {
			logger.Error("读取幂等聊天结果失败", zap.Error(err), zap.Int64("request_id", request.ID))
			writeChatConflict(c, chatErrorResultMissing, request.TraceID)
			return true
		}
		writeReplaySSE(c, dialogue, request.TraceID)
		return true
	case data.ChatRequestStatusFailed:
		code := chatErrorRequestFailed
		if request.ErrorCode != nil && *request.ErrorCode != "" {
			code = *request.ErrorCode
		}
		writeChatConflict(c, code, request.TraceID)
		return true
	default:
		writeChatConflict(c, chatErrorRequestRunning, request.TraceID)
		return true
	}
}

func writeReplaySSE(c *app.RequestContext, dialogue data.Dialogue, traceID string) {
	prepareSSE(c)
	if err := writeSSE(c, "message", map[string]any{
		"content": dialogue.Content, "replayed": true, "trace_id": traceID,
	}); err != nil {
		return
	}
	if err := writeSSE(c, "usage", map[string]any{
		"prompt_tokens": dialogue.PromptTokens, "completion_tokens": dialogue.CompletionTokens,
		"cache_hit_tokens": dialogue.CacheHitTokens, "cache_miss_tokens": dialogue.CacheMissTokens,
		"reasoning_tokens": dialogue.ReasoningTokens, "total_tokens": dialogue.TotalTokens,
		"replayed": true,
	}); err != nil {
		return
	}
	_ = writeSSE(c, "done", map[string]any{"replayed": true, "trace_id": traceID})
}

func writeChatConflict(c *app.RequestContext, code, traceID string) {
	c.JSON(consts.StatusConflict, map[string]any{
		"code": code, "message": "chat request cannot be executed again",
		"retryable": false, "trace_id": traceID,
	})
}

func (d *Deps) failChatRequest(requestID int64, traceID, code string) {
	if err := d.repo.FailChatRequest(context.Background(), requestID, code); err != nil {
		logger.Error("记录聊天请求失败状态失败", zap.Error(err), zap.Int64("request_id", requestID), zap.String("trace_id", traceID))
	}
}
