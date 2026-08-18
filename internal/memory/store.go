// store.go 放 memory.Store：对话历史的存取。
//
// 做的事情：
//  1. SaveMessage：把一条用户或助手消息写入 dialogues 表，记录 trace ID 和 token 用量，同时打埋点。
//  2. LoadHistory：从数据库加载最近 N 条对话，转成 LLM 用的 ChatMessage 切片（跳过 system 角色）。
//
// Phase 2：对话存 SQLite，每轮对话后写入，启动时从 DB 加载历史。
// 取代 Phase 1 的 Agent 内存 slice——数据存进数据库后重启不丢。
package memory

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/provider/llm"
	"github.com/swallow-sun/swallow-go/internal/telemetry"
	"github.com/swallow-sun/swallow-go/internal/trace"
)

// New 创建一个 memory Store。
func New(repo data.Repository) *Store {
	return &Store{repo: repo}
}

// SaveMessage 保存一条用户或助手消息，并记录 dialogue 埋点。
func (s *Store) SaveMessage(
	ctx context.Context,
	sessionID string,
	userID int64,
	role llm.Role,
	content string,
	usage llm.Usage,
) error {
	// 从上下文里拿到本轮对话的 trace ID。
	traceID := trace.FromContext(ctx)

	// 把 LLM usage 转成数据层的 TokenUsage。
	tokenUsage := data.TokenUsage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		CacheHitTokens:   usage.CacheHitTokens(),
		CacheMissTokens:  usage.CacheMissTokens(),
		ReasoningTokens:  usage.CompletionTokensDetails.ReasoningTokens,
		TotalTokens:      usage.TotalTokens,
	}

	// 保存消息到 dialogues 表。
	_, err := s.repo.InsertDialogue(
		ctx,
		sessionID,
		userID,
		string(role),
		content,
		tokenUsage,
		traceID,
	)

	if err != nil {
		// 消息保存失败时记录失败事件。
		telemetry.Emit(ctx, telemetry.EventDialogue, map[string]any{
			"session_id":    sessionID,
			"user_id":       userID,
			"role":          string(role),
			"content_chars": utf8.RuneCountInString(content),
			telemetry.FieldStatus: telemetry.StatusError,
			"error":         err.Error(),
		})

		return fmt.Errorf("save message: %w", err)
	}

	// dialogue 事件只描述保存了什么消息。
	// token 明细由 dialogues 表和 llm.stream.complete 事件负责，避免重复存储。
	telemetry.Emit(ctx, telemetry.EventDialogue, map[string]any{
		"session_id":    sessionID,
		"user_id":       userID,
		"role":          string(role),
		"content_chars": utf8.RuneCountInString(content),
		"content_bytes": len(content),
		telemetry.FieldStatus: telemetry.StatusOK,
	})

	return nil
}

// LoadHistory 从数据库加载最近 N 条对话，
// 转成 LLM 用的 ChatMessage。
func (s *Store) LoadHistory(
	ctx context.Context,
	sessionID string,
	limit int,
) ([]llm.ChatMessage, error) {
	// 记录查询开始时间。
	start := time.Now()

	dialogues, err := s.repo.GetRecentDialogues(
		ctx,
		sessionID,
		limit,
	)

	// 计算数据库查询耗时。
	elapsed := time.Since(start)

	if err != nil {
		// 查询失败也要记录埋点，便于看板统计错误率。
		telemetry.Emit(ctx, telemetry.EventMemoryQuery, map[string]any{
			"session_id": sessionID,
			"limit":      limit,
			telemetry.FieldStatus:     telemetry.StatusError,
			telemetry.FieldDurationMS: elapsed.Milliseconds(),
			"error":      err.Error(),
		})

		return nil, fmt.Errorf("load history: %w", err)
	}

	// 把数据库模型转成 LLM 消息。
	// system prompt 由 Agent 单独加，这里不重复返回。
	messages := make(
		[]llm.ChatMessage,
		0,
		len(dialogues),
	)

	for _, dialogue := range dialogues {
		if dialogue.Role == string(llm.RoleSystem) {
			continue
		}

		messages = append(messages, llm.ChatMessage{
			Role:    llm.Role(dialogue.Role),
			Content: dialogue.Content,
		})
	}

	// 查询成功后记录返回数量和耗时。
	telemetry.Emit(ctx, telemetry.EventMemoryQuery, map[string]any{
		"session_id":    sessionID,
		"limit":         limit,
		"rows_returned": len(messages),
		telemetry.FieldStatus:     telemetry.StatusOK,
		telemetry.FieldDurationMS: elapsed.Milliseconds(),
	})

	return messages, nil
}
