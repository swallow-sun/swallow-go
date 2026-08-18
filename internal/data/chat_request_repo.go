// chat_request_repo.go 放聊天幂等请求的 SQLite 数据访问方法。
//
// 做的事情：
//  1. BeginChatRequest：原子创建幂等请求记录，用 ON CONFLICT DO NOTHING 防重复插入。
//  2. MarkChatRequestRunning：请求开始执行模型后，标记状态为 running 并关联用户消息 ID。
//  3. CompleteChatRequest：助手回复保存后，标记状态为 completed 并关联助手消息 ID。
//  4. FailChatRequest：请求执行失败时，标记状态为 failed 并记录稳定错误码。
//
// 状态流转：accepted → running → completed/failed。
// 失败的请求不会自动重试，需要人工修复后重试。
package data

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm/clause"
)

// BeginChatRequest 原子创建幂等请求记录。
// created=false 表示同一会话和 client_message_id 已存在，不能再调一次模型。
func (r *sqliteRepo) BeginChatRequest(ctx context.Context, clientMessageID, sessionID string, userID int64, traceID string) (ChatRequest, bool, error) {
	model := ormChatRequest{
		ClientMessageID: clientMessageID,
		SessionID:       sessionID,
		UserID:          userID,
		Status:          ChatRequestStatusAccepted,
		TraceID:         traceID,
	}
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "session_id"}, {Name: "client_message_id"}},
		DoNothing: true,
	}).Create(&model)
	if result.Error != nil {
		return ChatRequest{}, false, fmt.Errorf("begin chat request: %w", result.Error)
	}
	if result.RowsAffected == 1 {
		return chatRequestFromORM(model), true, nil
	}

	if err := r.db.WithContext(ctx).
		Where("session_id = ? AND client_message_id = ?", sessionID, clientMessageID).
		First(&model).Error; err != nil {
		return ChatRequest{}, false, fmt.Errorf("get existing chat request: %w", err)
	}
	return chatRequestFromORM(model), false, nil
}

// MarkChatRequestRunning 关联已保存的用户消息，并把请求切换为执行中。
func (r *sqliteRepo) MarkChatRequestRunning(ctx context.Context, requestID int64, userDialogueID int64) error {
	result := r.db.WithContext(ctx).Model(&ormChatRequest{}).
		Where("id = ? AND status = ?", requestID, ChatRequestStatusAccepted).
		Updates(map[string]any{
			"status": ChatRequestStatusRunning, "user_dialogue_id": userDialogueID,
			"error_code": nil,
		})
	if result.Error != nil {
		return fmt.Errorf("mark chat request %d running: %w", requestID, result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("mark chat request %d running: invalid current status", requestID)
	}
	return nil
}

// CompleteChatRequest 关联完整助手回复，并把请求切换为已完成。
func (r *sqliteRepo) CompleteChatRequest(ctx context.Context, requestID int64, assistantDialogueID int64) error {
	completedAt := time.Now()
	result := r.db.WithContext(ctx).Model(&ormChatRequest{}).
		Where("id = ? AND status = ?", requestID, ChatRequestStatusRunning).
		Updates(map[string]any{
			"status": ChatRequestStatusCompleted, "assistant_dialogue_id": assistantDialogueID,
			"completed_at": completedAt, "error_code": nil,
		})
	if result.Error != nil {
		return fmt.Errorf("complete chat request %d: %w", requestID, result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("complete chat request %d: invalid current status", requestID)
	}
	return nil
}

// FailChatRequest 记录稳定错误码；失败请求不会被同一幂等键自动重新执行。
func (r *sqliteRepo) FailChatRequest(ctx context.Context, requestID int64, errorCode string) error {
	result := r.db.WithContext(ctx).Model(&ormChatRequest{}).
		Where("id = ? AND status IN ?", requestID, []string{ChatRequestStatusAccepted, ChatRequestStatusRunning}).
		Updates(map[string]any{"status": ChatRequestStatusFailed, "error_code": errorCode})
	if result.Error != nil {
		return fmt.Errorf("fail chat request %d: %w", requestID, result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("fail chat request %d: invalid current status", requestID)
	}
	return nil
}
