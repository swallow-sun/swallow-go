// sqlite_repo.go 用 GORM 实现 Repository 接口定义的 SQLite 数据访问方法。
//
// 做的事情：
//  1. 实现 Repository 接口的全部方法：用户 CRUD、会话 CRUD、对话 CRUD、事件插入。
//  2. 显式指定 ORM 模型对应的表名（不靠 GORM 默认的复数命名规则）。
//  3. 提供 ORM 模型和业务实体之间的转换函数（xxxFromORM / xxxToORM）。
//  4. 提供 repositoryError 辅助函数：把 GORM 的 ErrRecordNotFound 转成 sql.ErrNoRows。
package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 显式指定 ORM 模型对应的数据库表名，不靠 GORM 默认的复数命名规则。
func (ormUser) TableName() string { return "users" }           // users 表：注册用户
func (ormSession) TableName() string { return "sessions" }     // sessions 表：聊天会话
func (ormDialogue) TableName() string { return "dialogues" }   // dialogues 表：对话消息
func (ormEvent) TableName() string { return "events" }         // events 表：会话事件
func (ormChatRequest) TableName() string { return "chat_requests" } // chat_requests 表：聊天请求记录
func (ormAppSetting) TableName() string { return "app_settings" }   // app_settings 表：运行配置
func (ormEncryptedSecret) TableName() string { return "encrypted_secrets" } // encrypted_secrets 表：加密密钥

// CreateUser 新增用户，并返回数据库生成的 ID 和时间字段。
func (r *sqliteRepo) CreateUser(ctx context.Context, name, role string) (User, error) {
	model := ormUser{Name: name, Role: role}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	return userFromORM(model), nil
}

// GetUser 按用户 ID 查询用户。
func (r *sqliteRepo) GetUser(ctx context.Context, id int64) (User, error) {
	var model ormUser
	if err := r.db.WithContext(ctx).First(&model, id).Error; err != nil {
		return User{}, fmt.Errorf("get user %d: %w", id, repositoryError(err))
	}
	return userFromORM(model), nil
}

// GetUserByName 按用户名查询第一条匹配的用户记录。
func (r *sqliteRepo) GetUserByName(ctx context.Context, name string) (User, error) {
	var model ormUser
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&model).Error; err != nil {
		return User{}, fmt.Errorf("get user by name %q: %w", name, repositoryError(err))
	}
	return userFromORM(model), nil
}

// UpdateUserActive 将用户最后活跃时间更新为当前时间。
func (r *sqliteRepo) UpdateUserActive(ctx context.Context, id int64) error {
	if err := r.db.WithContext(ctx).Model(&ormUser{}).Where("id = ?", id).Update("last_active_at", time.Now()).Error; err != nil {
		return fmt.Errorf("update user active: %w", err)
	}
	return nil
}

// CreateSession 为用户新增会话，并返回完整会话记录。
func (r *sqliteRepo) CreateSession(ctx context.Context, sessionID string, userID int64) (Session, error) {
	model := ormSession{ID: sessionID, UserID: userID, Status: "active"}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return Session{}, fmt.Errorf("insert session: %w", err)
	}
	return sessionFromORM(model), nil
}

// GetSession 按 session ID 查询会话。
func (r *sqliteRepo) GetSession(ctx context.Context, sessionID string) (Session, error) {
	var model ormSession
	if err := r.db.WithContext(ctx).First(&model, "id = ?", sessionID).Error; err != nil {
		return Session{}, fmt.Errorf("get session %s: %w", sessionID, repositoryError(err))
	}
	return sessionFromORM(model), nil
}

// UpdateSessionActive 将会话最后活跃时间更新为当前时间。
func (r *sqliteRepo) UpdateSessionActive(ctx context.Context, sessionID string) error {
	if err := r.db.WithContext(ctx).Model(&ormSession{}).Where("id = ?", sessionID).Update("last_active_at", time.Now()).Error; err != nil {
		return fmt.Errorf("update session active: %w", err)
	}
	return nil
}

// InsertDialogue 保存一条对话消息，并返回包含自增 ID 和时间的领域对象。
func (r *sqliteRepo) InsertDialogue(ctx context.Context, sessionID string, userID int64, role, content string, usage TokenUsage, traceID string) (Dialogue, error) {
	model := ormDialogue{
		SessionID: sessionID, UserID: userID, Role: role, Content: content,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens, CacheHitTokens: usage.CacheHitTokens,
		CacheMissTokens: usage.CacheMissTokens, ReasoningTokens: usage.ReasoningTokens,
		TotalTokens: usage.TotalTokens, TraceID: traceID,
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return Dialogue{}, fmt.Errorf("insert dialogue: %w", err)
	}
	return dialogueFromORM(model), nil
}

// GetDialogue 按主键读取一条对话，用于幂等重试时返回已经完成的结果。
func (r *sqliteRepo) GetDialogue(ctx context.Context, id int64) (Dialogue, error) {
	var model ormDialogue
	if err := r.db.WithContext(ctx).First(&model, id).Error; err != nil {
		return Dialogue{}, fmt.Errorf("get dialogue %d: %w", id, repositoryError(err))
	}
	return dialogueFromORM(model), nil
}

// GetDialogueByTraceAndRole 查询某次请求保存的指定角色消息。
func (r *sqliteRepo) GetDialogueByTraceAndRole(ctx context.Context, traceID, role string) (Dialogue, error) {
	var model ormDialogue
	if err := r.db.WithContext(ctx).
		Where("trace_id = ? AND role = ?", traceID, role).
		Order("id DESC").First(&model).Error; err != nil {
		return Dialogue{}, fmt.Errorf("get dialogue by trace %s and role %s: %w", traceID, role, repositoryError(err))
	}
	return dialogueFromORM(model), nil
}

// GetRecentDialogues 查询最近 limit 条消息，并按从旧到新的顺序返回。
func (r *sqliteRepo) GetRecentDialogues(ctx context.Context, sessionID string, limit int) ([]Dialogue, error) {
	var models []ormDialogue
	// 同一时间戳内按自增 ID 倒序，保证高频连续写入时顺序仍然稳定。
	if err := r.db.WithContext(ctx).Where("session_id = ?", sessionID).
		Order("timestamp DESC").Order("id DESC").Limit(limit).Find(&models).Error; err != nil {
		return nil, fmt.Errorf("query dialogues: %w", err)
	}
	result := make([]Dialogue, len(models))
	for i := range models {
		result[len(models)-1-i] = dialogueFromORM(models[i])
	}
	return result, nil
}

// InsertEvent 保存一条埋点事件。
func (r *sqliteRepo) InsertEvent(ctx context.Context, eventType string, userID *int64, data string, durationMs int64, success bool, traceID string) error {
	model := ormEvent{EventType: eventType, UserID: userID, EventData: data, TraceID: traceID, DurationMs: durationMs, Success: &success}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

// Close 关闭 GORM 使用的底层 sql.DB 连接池。
func (r *sqliteRepo) Close() error {
	sqlDB, err := r.db.DB()
	if err != nil {
		return fmt.Errorf("get sqlite connection pool: %w", err)
	}
	return sqlDB.Close()
}

// repositoryError 保持 Repository 原有的 sql.ErrNoRows 错误约定。
func repositoryError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return sql.ErrNoRows
	}
	return err
}

// 以下 fromORM 函数把 GORM ORM 模型转成业务对象（去掉 GORM tag，只留业务字段）。

func userFromORM(model ormUser) User {
	return User{ID: model.ID, Name: model.Name, Role: model.Role, VoicePrint: stringValue(model.VoicePrint), FacePrint: stringValue(model.FacePrint), CreatedAt: model.CreatedAt, LastActiveAt: model.LastActiveAt}
}

func sessionFromORM(model ormSession) Session {
	return Session{ID: model.ID, UserID: model.UserID, StartedAt: model.StartedAt, LastActiveAt: model.LastActiveAt, Status: model.Status}
}

func dialogueFromORM(model ormDialogue) Dialogue {
	return Dialogue{
		ID: model.ID, SessionID: model.SessionID, UserID: model.UserID, Role: model.Role, Content: model.Content,
		PromptTokens: model.PromptTokens, CompletionTokens: model.CompletionTokens,
		CacheHitTokens: model.CacheHitTokens, CacheMissTokens: model.CacheMissTokens,
		ReasoningTokens: model.ReasoningTokens, TotalTokens: model.TotalTokens,
		TraceID: model.TraceID, Timestamp: model.Timestamp,
	}
}

func chatRequestFromORM(model ormChatRequest) ChatRequest {
	return ChatRequest{
		ID: model.ID, ClientMessageID: model.ClientMessageID,
		SessionID: model.SessionID, UserID: model.UserID, Status: model.Status,
		UserDialogueID: model.UserDialogueID, AssistantDialogueID: model.AssistantDialogueID,
		ErrorCode: model.ErrorCode, TraceID: model.TraceID,
		CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt, CompletedAt: model.CompletedAt,
	}
}

// stringValue 把可空 *string 转成 string，nil 返回空字符串。
// 用于 voice_print、face_print 等可空字段。
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
