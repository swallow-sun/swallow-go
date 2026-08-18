// types.go 放 data 层共用的类型定义.
//
// 做的事情:
//  1. 定义 Repository 接口:数据访问层的统一接口,抽象所有数据库操作,换数据库只需换实现不改业务代码.
//  2. 定义业务实体结构体:User,Session,Dialogue,ChatRequest,Event,AppSetting,EncryptedSecret 等.
//  3. 定义 ORM 模型结构体:ormUser,ormSession,ormDialogue 等,带 GORM 标签映射数据库表.
//  4. 定义状态常量:迁移状态(running/completed/failed)和聊天请求状态(accepted/running/completed/failed).
//  5. 定义各表列名常量:SELECT 查询用显式列名,不用 SELECT *.
//  6. 定义迁移相关结构体:Migration(磁盘迁移文件)和 MigrationRecord(schema_migrations 表记录).
//  7. 定义 Span ORM 模型:ormSpan 对应 spans 表,记录一次请求经过的每个处理步骤.
package data

import (
	"context"
	"time"

	"gorm.io/gorm"
)

const (
	// MigrationStatusRunning 表示迁移已经登记,正在执行.
	// 迁移开始时先插一条 running 记录,执行完再更新成 completed 或 failed.
	MigrationStatusRunning = "running"
	// MigrationStatusCompleted 表示迁移已经成功提交.
	// 事务提交成功后把状态从 running 更新成 completed.
	MigrationStatusCompleted = "completed"
	// MigrationStatusFailed 表示迁移执行失败,数据库结构事务已回滚.
	// 失败的迁移不会自动重试,需要人工修复后再重新执行.
	MigrationStatusFailed = "failed"

	// ChatRequestStatusAccepted 表示请求已经接收,但尚未调用模型.
	// BeginChatRequest 插入记录时的初始状态.
	ChatRequestStatusAccepted = "accepted"
	// ChatRequestStatusRunning 表示模型调用或响应读取正在进行.
	// MarkChatRequestRunning 把状态从 accepted 切到 running.
	ChatRequestStatusRunning = "running"
	// ChatRequestStatusCompleted 表示助手回复已经完整保存.
	// CompleteChatRequest 把状态从 running 切到 completed.
	ChatRequestStatusCompleted = "completed"
	// ChatRequestStatusFailed 表示请求已经失败,同一幂等键不会自动重复计费.
	// FailChatRequest 把状态从 accepted/running 切到 failed.
	ChatRequestStatusFailed = "failed"

	// 下面是 model_usages 表的操作类型和状态常量.
	// operation 字段区分这次模型调用是干什么的:聊天,嵌入,视觉,语音识别还是语音合成.
	ModelOperationChat     = "chat"      // 文字对话
	ModelOperationEmbedding = "embedding" // 向量嵌入
	ModelOperationVision   = "vision"    // 视觉理解
	ModelOperationASR      = "asr"       // 语音识别(Automatic Speech Recognition)
	ModelOperationTTS      = "tts"       // 语音合成(Text To Speech)

	// status 字段标记这次模型调用成功还是失败.
	// 和 ChatRequest 的状态不同,model_usages 只记最终结果,不记中间状态.
	ModelUsageStatusOK     = "ok"     // 模型调用成功
	ModelUsageStatusFailed = "failed" // 模型调用失败
)

// Repository 是数据访问层的统一接口.
// 所有数据库操作都通过这个接口暴露,业务代码只依赖接口,不依赖具体实现.
// 好处:换数据库(SQLite → MySQL)只需要换实现,业务代码不用改.
//
// 方法分组:
//   - 用户:CreateUser,GetUser,GetUserByName,UpdateUserActive
//   - 会话:CreateSession,GetSession,UpdateSessionActive
//   - 对话:InsertDialogue,GetDialogue,GetDialogueByTraceAndRole,GetRecentDialogues
//   - 聊天幂等:BeginChatRequest,MarkChatRequestRunning,CompleteChatRequest,FailChatRequest
//   - 模型用量:InsertModelUsage
//   - 普通配置:GetAppSetting,CreateAppSettingIfAbsent,UpsertAppSetting,DeleteAppSetting
//   - 加密密钥:GetEncryptedSecret,CreateEncryptedSecretIfAbsent,UpsertEncryptedSecret,DeleteEncryptedSecret
//   - 事件埋点:InsertEvent
//   - 连接管理:Close
type Repository interface {
	// CreateUser 新增用户,返回数据库生成的 ID 和时间字段.
	CreateUser(ctx context.Context, name, role string) (User, error)
	// GetUser 按用户 ID 查询用户.
	GetUser(ctx context.Context, id int64) (User, error)
	// GetUserByName 按用户名查询用户.
	GetUserByName(ctx context.Context, name string) (User, error)
	// UpdateUserActive 更新用户最后活跃时间.
	UpdateUserActive(ctx context.Context, id int64) error

	// CreateSession 新建会话.
	CreateSession(ctx context.Context, sessionID string, userID int64) (Session, error)
	// GetSession 按会话 ID 查询会话.
	GetSession(ctx context.Context, sessionID string) (Session, error)
	// UpdateSessionActive 更新会话最后活跃时间.
	UpdateSessionActive(ctx context.Context, sessionID string) error

	// InsertDialogue 保存一条对话消息.
	InsertDialogue(ctx context.Context, sessionID string, userID int64, role, content string, usage TokenUsage, traceID string) (Dialogue, error)
	// GetDialogue 按主键查一条对话消息.
	GetDialogue(ctx context.Context, id int64) (Dialogue, error)
	// GetDialogueByTraceAndRole 按 traceID 和角色查一条对话消息.
	GetDialogueByTraceAndRole(ctx context.Context, traceID, role string) (Dialogue, error)
	// GetRecentDialogues 查最近 N 条对话消息,按从旧到新返回.
	GetRecentDialogues(ctx context.Context, sessionID string, limit int) ([]Dialogue, error)

	// BeginChatRequest 原子创建幂等请求记录,返回是否新建成功.
	BeginChatRequest(ctx context.Context, clientMessageID, sessionID string, userID int64, traceID string) (ChatRequest, bool, error)
	// MarkChatRequestRunning 标记请求开始执行,关联用户消息 ID.
	MarkChatRequestRunning(ctx context.Context, requestID int64, userDialogueID int64) error
	// CompleteChatRequest 标记请求已完成,关联助手消息 ID.
	CompleteChatRequest(ctx context.Context, requestID int64, assistantDialogueID int64) error
	// FailChatRequest 标记请求失败,记录错误码.
	FailChatRequest(ctx context.Context, requestID int64, errorCode string) error

	// InsertModelUsage 保存一条模型调用的 Token 用量和费用估算记录.
	// 每次成功或失败的模型调用都写一条,独立于 dialogues 表,
	// 方便按供应商,模型,操作类型聚合统计成本.
	InsertModelUsage(ctx context.Context, usage ModelUsage) error

	// GetAppSetting 按键名读普通配置.
	GetAppSetting(ctx context.Context, key string) (AppSetting, error)
	// CreateAppSettingIfAbsent 只在配置不存在时写入默认值.
	CreateAppSettingIfAbsent(ctx context.Context, setting AppSetting) (bool, error)
	// UpsertAppSetting 创建或更新普通配置.
	UpsertAppSetting(ctx context.Context, setting AppSetting) error
	// DeleteAppSetting 删除普通配置.
	DeleteAppSetting(ctx context.Context, key string) error

	// GetEncryptedSecret 按键名读密文配置.
	GetEncryptedSecret(ctx context.Context, key string) (EncryptedSecret, error)
	// CreateEncryptedSecretIfAbsent 只在密钥不存在时保存首次加密结果.
	CreateEncryptedSecretIfAbsent(ctx context.Context, secret EncryptedSecret) (bool, error)
	// UpsertEncryptedSecret 创建或更新密文配置.
	UpsertEncryptedSecret(ctx context.Context, secret EncryptedSecret) error
	// DeleteEncryptedSecret 删除密文配置.
	DeleteEncryptedSecret(ctx context.Context, key string) error

	// InsertEvent 保存一条埋点事件.
	InsertEvent(ctx context.Context, eventType string, userID *int64, data string, durationMs int64, success bool, traceID string) error

	// InsertSpan 保存一条 Span 记录,记录请求经过的一个处理步骤.
	// 多个 Span 共享同一个 trace_id,通过 parent_span_id 组成调用链树.
	InsertSpan(ctx context.Context, span Span) error

	// Close 关闭数据库连接.
	Close() error
}

// EventSinkAdapter 把 Repository 适配成 telemetry.EventSink 接口.
// telemetry 模块只认 EventSink 接口,不直接依赖 Repository;
// 这个适配器把 telemetry 传来的事件转成 Repository 能接受的格式.
type EventSinkAdapter struct {
	// Repo 是持有的 Repository 实例,所有事件最终都通过它存进数据库
	Repo Repository
}

// User 是用户业务对象(不依赖 GORM,给业务层用的).
// ORM 模型 ormUser 和这个一一对应,通过 userFromORM 转换.
type User struct {
	// ID 是数据库自增主键
	ID int64
	// Name 是用户名
	Name string
	// Role 是角色,比如 "owner"(拥有者)
	Role string
	// VoicePrint 是声纹特征,空字符串表示还没录
	VoicePrint string
	// FacePrint 是人脸特征,空字符串表示还没录
	FacePrint string
	// CreatedAt 是注册时间
	CreatedAt time.Time
	// LastActiveAt 是最后活跃时间
	LastActiveAt time.Time
}

// Session 是会话业务对象.
// 一个会话就是一轮连续的对话(可能包含多条消息).
type Session struct {
	// ID 是会话标识,字符串类型,外部生成的(比如 UUID)
	ID string
	// UserID 是这个会话属于哪个用户
	UserID int64
	// StartedAt 是会话创建时间
	StartedAt time.Time
	// LastActiveAt 是会话最后活跃时间
	LastActiveAt time.Time
	// Status 是会话状态,比如 "active"(活跃),"closed"(关闭)
	Status string
}

// Dialogue 是一条对话消息业务对象.
// 每轮对话里的每一条消息(用户消息或助手消息)都是一条 Dialogue.
type Dialogue struct {
	// ID 是数据库自增主键
	ID int64
	// SessionID 是这条消息属于哪个会话
	SessionID string
	// UserID 是哪个用户发的(助手消息也记下用户 ID 方便关联)
	UserID int64
	// Role 是角色,"user"(用户)或 "assistant"(助手)
	Role string
	// Content 是消息内容
	Content string
	// PromptTokens 是输入 token 数(发给模型的部分)
	PromptTokens int
	// CompletionTokens 是输出 token 数(模型生成的部分)
	CompletionTokens int
	// CacheHitTokens 是缓存命中的 token 数(省钱用的)
	CacheHitTokens int
	// CacheMissTokens 是缓存未命中的 token 数
	CacheMissTokens int
	// ReasoningTokens 是推理 token 数(模型思考过程消耗的)
	ReasoningTokens int
	// TotalTokens 是总 token 数
	TotalTokens int
	// TraceID 是链路追踪 ID,方便把一次请求的多条消息串起来
	TraceID string
	// Timestamp 是消息保存时间
	Timestamp time.Time
}

// TokenUsage 是 token 用量统计,发给模型时和收到回复时都会用到.
// 字段和 Dialogue 里的 token 字段一一对应.
type TokenUsage struct {
	// PromptTokens 输入 token 数
	PromptTokens int
	// CompletionTokens 输出 token 数
	CompletionTokens int
	// CacheHitTokens 缓存命中 token 数
	CacheHitTokens int
	// CacheMissTokens 缓存未命中 token 数
	CacheMissTokens int
	// ReasoningTokens 推理 token 数
	ReasoningTokens int
	// TotalTokens 总 token 数
	TotalTokens int
}

// Event 是埋点事件业务对象.
// 记录系统里发生的事情,比如聊天请求,模型调用等,用于分析和排查.
type Event struct {
	// ID 数据库自增主键
	ID int64
	// EventType 事件类型,比如 "chat_request","llm_call"
	EventType string
	// UserID 关联的用户 ID,nil 表示系统级事件(不关联具体用户)
	UserID *int64
	// EventData 事件数据,一般是 JSON 字符串
	EventData string
	// TraceID 链路追踪 ID
	TraceID string
	// Timestamp 事件发生时间
	Timestamp time.Time
	// DurationMs 耗时毫秒数
	DurationMs int64
	// Success 这次操作成功没有
	Success bool
}

// ChatRequest 表示一轮客户端消息的幂等执行状态.
// 用 client_message_id + session_id 做唯一约束,防止同一条消息被重复处理.
type ChatRequest struct {
	// ID 数据库自增主键
	ID int64
	// ClientMessageID 客户端传来的消息 ID,用来防重复
	ClientMessageID string
	// SessionID 这个请求属于哪个会话
	SessionID string
	// UserID 哪个用户发的
	UserID int64
	// Status 当前状态:accepted/running/completed/failed
	Status string
	// UserDialogueID 关联的用户消息 ID(dialogues 表的主键),accepted 时为 nil
	UserDialogueID *int64
	// AssistantDialogueID 关联的助手消息 ID,completed 后才有值
	AssistantDialogueID *int64
	// ErrorCode 失败时的错误码,成功时为 nil
	ErrorCode *string
	// TraceID 链路追踪 ID
	TraceID string
	// CreatedAt 请求创建时间
	CreatedAt time.Time
	// UpdatedAt 最后更新时间(每次状态变更都会更新)
	UpdatedAt time.Time
	// CompletedAt 完成时间,completed 状态才有值,其他状态为 nil
	CompletedAt *time.Time
}

// AppSetting 是可以明文保存的普通运行配置.
// 比如系统名称,默认模型等非敏感信息.
type AppSetting struct {
	// Key 配置项的键名(主键)
	Key string
	// Value 配置项的值
	Value string
	// ValueType 值类型,比如 "string","int","bool"
	ValueType string
	// Description 配置项说明
	Description string
	// UpdatedAt 最后更新时间
	UpdatedAt time.Time
}

// EncryptedSecret 是只允许以密文形式保存的敏感配置.
// 比如 API Key,加密密钥等,不能明文存数据库.
type EncryptedSecret struct {
	// Key 密钥的键名(主键)
	Key string
	// Ciphertext 加密后的密文
	Ciphertext []byte
	// Nonce 加密用的随机数(每次加密都不一样)
	Nonce []byte
	// Algorithm 加密算法,比如 "chacha20-poly1305"
	Algorithm string
	// KeyVersion 密钥版本号,密钥轮换时用
	KeyVersion int
	// UpdatedAt 最后更新时间
	UpdatedAt time.Time
}

// ormUser 是 users 表的 GORM ORM 模型.
// GORM tag 说明:
//   - primaryKey 主键
//   - autoInsert 自动递增
//   - not null 不允许空
//   - default:xxx 默认值
//   - autoCreateTime 创建时自动填当前时间
type ormUser struct {
	// ID 自增主键
	ID int64 `gorm:"primaryKey;autoIncrement"`
	// Name 用户名,不允许空
	Name string `gorm:"not null"`
	// Role 角色,默认 "owner"
	Role string `gorm:"default:owner"`
	// VoicePrint 声纹特征,指针类型允许 NULL(没录就是 NULL)
	VoicePrint *string
	// FacePrint 人脸特征,指针类型允许 NULL
	FacePrint *string
	// CreatedAt 创建时间,GORM 自动填
	CreatedAt time.Time `gorm:"autoCreateTime"`
	// LastActiveAt 最后活跃时间,GORM 自动填
	LastActiveAt time.Time `gorm:"autoCreateTime"`
}

// ormSession 是 sessions 表的 GORM ORM 模型.
type ormSession struct {
	// ID 会话标识,字符串主键(外部传入,比如 UUID)
	ID string `gorm:"primaryKey"`
	// UserID 用户 ID,不允许空
	UserID int64 `gorm:"not null"`
	// StartedAt 会话开始时间,GORM 自动填
	StartedAt time.Time `gorm:"autoCreateTime"`
	// LastActiveAt 最后活跃时间,GORM 自动填
	LastActiveAt time.Time `gorm:"autoCreateTime"`
	// Status 会话状态,默认 "active"
	Status string `gorm:"default:active"`
}

// ormDialogue 是 dialogues 表的 GORM ORM 模型.
// 复合索引 idx_dialogues_session_time 用于按会话和时间查最近消息(GetRecentDialogues 用).
type ormDialogue struct {
	// ID 自增主键,排在复合索引最后(priority:3)
	ID int64 `gorm:"primaryKey;autoIncrement;index:idx_dialogues_session_time,sort:desc,priority:3"`
	// SessionID 会话 ID,复合索引第一列(priority:1)
	SessionID string `gorm:"not null;index:idx_dialogues_session_time,priority:1"`
	// UserID 用户 ID
	UserID int64 `gorm:"not null"`
	// Role 角色,"user" 或 "assistant"
	Role string `gorm:"not null"`
	// Content 消息内容
	Content string `gorm:"not null"`
	// PromptTokens 输入 token 数,默认 0
	PromptTokens int `gorm:"default:0"`
	// CompletionTokens 输出 token 数,默认 0
	CompletionTokens int `gorm:"default:0"`
	// CacheHitTokens 缓存命中 token 数
	CacheHitTokens int `gorm:"default:0"`
	// CacheMissTokens 缓存未命中 token 数
	CacheMissTokens int `gorm:"default:0"`
	// ReasoningTokens 推理 token 数
	ReasoningTokens int `gorm:"default:0"`
	// TotalTokens 总 token 数
	TotalTokens int `gorm:"default:0"`
	// TraceID 链路追踪 ID,单列索引方便按 trace 查
	TraceID string `gorm:"index:idx_dialogues_trace"`
	// Timestamp 消息时间,GORM 自动填,排在复合索引第二列(priority:2,倒序)
	Timestamp time.Time `gorm:"autoCreateTime;index:idx_dialogues_session_time,sort:desc,priority:2"`
}

// ormEvent 是 events 表的 GORM ORM 模型.
// 复合索引 idx_events_type_time 用于按事件类型和时间查询.
type ormEvent struct {
	// ID 自增主键
	ID int64 `gorm:"primaryKey;autoIncrement"`
	// EventType 事件类型,复合索引第一列(priority:1)
	EventType string `gorm:"not null;index:idx_events_type_time,priority:1"`
	// UserID 用户 ID,指针类型允许 NULL(系统级事件没有关联用户)
	UserID *int64
	// EventData 事件数据
	EventData string
	// TraceID 链路追踪 ID,单列索引
	TraceID string `gorm:"index:idx_events_trace"`
	// Timestamp 事件时间,GORM 自动填,复合索引第二列(priority:2,倒序)
	Timestamp time.Time `gorm:"autoCreateTime;index:idx_events_type_time,sort:desc,priority:2"`
	// DurationMs 耗时毫秒
	DurationMs int64
	// Success 成功没有,指针类型允许 NULL,默认 true
	Success *bool `gorm:"default:true"`
}

// ormChatRequest 是 chat_requests 表的 GORM ORM 模型.
// 唯一索引 idx_chat_requests_session_client 保证 (session_id, client_message_id) 组合唯一(幂等用).
type ormChatRequest struct {
	// ID 自增主键
	ID int64 `gorm:"primaryKey;autoIncrement"`
	// ClientMessageID 客户端消息 ID,唯一索引第二列(priority:2)
	ClientMessageID string `gorm:"not null;uniqueIndex:idx_chat_requests_session_client,priority:2"`
	// SessionID 会话 ID,唯一索引第一列(priority:1)
	SessionID string `gorm:"not null;uniqueIndex:idx_chat_requests_session_client,priority:1"`
	// UserID 用户 ID
	UserID int64 `gorm:"not null"`
	// Status 请求状态,复合索引第一列(priority:1)
	Status string `gorm:"not null;index:idx_chat_requests_status_updated,priority:1"`
	// UserDialogueID 关联的用户消息 ID,指针允许 NULL
	UserDialogueID *int64
	// AssistantDialogueID 关联的助手消息 ID,指针允许 NULL
	AssistantDialogueID *int64
	// ErrorCode 错误码,指针允许 NULL
	ErrorCode *string
	// TraceID 链路追踪 ID,单列索引
	TraceID string `gorm:"not null;index:idx_chat_requests_trace"`
	// CreatedAt 创建时间,GORM 自动填
	CreatedAt time.Time `gorm:"autoCreateTime"`
	// UpdatedAt 最后更新时间,GORM 自动更新,复合索引第二列(priority:2,倒序)
	UpdatedAt time.Time `gorm:"autoUpdateTime;index:idx_chat_requests_status_updated,sort:desc,priority:2"`
	// CompletedAt 完成时间,指针允许 NULL(没完成时为 NULL)
	CompletedAt *time.Time
}

// ormAppSetting 是 app_settings 表的 GORM ORM 模型.
// 用 column 标签显式指定列名,因为表里列名和字段名不一样(setting_key/setting_value).
type ormAppSetting struct {
	// Key 配置键名,主键,列名 setting_key
	Key string `gorm:"column:setting_key;primaryKey"`
	// Value 配置值,列名 setting_value
	Value string `gorm:"column:setting_value;not null"`
	// ValueType 值类型
	ValueType string `gorm:"not null"`
	// Description 配置说明
	Description string
	// UpdatedAt 最后更新时间,GORM 自动更新
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

// ormEncryptedSecret 是 encrypted_secrets 表的 GORM ORM 模型.
// 用 column 标签显式指定列名 secret_key.
type ormEncryptedSecret struct {
	// Key 密钥键名,主键,列名 secret_key
	Key string `gorm:"column:secret_key;primaryKey"`
	// Ciphertext 密文
	Ciphertext []byte `gorm:"not null"`
	// Nonce 加密随机数
	Nonce []byte `gorm:"not null"`
	// Algorithm 加密算法
	Algorithm string `gorm:"not null"`
	// KeyVersion 密钥版本号
	KeyVersion int `gorm:"not null"`
	// UpdatedAt 最后更新时间,GORM 自动更新
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

// ModelUsage 是一次模型调用的 Token 用量和费用估算记录(业务对象).
// 每次调模型(成功或失败)都写一条,独立于 dialogues 表.
// 方便按供应商,模型,操作类型聚合统计成本.
//
// NULL 和 0 的区别(方案铁律):
//   - 0:供应商明确返回该项没有消耗.
//   - nil(指针为 nil):供应商没返回,该模型不适用或当前无法可靠计算.
// 不能把 nil 自动变成 0,否则看板会错误显示"没有缓存 Token".
type ModelUsage struct {
	// ID 数据库自增主键
	ID int64
	// RequestID 本次模型调用的内部请求标识,目前用 trace_id
	RequestID string
	// TraceID 链路追踪 ID,关联 events 和 dialogues
	TraceID string
	// SessionID 哪个会话产生的调用,空字符串表示不关联会话
	SessionID string
	// UserID 哪个用户产生的调用,0 表示不关联用户
	UserID int64
	// DeviceID 哪个设备产生的调用(阶段 4+ 才有),空字符串表示本机调用
	DeviceID string
	// Provider 供应商名称,如 "deepseek","openai"
	Provider string
	// Model 模型名,如 "deepseek-chat"
	Model string
	// Operation 操作类型:chat/embedding/vision/asr/tts
	Operation string
	// InputTokens 输入 Token 总量
	// 指针类型:nil = 供应商没返回,非 nil = 供应商返回了具体数值
	InputTokens *int
	// OutputTokens 输出 Token 数
	OutputTokens *int
	// CachedInputTokens 输入中命中供应商缓存的部分
	// 指针类型:nil = 供应商没返回,非 nil = 供应商返回了具体数值
	CachedInputTokens *int
	// CacheMissTokens 缓存未命中的输入 Token(DeepSeek/OpenAI 返回 prompt_cache_miss_tokens)
	// 指针类型:nil = 供应商没返回,非 nil = 供应商返回了具体数值
	CacheMissTokens *int
	// CacheCreationTokens 创建缓存时单独计量的输入(Anthropic 的概念);供应商不支持则为 nil
	CacheCreationTokens *int
	// ReasoningTokens 供应商明确返回的推理 Token;不支持则为 nil
	ReasoningTokens *int
	// TotalTokens 总 Token 数;按供应商返回值保存,缺失时才由已知字段推导
	TotalTokens *int
	// InputAudioSeconds ASR 输入音频时长(秒),非 ASR 操作为 nil
	InputAudioSeconds *float64
	// OutputAudioSeconds TTS 输出音频时长(秒),非 TTS 操作为 nil
	OutputAudioSeconds *float64
	// InputImageCount 视觉操作输入图片数,非视觉操作为 nil
	InputImageCount *int
	// Currency 费用币种,如 "USD","CNY";无费用估算时为空
	Currency string
	// EstimatedCostMicros 估算费用,百万分之一货币单位;无估算时为 nil
	EstimatedCostMicros *int64
	// ProviderRequestID 供应商返回的请求 ID(有些供应商不返回)
	ProviderRequestID string
	// Status 调用状态:ok / failed
	Status string
	// DurationMs 调用耗时(毫秒)
	DurationMs int64
	// OccurredAt 调用发生时间
	OccurredAt time.Time
}

// ormModelUsage 是 model_usages 表的 GORM ORM 模型.
// 所有字段都用指针类型(*int,*float64,*int64),对应数据库里的 NULL.
// 只有 ID,TraceID,Provider,Model,Operation,Status,OccurredAt 不允许 NULL.
type ormModelUsage struct {
	// ID 自增主键
	ID int64 `gorm:"primaryKey;autoIncrement"`
	// RequestID 内部请求标识,目前用 trace_id
	RequestID *string
	// TraceID 链路追踪 ID,关联 events 和 dialogues,不允许空
	TraceID string `gorm:"not null;index:idx_model_usages_trace"`
	// SessionID 会话 ID,允许 NULL
	SessionID *string
	// UserID 用户 ID,允许 NULL,同时是 idx_model_usages_user 索引第一列
	UserID *int64 `gorm:"index:idx_model_usages_user,priority:1"`
	// DeviceID 设备 ID,允许 NULL(阶段 4+ 才有)
	DeviceID *string
	// Provider 供应商名称,不允许空,同时是 idx_model_usages_provider_model 索引第一列
	Provider string `gorm:"not null;index:idx_model_usages_provider_model,priority:1"`
	// Model 模型名,不允许空,同时是 idx_model_usages_provider_model 索引第二列
	Model string `gorm:"not null;index:idx_model_usages_provider_model,priority:2"`
	// Operation 操作类型,不允许空
	Operation string `gorm:"not null"`
	// InputTokens 输入 Token 总量,允许 NULL
	InputTokens *int
	// OutputTokens 输出 Token 数,允许 NULL
	OutputTokens *int
	// CachedInputTokens 缓存命中输入 Token,允许 NULL
	CachedInputTokens *int
	// CacheMissTokens 缓存未命中输入 Token,允许 NULL
	CacheMissTokens *int
	// CacheCreationTokens 缓存创建 Token,允许 NULL
	CacheCreationTokens *int
	// ReasoningTokens 推理 Token,允许 NULL
	ReasoningTokens *int
	// TotalTokens 总 Token 数,允许 NULL
	TotalTokens *int
	// InputAudioSeconds ASR 输入音频时长,允许 NULL
	InputAudioSeconds *float64
	// OutputAudioSeconds TTS 输出音频时长,允许 NULL
	OutputAudioSeconds *float64
	// InputImageCount 视觉输入图片数,允许 NULL
	InputImageCount *int
	// Currency 费用币种,允许 NULL
	Currency *string
	// EstimatedCostMicros 估算费用,允许 NULL
	EstimatedCostMicros *int64
	// ProviderRequestID 供应商请求 ID,允许 NULL
	ProviderRequestID *string
	// Status 调用状态,不允许空
	Status string `gorm:"not null"`
	// DurationMs 调用耗时(毫秒)
	DurationMs int64
	// OccurredAt 调用发生时间,不允许空
	// 同时是 idx_model_usages_time 索引和 idx_model_usages_user 索引第二列
	OccurredAt time.Time `gorm:"not null;index:idx_model_usages_time;index:idx_model_usages_user,priority:2"`
}

// sqliteRepo 是 Repository 接口的 SQLite 实现.
// 里面就一个字段 db,是 GORM 的数据库连接对象.
// 所有 Repository 方法都挂在 *sqliteRepo 上,通过 db 操作数据库.
type sqliteRepo struct {
	// db 是 GORM 的 *gorm.DB 实例,所有数据库操作都靠它
	db *gorm.DB
}

// Migration 描述从磁盘加载的一份版本化 SQL 迁移文件.
// loadMigrations 函数读磁盘后生成这个结构体,applyMigration 函数执行它.
type Migration struct {
	// 版本号,如 1
	Version int
	// 名称,如 "init"
	Name string
	// 文件完整路径,如 "script/migrations/0001_init.sql"
	Path string
	// 文件内容的 SHA-256 指纹,64 字符十六进制字符串
	Checksum string
	// 文件的完整 SQL 文本
	SQL string
}

// MigrationRecord 是 schema_migrations 表中的迁移执行记录.
// 每次执行迁移文件前插一条 running 记录,执行完更新为 completed 或 failed.
// 下次启动时查这张表,知道哪些版本执行过了,成功没有.
type MigrationRecord struct {
	// 版本号,主键,如 1,2
	Version int `gorm:"primaryKey"`
	// 迁移名称,如 "init"
	Name string `gorm:"not null"`
	// SQL 文件的 SHA-256 指纹,用于检测已执行的迁移文件是否被篡改
	Checksum string `gorm:"not null"`
	// 执行状态:running / completed / failed
	Status string `gorm:"not null"`
	// 开始执行时间
	StartedAt time.Time `gorm:"not null"`
	// 完成时间,指针类型:running 状态时为 NULL,completed 时才有值
	CompletedAt *time.Time
	// 失败时的错误信息,指针类型:成功时为 NULL,失败时才有值
	ErrorMessage *string
}

// Span 是 Span 追踪记录的业务对象.
// 和 trace.Span 对应,但这个不依赖 trace 包,给 data 层用.
type Span struct {
	// ID 是 Span 的唯一标识(UUID),主键
	ID string
	// TraceID 是链路追踪 ID,同一次请求的所有 Span 共享同一个
	TraceID string
	// ParentSpanID 是父 Span 的 ID,根 Span 为空字符串
	ParentSpanID string
	// Component 是组件名:handler / chat_service / model_provider
	Component string
	// Operation 是操作名:POST /api/chat,stream_loop,llm.stream 等
	Operation string
	// Status 是状态:ok / error / cancelled
	Status string
	// DurationMs 是耗时(毫秒)
	DurationMs int64
	// StartedAt 是开始时间
	StartedAt time.Time
	// FinishedAt 是结束时间
	FinishedAt time.Time
	// Attributes 是附加属性 JSON 字符串(model,error_code 等)
	Attributes string
}

// ormSpan 是 spans 表的 GORM ORM 模型.
type ormSpan struct {
	// ID 是 Span 的唯一标识(UUID),主键
	ID string `gorm:"primaryKey"`
	// TraceID 是链路追踪 ID,不允许空
	TraceID string `gorm:"not null;index:idx_spans_trace,priority:1"`
	// ParentSpanID 是父 Span 的 ID,根 Span 为 NULL
	ParentSpanID *string `gorm:"index:idx_spans_parent"`
	// Component 是组件名,不允许空
	Component string `gorm:"not null"`
	// Operation 是操作名,不允许空
	Operation string `gorm:"not null"`
	// Status 是状态,不允许空
	Status string `gorm:"not null"`
	// DurationMs 是耗时(毫秒),默认 0
	DurationMs int64 `gorm:"default:0"`
	// StartedAt 是开始时间,不允许空
	StartedAt time.Time `gorm:"not null;index:idx_spans_trace,priority:2"`
	// FinishedAt 是结束时间,允许 NULL(异常退出没来得及标记)
	FinishedAt *time.Time
	// Attributes 是附加属性 JSON 字符串,允许 NULL
	Attributes *string
}

// TableName 指定 spans 表名,不靠 GORM 的复数命名规则.
func (ormSpan) TableName() string { return "spans" }

// 下面是各表的列名常量,SELECT 查询用显式列名,不用 SELECT *.
// 好处:表加列不会意外查出不需要的数据;列顺序稳定;SQL 日志清晰.
const (
	// users 表列名
	userColumns = "id, name, role, voice_print, face_print, created_at, last_active_at"

	// sessions 表列名
	sessionColumns = "id, user_id, started_at, last_active_at, status"

	// dialogues 表列名
	dialogueColumns = "id, session_id, user_id, role, content, prompt_tokens, completion_tokens, cache_hit_tokens, cache_miss_tokens, reasoning_tokens, total_tokens, trace_id, timestamp"

	// chat_requests 表列名
	chatRequestColumns = "id, client_message_id, session_id, user_id, status, user_dialogue_id, assistant_dialogue_id, error_code, trace_id, created_at, updated_at, completed_at"

	// app_settings 表列名
	appSettingColumns = "setting_key, setting_value, value_type, description, updated_at"

	// encrypted_secrets 表列名
	encryptedSecretColumns = "secret_key, ciphertext, nonce, algorithm, key_version, updated_at"

	// schema_migrations 表列名
	migrationRecordColumns = "version, name, checksum, status, started_at, completed_at, error_message"

	// spans 表列名
	spanColumns = "id, trace_id, parent_span_id, component, operation, status, duration_ms, started_at, finished_at, attributes"
)
