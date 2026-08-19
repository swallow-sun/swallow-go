// chat_request_repo.go 放聊天幂等请求的 SQLite 数据访问方法.
//
// 做的事情:
//  1. BeginChatRequest:原子创建幂等请求记录,用 ON CONFLICT DO NOTHING 防重复插入.
//  2. MarkChatRequestRunning:请求开始执行模型后,标记状态为 running 并关联用户消息 ID.
//  3. CompleteChatRequest:助手回复保存后,标记状态为 completed 并关联助手消息 ID.
//  4. FailChatRequest:请求执行失败时,标记状态为 failed 并记录稳定错误码.
//
// 状态流转:accepted → running → completed/failed.
// 失败的请求不会自动重试,需要人工修复后重试.
package data

import (
	"context"
	"fmt"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm/clause"
)

// BeginChatRequest 原子创建幂等请求记录.
// created=false 表示同一会话和 client_message_id 已存在,不能再调一次模型.
// 参数说明:
//   - clientMessageID: 客户端传来的消息 ID,用来防重复
//   - sessionID: 会话 ID
//   - userID: 用户 ID
//   - traceID: 链路追踪 ID
// 返回值:ChatRequest(记录),bool(是否新建成功),error
func (r *sqliteRepo) BeginChatRequest(ctx context.Context, clientMessageID, sessionID string, userID int64, traceID string) (ChatRequest, bool, error) {
	// 构造一条 accepted 状态的记录,准备插进去
	model := ormChatRequest{
		ClientMessageID: clientMessageID,
		SessionID:       sessionID,
		UserID:          userID,
		Status:          ChatRequestStatusAccepted,
		TraceID:         traceID,
	}

	// .Clauses(clause.OnConflict{...}) 加冲突处理子句,相当于 SQL 的 ON CONFLICT
	//   Columns: 指定唯一约束列(session_id + client_message_id 组合唯一)
	//   DoNothing: true 冲突时什么都不做(ON CONFLICT DO NOTHING)
	// .Create(&model) 执行 INSERT
	// 效果:如果这个会话已经有过这个 client_message_id 的请求,就不插,保持原记录
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "session_id"}, {Name: "client_message_id"}},
		DoNothing: true,
	}).Create(&model)

	// .Error 取错误
	if result.Error != nil {
		logger.Error("chat_requests insert failed",
			zap.String("session_id", sessionID),
			zap.String("client_message_id", clientMessageID),
			zap.Int64("user_id", userID),
			zap.String("trace_id", traceID),
			zap.Error(result.Error),
		)
		return ChatRequest{}, false, fmt.Errorf("begin chat request: %w", result.Error)
	}

	// .RowsAffected 返回这次操作实际插入了几行
	// 等于 1 说明插入成功(新建了一条记录),等于 0 说明冲突了(已存在)
	if result.RowsAffected == 1 {
		// 新建成功,把模型转成业务对象返回,created=true
		logger.Debug("chat_requests insert succeeded",
			zap.Any("row", model),
		)
		return chatRequestFromORM(model), true, nil
	}

	// 冲突了,记录已存在,把它查出来返回,created=false 表示"不是新建的"
	if err := r.db.WithContext(ctx).Select(chatRequestColumns).
		Where("session_id = ? AND client_message_id = ?", sessionID, clientMessageID).
		First(&model).Error; err != nil {
		return ChatRequest{}, false, fmt.Errorf("get existing chat request: %w", err)
	}
	return chatRequestFromORM(model), false, nil
}

// MarkChatRequestRunning 关联已保存的用户消息,并把请求切换为执行中.
// 参数 requestID 是请求记录的主键,userDialogueID 是已保存的用户消息 ID.
// 状态流转:accepted → running.
func (r *sqliteRepo) MarkChatRequestRunning(ctx context.Context, requestID int64, userDialogueID int64) error {
	// updates 是要更新的字段,写在 map 里方便后面打日志
	//   status 改成 running
	//   user_dialogue_id 记下用户消息的 ID
	//   error_code 设成 nil(清空之前的错误码,如果有)
	// .Model(&ormChatRequest{}) 指定操作 chat_requests 表
	// .Where("id = ? AND status = ?", ...) 按 ID 找,同时要求当前状态是 accepted
	//   这样做是防呆:万一状态已经被别的流程改了,这里就不会误改
	updates := map[string]any{
		"status":           ChatRequestStatusRunning,
		"user_dialogue_id": userDialogueID,
		"error_code":       nil,
	}
	result := r.db.WithContext(ctx).Model(&ormChatRequest{}).
		Where("id = ? AND status = ?", requestID, ChatRequestStatusAccepted).
		Updates(updates)

	// .Error 取错误
	if result.Error != nil {
		logger.Error("chat_requests update failed → running",
			zap.Int64("request_id", requestID),
			zap.Int64("user_dialogue_id", userDialogueID),
			zap.Error(result.Error),
		)
		return fmt.Errorf("mark chat request %d running: %w", requestID, result.Error)
	}

	// .RowsAffected 检查更新了几行
	// 不等于 1 说明没更新到(可能状态已经不是 accepted 了,或者 ID 不存在)
	// 这种情况报错返回,调用方要处理
	if result.RowsAffected != 1 {
		return fmt.Errorf("mark chat request %d running: invalid current status", requestID)
	}

	return nil
}

// CompleteChatRequest 关联完整助手回复,并把请求切换为已完成.
// 参数 requestID 是请求记录的主键,assistantDialogueID 是已保存的助手消息 ID.
// 状态流转:running → completed.
func (r *sqliteRepo) CompleteChatRequest(ctx context.Context, requestID int64, assistantDialogueID int64) error {
	// 记下完成时间
	completedAt := time.Now()

	// updates 是要更新的字段,写在 map 里方便后面打日志
	//   status 改成 completed
	//   assistant_dialogue_id 记下助手消息的 ID
	//   completed_at 记下完成时间
	//   error_code 设成 nil(清空错误码)
	// .Model(&ormChatRequest{}) 指定操作 chat_requests 表
	// .Where("id = ? AND status = ?", ...) 按 ID 找,同时要求当前状态是 running
	//   防呆:只有 running 状态的才能标记 completed,不能从 accepted 直接跳到 completed
	updates := map[string]any{
		"status":               ChatRequestStatusCompleted,
		"assistant_dialogue_id": assistantDialogueID,
		"completed_at":         completedAt,
		"error_code":           nil,
	}
	result := r.db.WithContext(ctx).Model(&ormChatRequest{}).
		Where("id = ? AND status = ?", requestID, ChatRequestStatusRunning).
		Updates(updates)

	if result.Error != nil {
		logger.Error("chat_requests update failed → completed",
			zap.Int64("request_id", requestID),
			zap.Int64("assistant_dialogue_id", assistantDialogueID),
			zap.Error(result.Error),
		)
		return fmt.Errorf("complete chat request %d: %w", requestID, result.Error)
	}

	// RowsAffected 不等于 1 说明状态已经不是 running(可能已经 completed 或 failed)
	if result.RowsAffected != 1 {
		return fmt.Errorf("complete chat request %d: invalid current status", requestID)
	}

	return nil
}

// FailChatRequest 记录稳定错误码;失败请求不会被同一幂等键自动重新执行.
// 参数 requestID 是请求记录的主键,errorCode 是稳定的错误码(不是详细的错误信息).
// 状态流转:accepted/running → failed.
func (r *sqliteRepo) FailChatRequest(ctx context.Context, requestID int64, errorCode string) error {
	// updates 是要更新的字段,写在 map 里方便后面打日志
	//   status 改成 failed
	//   error_code 记下错误码
	// .Model(&ormChatRequest{}) 指定操作 chat_requests 表
	// .Where("id = ? AND status IN ?", ...) 按 ID 找
	//   status IN (...) 表示当前状态是 accepted 或 running 才能改成 failed
	//   已经 completed 或已经 failed 的不能再改成 failed(幂等保护)
	updates := map[string]any{"status": ChatRequestStatusFailed, "error_code": errorCode}
	result := r.db.WithContext(ctx).Model(&ormChatRequest{}).
		Where("id = ? AND status IN ?", requestID, []string{ChatRequestStatusAccepted, ChatRequestStatusRunning}).
		Updates(updates)

	if result.Error != nil {
		logger.Error("chat_requests update failed → failed",
			zap.Int64("request_id", requestID),
			zap.String("error_code", errorCode),
			zap.Error(result.Error),
		)
		return fmt.Errorf("fail chat request %d: %w", requestID, result.Error)
	}

	// RowsAffected 不等于 1 说明状态不在 accepted/running 里(可能已经 completed 或 failed)
	if result.RowsAffected != 1 {
		return fmt.Errorf("fail chat request %d: invalid current status", requestID)
	}

	return nil
}
