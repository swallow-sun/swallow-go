package data

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm/clause"
)

// BeginChatRequest 原子创建幂等请求记录。
// created=false 表示同一会话和 client_message_id 已存在，调用方不得再次调用模型。
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
