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
	GetRecentDialogues(ctx context.Context, sessionID string, limit int) ([]Dialogue, error)
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

type sqliteRepo struct {
	db *gorm.DB
}

// Migration 描述从磁盘加载的一份版本化 SQL 迁移文件。
type Migration struct {
	Version  int
	Name     string
	Path     string
	Checksum string
	SQL      string
}

// MigrationRecord 是 schema_migrations 表中的迁移执行记录。
type MigrationRecord struct {
	Version      int       `gorm:"primaryKey"`
	Name         string    `gorm:"not null"`
	Checksum     string    `gorm:"not null"`
	Status       string    `gorm:"not null"`
	StartedAt    time.Time `gorm:"not null"`
	CompletedAt  *time.Time
	ErrorMessage *string
}
