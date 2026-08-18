// types.go 放 data 层共用的类型定义。
//
// 做的事情：
//  1. 定义 Repository 接口：数据访问层的统一接口，抽象所有数据库操作，换数据库只需换实现不改业务代码。
//  2. 定义业务实体结构体：User、Session、Dialogue、ChatRequest、Event、AppSetting、EncryptedSecret 等。
//  3. 定义 ORM 模型结构体：ormUser、ormSession、ormDialogue 等，带 GORM 标签映射数据库表。
//  4. 定义状态常量：迁移状态（running/completed/failed）和聊天请求状态（accepted/running/completed/failed）。
//  5. 定义迁移相关结构体：Migration（磁盘迁移文件）和 MigrationRecord（schema_migrations 表记录）。
package data

import (
	"context"
	"time"

	"gorm.io/gorm"
)

const (
	// MigrationStatusRunning 表示迁移已经登记，正在执行。
	MigrationStatusRunning = "running"
	// MigrationStatusCompleted 表示迁移已经成功提交。
	MigrationStatusCompleted = "completed"
	// MigrationStatusFailed 表示迁移执行失败，数据库结构事务已回滚。
	MigrationStatusFailed = "failed"

	// ChatRequestStatusAccepted 表示请求已经接收，但尚未调用模型。
	ChatRequestStatusAccepted = "accepted"
	// ChatRequestStatusRunning 表示模型调用或响应读取正在进行。
	ChatRequestStatusRunning = "running"
	// ChatRequestStatusCompleted 表示助手回复已经完整保存。
	ChatRequestStatusCompleted = "completed"
	// ChatRequestStatusFailed 表示请求已经失败，同一幂等键不会自动重复计费。
	ChatRequestStatusFailed = "failed"
)

// Repository 是数据访问层的统一接口。
type Repository interface {
	CreateUser(ctx context.Context, name, role string) (User, error)
	GetUser(ctx context.Context, id int64) (User, error)
	GetUserByName(ctx context.Context, name string) (User, error)
	UpdateUserActive(ctx context.Context, id int64) error
	CreateSession(ctx context.Context, sessionID string, userID int64) (Session, error)
	GetSession(ctx context.Context, sessionID string) (Session, error)
	UpdateSessionActive(ctx context.Context, sessionID string) error
	InsertDialogue(ctx context.Context, sessionID string, userID int64, role, content string, usage TokenUsage, traceID string) (Dialogue, error)
	GetDialogue(ctx context.Context, id int64) (Dialogue, error)
	GetDialogueByTraceAndRole(ctx context.Context, traceID, role string) (Dialogue, error)
	GetRecentDialogues(ctx context.Context, sessionID string, limit int) ([]Dialogue, error)
	BeginChatRequest(ctx context.Context, clientMessageID, sessionID string, userID int64, traceID string) (ChatRequest, bool, error)
	MarkChatRequestRunning(ctx context.Context, requestID int64, userDialogueID int64) error
	CompleteChatRequest(ctx context.Context, requestID int64, assistantDialogueID int64) error
	FailChatRequest(ctx context.Context, requestID int64, errorCode string) error
	GetAppSetting(ctx context.Context, key string) (AppSetting, error)
	CreateAppSettingIfAbsent(ctx context.Context, setting AppSetting) (bool, error)
	UpsertAppSetting(ctx context.Context, setting AppSetting) error
	DeleteAppSetting(ctx context.Context, key string) error
	GetEncryptedSecret(ctx context.Context, key string) (EncryptedSecret, error)
	CreateEncryptedSecretIfAbsent(ctx context.Context, secret EncryptedSecret) (bool, error)
	UpsertEncryptedSecret(ctx context.Context, secret EncryptedSecret) error
	DeleteEncryptedSecret(ctx context.Context, key string) error
	InsertEvent(ctx context.Context, eventType string, userID *int64, data string, durationMs int64, success bool, traceID string) error
	Close() error
}

type EventSinkAdapter struct {
	Repo Repository
}

type User struct {
	ID           int64
	Name         string
	Role         string
	VoicePrint   string
	FacePrint    string
	CreatedAt    time.Time
	LastActiveAt time.Time
}

type Session struct {
	ID           string
	UserID       int64
	StartedAt    time.Time
	LastActiveAt time.Time
	Status       string
}

type Dialogue struct {
	ID               int64
	SessionID        string
	UserID           int64
	Role             string
	Content          string
	PromptTokens     int
	CompletionTokens int
	CacheHitTokens   int
	CacheMissTokens  int
	ReasoningTokens  int
	TotalTokens      int
	TraceID          string
	Timestamp        time.Time
}

type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	CacheHitTokens   int
	CacheMissTokens  int
	ReasoningTokens  int
	TotalTokens      int
}

type Event struct {
	ID         int64
	EventType  string
	UserID     *int64
	EventData  string
	TraceID    string
	Timestamp  time.Time
	DurationMs int64
	Success    bool
}

// ChatRequest 表示一轮客户端消息的幂等执行状态。
type ChatRequest struct {
	ID                  int64
	ClientMessageID     string
	SessionID           string
	UserID              int64
	Status              string
	UserDialogueID      *int64
	AssistantDialogueID *int64
	ErrorCode           *string
	TraceID             string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	CompletedAt         *time.Time
}

// AppSetting 是可以明文保存的普通运行配置。
type AppSetting struct {
	Key         string
	Value       string
	ValueType   string
	Description string
	UpdatedAt   time.Time
}

// EncryptedSecret 是只允许以密文形式保存的敏感配置。
type EncryptedSecret struct {
	Key        string
	Ciphertext []byte
	Nonce      []byte
	Algorithm  string
	KeyVersion int
	UpdatedAt  time.Time
}

type ormUser struct {
	ID           int64  `gorm:"primaryKey;autoIncrement"`
	Name         string `gorm:"not null"`
	Role         string `gorm:"default:owner"`
	VoicePrint   *string
	FacePrint    *string
	CreatedAt    time.Time `gorm:"autoCreateTime"`
	LastActiveAt time.Time `gorm:"autoCreateTime"`
}

type ormSession struct {
	ID           string    `gorm:"primaryKey"`
	UserID       int64     `gorm:"not null"`
	StartedAt    time.Time `gorm:"autoCreateTime"`
	LastActiveAt time.Time `gorm:"autoCreateTime"`
	Status       string    `gorm:"default:active"`
}

type ormDialogue struct {
	ID               int64     `gorm:"primaryKey;autoIncrement;index:idx_dialogues_session_time,sort:desc,priority:3"`
	SessionID        string    `gorm:"not null;index:idx_dialogues_session_time,priority:1"`
	UserID           int64     `gorm:"not null"`
	Role             string    `gorm:"not null"`
	Content          string    `gorm:"not null"`
	PromptTokens     int       `gorm:"default:0"`
	CompletionTokens int       `gorm:"default:0"`
	CacheHitTokens   int       `gorm:"default:0"`
	CacheMissTokens  int       `gorm:"default:0"`
	ReasoningTokens  int       `gorm:"default:0"`
	TotalTokens      int       `gorm:"default:0"`
	TraceID          string    `gorm:"index:idx_dialogues_trace"`
	Timestamp        time.Time `gorm:"autoCreateTime;index:idx_dialogues_session_time,sort:desc,priority:2"`
}

type ormEvent struct {
	ID         int64  `gorm:"primaryKey;autoIncrement"`
	EventType  string `gorm:"not null;index:idx_events_type_time,priority:1"`
	UserID     *int64
	EventData  string
	TraceID    string    `gorm:"index:idx_events_trace"`
	Timestamp  time.Time `gorm:"autoCreateTime;index:idx_events_type_time,sort:desc,priority:2"`
	DurationMs int64
	Success    *bool `gorm:"default:true"`
}

type ormChatRequest struct {
	ID                  int64  `gorm:"primaryKey;autoIncrement"`
	ClientMessageID     string `gorm:"not null;uniqueIndex:idx_chat_requests_session_client,priority:2"`
	SessionID           string `gorm:"not null;uniqueIndex:idx_chat_requests_session_client,priority:1"`
	UserID              int64  `gorm:"not null"`
	Status              string `gorm:"not null;index:idx_chat_requests_status_updated,priority:1"`
	UserDialogueID      *int64
	AssistantDialogueID *int64
	ErrorCode           *string
	TraceID             string    `gorm:"not null;index:idx_chat_requests_trace"`
	CreatedAt           time.Time `gorm:"autoCreateTime"`
	UpdatedAt           time.Time `gorm:"autoUpdateTime;index:idx_chat_requests_status_updated,sort:desc,priority:2"`
	CompletedAt         *time.Time
}

type ormAppSetting struct {
	Key         string `gorm:"column:setting_key;primaryKey"`
	Value       string `gorm:"column:setting_value;not null"`
	ValueType   string `gorm:"not null"`
	Description string
	UpdatedAt   time.Time `gorm:"autoUpdateTime"`
}

type ormEncryptedSecret struct {
	Key        string    `gorm:"column:secret_key;primaryKey"`
	Ciphertext []byte    `gorm:"not null"`
	Nonce      []byte    `gorm:"not null"`
	Algorithm  string    `gorm:"not null"`
	KeyVersion int       `gorm:"not null"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime"`
}

type sqliteRepo struct {
	db *gorm.DB
}

// Migration 描述从磁盘加载的一份版本化 SQL 迁移文件。
type Migration struct {
	// 版本号，如 1
	Version int
	// 名称，如 "init"
	Name string
	// 文件完整路径，如 "script/migrations/0001_init.sql"
	Path string
	// 文件内容的 SHA-256 指纹，64 字符十六进制字符串
	Checksum string
	// 文件的完整 SQL 文本
	SQL string
}

// MigrationRecord 是 schema_migrations 表中的迁移执行记录。
// 每次执行迁移文件前插一条 running 记录，执行完更新为 completed 或 failed。
// 下次启动时查这张表，知道哪些版本执行过了、成功没有。
type MigrationRecord struct {
	Version      int        `gorm:"primaryKey"` // 版本号，主键，如 1、2
	Name         string     `gorm:"not null"`   // 迁移名称，如 "init"
	Checksum     string     `gorm:"not null"`   // SQL 文件的 SHA-256 指纹，用于检测已执行的迁移文件是否被篡改
	Status       string     `gorm:"not null"`   // 执行状态：running / completed / failed
	StartedAt    time.Time  `gorm:"not null"`   // 开始执行时间
	CompletedAt  *time.Time // 完成时间，指针类型：running 状态时为 NULL，completed 时才有值
	ErrorMessage *string    // 失败时的错误信息，指针类型：成功时为 NULL，失败时才有值
}
