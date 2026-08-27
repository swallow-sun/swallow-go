// types.go 放 data 层共用的类型定义.
//
// 做的事情:
//  1. 定义 Repository 接口:数据访问层的统一接口,抽象所有数据库操作,换数据库只需换实现不改业务代码.
//  2. 定义业务实体结构体:User、Device、Session、Dialogue、ChatRequest、Event、配置等.
//  3. 定义 ORM 模型结构体:ormUser,ormSession,ormDialogue 等,带 GORM 标签映射数据库表.
//  4. 定义迁移、聊天请求、模型用量、设备和长期记忆状态常量.
//  5. 定义各表列名常量:SELECT 查询用显式列名,不用 SELECT *.
//  6. 定义迁移相关结构体:Migration(磁盘迁移文件)和 MigrationRecord(schema_migrations 表记录).
//  7. 定义 Span ORM 模型:ormSpan 对应 spans 表,记录一次请求经过的每个处理步骤.
package data

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// SpanSinkAdapter 把 Repository 适配为 trace.SpanSink。
type SpanSinkAdapter struct{ Repo Repository }

const (
	// SessionStatusActive 表示会话可以继续接收消息。
	SessionStatusActive = "active"
	// SessionStatusClosed 表示会话已经关闭，不应再接收新消息。
	SessionStatusClosed = "closed"

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
	ModelOperationChat      = "chat"      // 文字对话
	ModelOperationEmbedding = "embedding" // 向量嵌入
	ModelOperationVision    = "vision"    // 视觉理解
	ModelOperationASR       = "asr"       // 语音识别(Automatic Speech Recognition)
	ModelOperationTTS       = "tts"       // 语音合成(Text To Speech)

	// status 字段标记这次模型调用成功还是失败.
	// 和 ChatRequest 的状态不同,model_usages 只记最终结果,不记中间状态.
	ModelUsageStatusOK     = "ok"     // 模型调用成功
	ModelUsageStatusFailed = "failed" // 模型调用失败

	// ErrPriceNotFound 表示没找到指定供应商+模型在指定时间点的价格快照.
	// 调用方据此决定是跳过费用估算还是报错.
	ErrPriceNotFound = "price_snapshot_not_found"

	// ErrDuplicatedKey 表示插入时违反了唯一约束(比如用户名重复).
	// 上层据此做"冲突后重新查询"的并发安全处理,而不是直接报错.
	ErrDuplicatedKey = "duplicated_key"

	// DeviceStatusActive 表示设备令牌有效,允许通过设备认证.
	DeviceStatusActive = "active"
	// DeviceStatusRevoked 表示设备已被用户吊销,禁止继续访问服务端.
	DeviceStatusRevoked = "revoked"
	// deviceColumns 是查询 devices 表时使用的完整显式列名.
	deviceColumns = "id, user_id, name, platform, token_hash, status, capabilities_json, created_at, last_seen_at, revoked_at"
)

// Repository 是数据访问层的统一接口.
// 所有数据库操作都通过这个接口暴露,业务代码只依赖接口,不依赖具体实现.
// 好处:换数据库(SQLite → MySQL)只需要换实现,业务代码不用改.
//
// 方法分组:
//   - 用户:CreateUser,GetUser,GetUserByName,UpdateUserActive
//   - 设备:CreateDevice,GetDevice,UpdateDeviceLastSeen
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

	// CreateDevice 注册一台设备,令牌字段只接收不可逆摘要.
	CreateDevice(ctx context.Context, device Device) (Device, error)
	// GetDevice 按设备 UUID 查询设备身份.
	GetDevice(ctx context.Context, id string) (Device, error)
	// UpdateDeviceLastSeen 更新设备最近一次认证成功时间.
	UpdateDeviceLastSeen(ctx context.Context, id string, at time.Time) error

	// CreateSession 新建会话.
	CreateSession(ctx context.Context, sessionID string, userID int64) (Session, error)
	// GetSession 按会话 ID 查询会话.
	GetSession(ctx context.Context, sessionID string) (Session, error)
	// GetSessionForUser 按会话 ID 查询会话, 同时校验会话归属.
	// 如果 sessionID 存在但不属于 userID, 返回 sql.ErrNoRows.
	// 所有需要认证的接口必须用这个方法, 不能用 GetSession.
	GetSessionForUser(ctx context.Context, sessionID string, userID int64) (Session, error)
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
	// GetRecentDialoguesForUser 查最近 N 条对话消息, 同时校验会话归属.
	// 如果 sessionID 存在但不属于 userID, 返回空列表.
	// 所有需要认证的接口必须用这个方法, 不能用 GetRecentDialogues.
	GetRecentDialoguesForUser(ctx context.Context, sessionID string, userID int64, limit int) ([]Dialogue, error)

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

	// GetPriceSnapshot 查询指定供应商+模型在指定时间点的有效价格快照.
	// 找不到返回 ErrPriceNotFound, 方便调用方决定是跳过费用估算还是报错.
	GetPriceSnapshot(ctx context.Context, provider, model string, at time.Time) (ModelPriceSnapshot, error)

	// UpsertModelUsageDaily 把一条原始 model_usages 记录聚合到日表.
	// 幂等: 同一聚合键(date+device+user+provider+model+operation)存在就累加, 不存在就插入.
	// 方案 15.7 节: 原始用量写入成功后通过幂等聚合任务更新日表.
	UpsertModelUsageDaily(ctx context.Context, usage ModelUsage) error

	// GetDailyUsage 按日期范围查日聚合数据, 供看板查询.
	// 返回按日期倒序排列的日聚合记录.
	GetDailyUsage(ctx context.Context, dateFrom, dateTo string) ([]ModelUsageDaily, error)

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

	// ===== 设备同步 =====

	// InsertDeviceSyncLog 原子插入一条设备同步日志, 用 (device_id, item_id) 做幂等.
	// 返回 created=true 表示这是首次接收, created=false 表示重复条目已存在.
	InsertDeviceSyncLog(ctx context.Context, log DeviceSyncLog) (bool, error)
	// DeleteDeviceSyncLog 撤销处理失败条目的幂等占位，使设备下次可以安全重试.
	DeleteDeviceSyncLog(ctx context.Context, deviceID, itemID string) error

	// ===== 长期记忆: 候选管理 =====

	// InsertMemoryCandidate 创建一条记忆候选.
	// 对话产生候选后调, status 初始为 pending.
	InsertMemoryCandidate(ctx context.Context, c MemoryCandidate) (MemoryCandidate, error)
	// GetMemoryCandidate 按 ID 查一条候选.
	GetMemoryCandidate(ctx context.Context, id int64) (MemoryCandidate, error)
	// GetMemoryCandidates 按用户 ID 和状态查候选列表.
	// status 为空时查所有状态, 按创建时间倒序返回.
	GetMemoryCandidates(ctx context.Context, userID int64, status string) ([]MemoryCandidate, error)
	// ConfirmMemoryCandidate 把候选状态从 pending 改成 confirmed, 同时写入正式记忆.
	// 返回新建的 Memory 记录.
	ConfirmMemoryCandidate(ctx context.Context, id int64, userID int64) (Memory, error)
	// RejectMemoryCandidate 把候选状态从 pending 改成 rejected.
	RejectMemoryCandidate(ctx context.Context, id int64, userID int64) error

	// ===== 长期记忆: 正式记忆管理 =====

	// InsertMemory 创建一条正式记忆, 通常由 ConfirmMemoryCandidate 内部调.
	InsertMemory(ctx context.Context, m Memory) (Memory, error)
	// GetMemory 按 ID 查一条记忆.
	GetMemory(ctx context.Context, id int64) (Memory, error)
	// GetMemories 按用户 ID 查正式记忆列表, 只返回 status=active 的记录.
	GetMemories(ctx context.Context, userID int64) ([]Memory, error)
	// SearchMemories 按用户 ID + 关键词检索记忆, 只搜 status=active 的记录.
	// 第一版用 LIKE, 不用向量.
	SearchMemories(ctx context.Context, userID int64, keywords string, limit int) ([]Memory, error)
	// GetMemorySyncChanges 按同步版本返回设备需要应用的记忆和删除墓碑.
	GetMemorySyncChanges(ctx context.Context, userID int64, sinceVersion, limit int) (MemorySyncChanges, error)
	// UpdateMemory 编辑记忆内容和关键词, 同时写一条版本记录到 memory_versions.
	UpdateMemory(ctx context.Context, id int64, userID int64, content, keywords string) (Memory, error)
	// DeleteMemory 软删记忆(status=deleted), 同时写一条 tombstone.
	DeleteMemory(ctx context.Context, id int64, userID int64) error

	// ===== 阶段 4.5: 对话标签 =====

	// InsertDialogueTag 创建一条对话标签明细.
	InsertDialogueTag(ctx context.Context, tag DialogueTag) (DialogueTag, error)
	// GetDialogueTags 按用户 ID 查标签明细, 按时间倒序, 限制条数.
	GetDialogueTags(ctx context.Context, userID int64, limit int) ([]DialogueTag, error)
	// CountDialogueTagsByUser 统计用户当前的对话轮数 (最大的 round 值).
	CountDialogueTagsByUser(ctx context.Context, userID int64) (int, error)

	// ===== 阶段 4.5: 标签统计 =====

	// UpsertTagStatistic UPSERT 一条按天聚合的标签统计.
	UpsertTagStatistic(ctx context.Context, stat TagStatistic) error
	// GetTagStatistics 查标签统计, 可按维度过滤, since > 0 时只查 last_round > since 的记录.
	GetTagStatistics(ctx context.Context, userID int64, tagDim string, since int) ([]TagStatistic, error)

	// ===== 阶段 4.5: 情绪持续段 =====

	// InsertEmotionSession 创建一条情绪持续段.
	InsertEmotionSession(ctx context.Context, session EmotionSession) (EmotionSession, error)
	// UpdateEmotionSession 更新一条情绪持续段 (延长或结束).
	UpdateEmotionSession(ctx context.Context, id int64, fields map[string]any) error
	// GetLatestEmotionSession 取用户最近一条情绪持续段 (可能是进行中或已结束).
	GetLatestEmotionSession(ctx context.Context, userID int64) (EmotionSession, error)
	// GetEmotionSessions 查情绪持续段列表, since > 0 时只查 start_round > since 的记录.
	GetEmotionSessions(ctx context.Context, userID int64, since int, limit int) ([]EmotionSession, error)

	// ===== 阶段 4.5: 用户画像 =====

	// GetUserProfile 按用户 ID 查画像.
	GetUserProfile(ctx context.Context, userID int64) (UserProfile, error)
	// UpsertUserProfile 创建或更新用户画像.
	UpsertUserProfile(ctx context.Context, profile UserProfile) (UserProfile, error)
	// GetCompanionState / UpsertCompanionState 读写用户与 Agent 的关系人格状态。
	GetCompanionState(ctx context.Context, userID int64) (CompanionState, error)
	UpsertCompanionState(ctx context.Context, state CompanionState) (CompanionState, error)

	// ===== 阶段 4.5: 待办提醒 =====

	// InsertReminder 创建一条待办提醒.
	InsertReminder(ctx context.Context, reminder Reminder) (Reminder, error)
	// GetReminders 按用户 ID 和状态查提醒列表.
	GetReminders(ctx context.Context, userID int64, status string) ([]Reminder, error)
	// GetReminder 按 ID 查一条提醒.
	GetReminder(ctx context.Context, id int64) (Reminder, error)
	// UpdateReminder 更新一条提醒 (改状态/改时间/改内容).
	UpdateReminder(ctx context.Context, id int64, fields map[string]any) error
	// GetPendingRemindersDue 查已到期但尚未投递的提醒 (status=pending AND remind_at <= now).
	GetPendingRemindersDue(ctx context.Context, now time.Time) ([]Reminder, error)

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

// Device 是一台已经注册到 Go 服务端的嵌入式设备业务对象.
// TokenHash 只保存认证令牌摘要,任何接口和日志都不能返回这个字段.
type Device struct {
	ID               string     // 服务端生成的设备 UUID
	UserID           int64      // 设备所属用户 ID
	Name             string     // 用户可读设备名称
	Platform         string     // 运行平台,如 linux-arm64
	TokenHash        string     // 设备令牌的 SHA-256 十六进制摘要
	Status           string     // active 或 revoked
	CapabilitiesJSON string     // 设备能力 JSON
	CreatedAt        time.Time  // 首次注册时间
	LastSeenAt       *time.Time // 最近认证成功时间
	RevokedAt        *time.Time // 吊销时间
}

// ormDevice 是 devices 表的 GORM ORM 模型.
type ormDevice struct {
	ID               string     `gorm:"primaryKey"`                                            // 设备 UUID 主键
	UserID           int64      `gorm:"not null;uniqueIndex:idx_devices_user_name"`            // 所属用户 ID
	Name             string     `gorm:"not null;uniqueIndex:idx_devices_user_name"`            // 同一用户下唯一名称
	Platform         string     `gorm:"not null;default:''"`                                   // 设备运行平台
	TokenHash        string     `gorm:"not null"`                                              // 认证令牌摘要
	Status           string     `gorm:"not null;default:active;index:idx_devices_user_status"` // 设备状态
	CapabilitiesJSON string     `gorm:"not null;default:'{}'"`                                 // 设备能力 JSON
	CreatedAt        time.Time  `gorm:"not null;index:idx_devices_user_status"`                // 注册时间
	LastSeenAt       *time.Time // 最近认证成功时间
	RevokedAt        *time.Time // 吊销时间
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
	// Name 用户名,不允许空,加唯一索引防止并发重复创建
	Name string `gorm:"not null;uniqueIndex"`
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
//
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

// ModelPriceSnapshot 是一条模型价格快照(业务对象).
// 方案 15.3 节: 价格会变化, 每条 model_usages 记录保存调用时使用的价格版本和估算结果.
// 查询时按 provider + model + effective_from 找到调用时点的有效价格.
type ModelPriceSnapshot struct {
	// ID 数据库自增主键
	ID int64
	// Provider 供应商名称, 如 "deepseek", "openai"
	Provider string
	// Model 模型名, 如 "deepseek-chat"
	Model string
	// EffectiveFrom 价格生效时间
	EffectiveFrom time.Time
	// InputPrice 输入 Token 单价(每百万 Token)
	InputPrice *float64
	// OutputPrice 输出 Token 单价(每百万 Token)
	OutputPrice *float64
	// CachedInputPrice 缓存命中输入 Token 单价(比正常输入便宜)
	CachedInputPrice *float64
	// CacheCreationPrice 创建缓存的单价(Anthropic 概念)
	CacheCreationPrice *float64
	// Unit 计价单位, 如 "per_million_tokens"
	Unit string
	// Currency 币种, 如 "CNY", "USD"
	Currency string
	// SourceVersion 价格来源版本标识, 方便追溯是哪次更新
	SourceVersion string
	// CreatedAt 记录创建时间
	CreatedAt time.Time
}

// ormModelPriceSnapshot 是 model_price_snapshots 表的 GORM ORM 模型.
// 价格字段用指针类型(*float64), 允许 NULL(供应商没公布某项价格时为 NULL).
type ormModelPriceSnapshot struct {
	// ID 自增主键
	ID int64 `gorm:"primaryKey;autoIncrement"`
	// Provider 供应商名称, 不允许空, 复合索引第一列
	Provider string `gorm:"not null;index:idx_price_snapshots_provider_model_time,priority:1;index:idx_price_snapshots_provider_model,priority:1"`
	// Model 模型名, 不允许空, 复合索引第二列
	Model string `gorm:"not null;index:idx_price_snapshots_provider_model_time,priority:2;index:idx_price_snapshots_provider_model,priority:2"`
	// EffectiveFrom 价格生效时间, 不允许空, 复合索引第三列(倒序)
	EffectiveFrom time.Time `gorm:"not null;index:idx_price_snapshots_provider_model_time,priority:3:desc"`
	// InputPrice 输入 Token 单价, 允许 NULL
	InputPrice *float64
	// OutputPrice 输出 Token 单价, 允许 NULL
	OutputPrice *float64
	// CachedInputPrice 缓存命中输入 Token 单价, 允许 NULL
	CachedInputPrice *float64
	// CacheCreationPrice 创建缓存的单价, 允许 NULL
	CacheCreationPrice *float64
	// Unit 计价单位, 不允许空
	Unit string `gorm:"not null"`
	// Currency 币种, 不允许空
	Currency string `gorm:"not null"`
	// SourceVersion 价格来源版本标识, 允许 NULL
	SourceVersion *string
	// CreatedAt 记录创建时间, 不允许空
	CreatedAt time.Time `gorm:"not null;index:idx_price_snapshots_provider_model,priority:3:desc"`
}

// ModelUsageDaily 是一条日聚合记录(业务对象).
// 方案 15.7 节: 大范围查询使用预聚合表, 不能每次扫描原始事件 JSON.
// 原始用量写入成功后通过幂等聚合任务更新日表.
// 聚合粒度: date + device_id + user_id + provider + model + operation.
type ModelUsageDaily struct {
	// ID 数据库自增主键
	ID int64
	// Date 聚合日期, 格式 YYYY-MM-DD
	Date string
	// DeviceID 设备 ID(阶段 4+ 才有, 目前空字符串)
	DeviceID string
	// UserID 用户 ID, 0 表示不关联用户
	UserID int64
	// Provider 供应商名称
	Provider string
	// Model 模型名
	Model string
	// Operation 操作类型: chat/embedding/vision/asr/tts
	Operation string
	// RequestCount 请求总数
	RequestCount int64
	// FailedCount 失败请求数
	FailedCount int64
	// InputTokens 输入 Token 总量
	InputTokens int64
	// OutputTokens 输出 Token 总量
	OutputTokens int64
	// CachedInputTokens 缓存命中输入 Token 总量
	CachedInputTokens int64
	// EstimatedCostMicros 估算费用总额, 无价格快照时为 nil
	EstimatedCostMicros *int64
	// Currency 币种, 无费用估算时为空
	Currency string
}

// ormModelUsageDaily 是 model_usage_daily 表的 GORM ORM 模型.
type ormModelUsageDaily struct {
	// ID 自增主键
	ID int64 `gorm:"primaryKey;autoIncrement"`
	// Date 聚合日期, 不允许空
	Date string `gorm:"not null;index:idx_usage_daily_date;index:idx_usage_daily_provider_model,priority:3;index:idx_usage_daily_user,priority:2"`
	// DeviceID 设备 ID；空字符串表示当前未关联设备，避免 NULL 破坏唯一约束。
	DeviceID string `gorm:"not null;default:''"`
	// UserID 用户 ID；0 表示系统级用量，避免 NULL 破坏唯一约束。
	UserID int64 `gorm:"not null;default:0;index:idx_usage_daily_user,priority:1"`
	// Provider 供应商名称, 不允许空
	Provider string `gorm:"not null;index:idx_usage_daily_provider_model,priority:1"`
	// Model 模型名, 不允许空
	Model string `gorm:"not null;index:idx_usage_daily_provider_model,priority:2"`
	// Operation 操作类型, 不允许空
	Operation string `gorm:"not null"`
	// RequestCount 请求总数, 不允许空, 默认 0
	RequestCount int64 `gorm:"not null;default:0"`
	// FailedCount 失败请求数, 不允许空, 默认 0
	FailedCount int64 `gorm:"not null;default:0"`
	// InputTokens 输入 Token 总量, 不允许空, 默认 0
	InputTokens int64 `gorm:"not null;default:0"`
	// OutputTokens 输出 Token 总量, 不允许空, 默认 0
	OutputTokens int64 `gorm:"not null;default:0"`
	// CachedInputTokens 缓存命中输入 Token 总量, 不允许空, 默认 0
	CachedInputTokens int64 `gorm:"not null;default:0"`
	// EstimatedCostMicros 估算费用总额, 允许 NULL
	EstimatedCostMicros *int64
	// Currency 币种, 允许 NULL
	Currency *string
}

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

	// model_usages 表列名
	modelUsageColumns = "id, request_id, trace_id, session_id, user_id, device_id, provider, model, operation, input_tokens, output_tokens, cached_input_tokens, cache_miss_tokens, cache_creation_tokens, reasoning_tokens, total_tokens, input_audio_seconds, output_audio_seconds, input_image_count, currency, estimated_cost_micros, provider_request_id, status, duration_ms, occurred_at"

	// model_price_snapshots 表列名
	modelPriceSnapshotColumns = "id, provider, model, effective_from, input_price, output_price, cached_input_price, cache_creation_price, unit, currency, source_version, created_at"

	// model_usage_daily 表列名
	modelUsageDailyColumns = "id, date, device_id, user_id, provider, model, operation, request_count, failed_count, input_tokens, output_tokens, cached_input_tokens, estimated_cost_micros, currency"

	// device_sync_log 表列名
	deviceSyncLogColumns = "id, device_id, user_id, item_id, item_type, payload, received_at"

	// memory_candidates 表列名
	memoryCandidateColumns = "id, user_id, session_id, trace_id, content, memory_type, source, reason, usage_hint, status, created_at, resolved_at"

	// memories 表列名
	memoryColumns = "id, user_id, candidate_id, source_session_id, content, memory_type, keywords, sync_version, status, created_at, updated_at"

	// memory_versions 表列名
	memoryVersionColumns = "id, memory_id, version, content, keywords, edited_by, created_at"

	// memory_tombstones 表列名
	memoryTombstoneColumns = "id, memory_id, user_id, sync_version, deleted_at"

	// dialogue_tags 表列名
	dialogueTagColumns = "id, user_id, session_id, trace_id, round, tag_dim, tag_value, tag_extra, trigger_reason, source, created_at"

	// tag_statistics 表列名
	tagStatisticColumns = "user_id, tag_dim, tag_value, period, hit_count, last_round, updated_at"

	// emotion_sessions 表列名
	emotionSessionColumns = "id, user_id, emotion, intensity, urgency, cooperation, trigger, start_round, end_round, start_at, end_at, duration_minutes, trace_id, created_at"

	// user_profiles 表列名
	userProfileColumns = "id, user_id, profile_json, analyzed_rounds, analysis_count, updated_at, created_at"

	// reminders 表列名
	reminderColumns = "id, user_id, session_id, trace_id, content, remind_at, status, source, created_at, delivered_at, acknowledged_at"
)

// ============================================================
// 阶段 4.5: 用户画像、情绪感知与主动提醒相关业务对象和 ORM 模型
// 方案 16.12.6 节
// ============================================================

// 标签来源常量.
const (
	// TagSourceLLM 表示标签由 LLM 输出.
	TagSourceLLM = "llm"
	// TagSourceRule 表示标签由 Go 规则提取.
	TagSourceRule = "rule"
)

// 提醒状态常量.
const (
	// ReminderStatusPending 表示提醒等待触发.
	ReminderStatusPending = "pending"
	// ReminderStatusDelivered 表示提醒已投递 (已注入 system prompt).
	ReminderStatusDelivered = "delivered"
	// ReminderStatusAcknowledged 表示用户已确认收到.
	ReminderStatusAcknowledged = "acknowledged"
	// ReminderStatusExpired 表示提醒已过期未确认.
	ReminderStatusExpired = "expired"
	// ReminderStatusCancelled 表示提醒已取消.
	ReminderStatusCancelled = "cancelled"
)

// 提醒来源常量.
const (
	// ReminderSourceDialogue 表示提醒从对话中提取.
	ReminderSourceDialogue = "dialogue"
	// ReminderSourceManual 表示提醒由用户手动创建.
	ReminderSourceManual = "manual"
)

// DialogueTag 是一条对话标签明细 (业务对象).
// 每轮对话的每个维度各一行, LLM 输出和 Go 规则提取的都存这里.
type DialogueTag struct {
	ID            int64   // 自增主键
	UserID        int64   // 哪个用户的标签
	SessionID     string  // 来源会话 ID
	TraceID       string  // 来源对话的 trace ID
	Round         int     // 第几轮对话
	TagDim        string  // 维度名: emotion, communication_style, topic...
	TagValue      string  // 标签值: frustrated, direct, cpp...
	TagExtra      float64 // 数值型标签的值 (如 intensity=0.6), 0 表示无
	TriggerReason string  // 情绪触发原因 (只有情绪维度用)
	Source        string  // 来源: llm / rule
	CreatedAt     time.Time
}

// ormDialogueTag 是 dialogue_tags 表的 GORM ORM 模型.
type ormDialogueTag struct {
	ID            int64  `gorm:"primaryKey;autoIncrement"`
	UserID        int64  `gorm:"not null;index:idx_dialogue_tags_user_round,priority:1"`
	SessionID     string `gorm:"not null"`
	TraceID       *string
	Round         int    `gorm:"not null;index:idx_dialogue_tags_user_round,priority:2"`
	TagDim        string `gorm:"not null;index:idx_dialogue_tags_dim_time,priority:1"`
	TagValue      string `gorm:"not null"`
	TagExtra      *float64
	TriggerReason string    `gorm:"not null;default:''"`
	Source        string    `gorm:"not null;default:llm"`
	CreatedAt     time.Time `gorm:"not null;index:idx_dialogue_tags_dim_time,priority:2:desc"`
}

// TagStatistic 是一条按天聚合的标签统计 (业务对象).
type TagStatistic struct {
	UserID    int64
	TagDim    string
	TagValue  string
	Period    string // 'YYYY-MM-DD'
	HitCount  int
	LastRound int
	UpdatedAt time.Time
}

// ormTagStatistic 是 tag_statistics 表的 GORM ORM 模型.
type ormTagStatistic struct {
	UserID    int64     `gorm:"primaryKey;index:idx_tag_stats_user_dim_val,priority:1"`
	TagDim    string    `gorm:"primaryKey;index:idx_tag_stats_user_dim_val,priority:2"`
	TagValue  string    `gorm:"primaryKey;index:idx_tag_stats_user_dim_val,priority:3"`
	Period    string    `gorm:"primaryKey"`
	HitCount  int       `gorm:"not null;default:0"`
	LastRound int       `gorm:"not null;default:0"`
	UpdatedAt time.Time `gorm:"not null"`
}

// EmotionSession 是一条情绪持续段记录 (业务对象).
type EmotionSession struct {
	ID              int64
	UserID          int64
	Emotion         string
	Intensity       float64
	Urgency         string
	Cooperation     string
	Trigger         string
	StartRound      int
	EndRound        *int
	StartAt         time.Time
	EndAt           *time.Time
	DurationMinutes *float64
	TraceID         string
	CreatedAt       time.Time
}

// ormEmotionSession 是 emotion_sessions 表的 GORM ORM 模型.
type ormEmotionSession struct {
	ID              int64   `gorm:"primaryKey;autoIncrement"`
	UserID          int64   `gorm:"not null;index:idx_emotion_sessions_user,priority:1"`
	Emotion         string  `gorm:"not null"`
	Intensity       float64 `gorm:"not null;default:0.5"`
	Urgency         string  `gorm:"not null;default:normal"`
	Cooperation     string  `gorm:"not null;default:normal"`
	Trigger         string  `gorm:"not null;default:''"`
	StartRound      int     `gorm:"not null"`
	EndRound        *int
	StartAt         time.Time `gorm:"not null;index:idx_emotion_sessions_user,priority:2:desc"`
	EndAt           *time.Time
	DurationMinutes *float64
	TraceID         *string
	CreatedAt       time.Time `gorm:"not null"`
}

// UserProfile 是用户画像 (业务对象).
type UserProfile struct {
	ID             int64
	UserID         int64
	ProfileJSON    string
	AnalyzedRounds int
	AnalysisCount  int
	UpdatedAt      time.Time
	CreatedAt      time.Time
}

// CompanionState 是可解释的关系人格状态，不代表真实主观意识。
type CompanionState struct {
	UserID              int64
	Concern             float64
	Urgency             float64
	Fondness            float64
	Playfulness         float64
	AllowTeasing        bool
	AllowStrictReminder bool
	AllowAffection      bool
	LastMode            string
	CurrentTask         string
	TaskUpdatedAt       *time.Time
	InteractionCount    int
	UpdatedAt           time.Time
}

type ormCompanionState struct {
	UserID              int64   `gorm:"primaryKey"`
	Concern             float64 `gorm:"not null;default:0"`
	Urgency             float64 `gorm:"not null;default:0"`
	Fondness            float64 `gorm:"not null;default:0.5"`
	Playfulness         float64 `gorm:"not null;default:0.3"`
	AllowTeasing        bool    `gorm:"not null;default:true"`
	AllowStrictReminder bool    `gorm:"not null;default:true"`
	AllowAffection      bool    `gorm:"not null;default:true"`
	LastMode            string  `gorm:"not null;default:neutral"`
	CurrentTask         string  `gorm:"not null;default:''"`
	TaskUpdatedAt       *time.Time
	InteractionCount    int       `gorm:"not null;default:0"`
	UpdatedAt           time.Time `gorm:"not null"`
}

// ormUserProfile 是 user_profiles 表的 GORM ORM 模型.
type ormUserProfile struct {
	ID             int64     `gorm:"primaryKey;autoIncrement"`
	UserID         int64     `gorm:"not null;uniqueIndex:idx_user_profiles_user"`
	ProfileJSON    string    `gorm:"not null;default:'{}'"`
	AnalyzedRounds int       `gorm:"not null;default:0"`
	AnalysisCount  int       `gorm:"not null;default:0"`
	UpdatedAt      time.Time `gorm:"not null"`
	CreatedAt      time.Time `gorm:"not null"`
}

// Reminder 是一条待办提醒 (业务对象).
type Reminder struct {
	ID             int64
	UserID         int64
	SessionID      string
	TraceID        string
	Content        string
	RemindAt       time.Time
	Status         string
	Source         string
	CreatedAt      time.Time
	DeliveredAt    *time.Time
	AcknowledgedAt *time.Time
}

// ormReminder 是 reminders 表的 GORM ORM 模型.
type ormReminder struct {
	ID             int64  `gorm:"primaryKey;autoIncrement"`
	UserID         int64  `gorm:"not null;index:idx_reminders_user_status,priority:1"`
	SessionID      string `gorm:"not null"`
	TraceID        *string
	Content        string    `gorm:"not null"`
	RemindAt       time.Time `gorm:"not null;index:idx_reminders_user_status,priority:2;index:idx_reminders_due,priority:2"`
	Status         string    `gorm:"not null;default:pending;index:idx_reminders_user_status,priority:3;index:idx_reminders_due,priority:1"`
	Source         string    `gorm:"not null;default:dialogue"`
	CreatedAt      time.Time `gorm:"not null"`
	DeliveredAt    *time.Time
	AcknowledgedAt *time.Time
}

// ============================================================
// 设备同步相关业务对象和 ORM 模型
// ============================================================

// DeviceSyncLog 是设备同步日志的一条记录(业务对象).
// 设备把 sync_outbox 里的条目 POST 到服务端, 服务端用 (device_id, item_id) 做幂等.
// created=false 表示这条之前已经收过了, 设备可以安全地从 outbox 删除.
// DeviceSyncLogItem 是 sync 请求体里的一条条目, 不是数据库表.
// 它对应 C++ 侧 SyncOutboxItem 的 JSON 序列化形式.
type DeviceSyncLog struct {
	// ID 数据库自增主键
	ID int64
	// DeviceID 哪台设备上报的
	DeviceID string
	// UserID 设备所属用户 ID
	UserID int64
	// ItemID 同步条目 ID (C++ 侧的 item_id, 即 event_id 或 message_id)
	ItemID string
	// ItemType 条目类型: "message" 或 "event"
	ItemType string
	// Payload JSON 字符串, 包含完整的消息或事件数据
	Payload string
	// ReceivedAt 服务端接收时间
	ReceivedAt time.Time
}

// ormDeviceSyncLog 是 device_sync_log 表的 GORM ORM 模型.
// 唯一索引 (device_id, item_id) 保证幂等: 同一设备同一条目不会重复入库.
type ormDeviceSyncLog struct {
	// ID 自增主键
	ID int64 `gorm:"primaryKey;autoIncrement"`
	// DeviceID 设备 ID, 唯一索引第一列
	DeviceID string `gorm:"not null;uniqueIndex:idx_device_sync_log_item,priority:1"`
	// UserID 用户 ID
	UserID int64 `gorm:"not null"`
	// ItemID 同步条目 ID, 唯一索引第二列
	ItemID string `gorm:"not null;uniqueIndex:idx_device_sync_log_item,priority:2"`
	// ItemType 条目类型: "message" 或 "event"
	ItemType string `gorm:"not null"`
	// Payload JSON 字符串
	Payload string `gorm:"not null"`
	// ReceivedAt 接收时间
	ReceivedAt time.Time `gorm:"not null"`
}

// ormDeviceSyncLog TableName 指定 GORM 映射的表名.
func (ormDeviceSyncLog) TableName() string { return "device_sync_log" }

// ============================================================
// 长期记忆相关业务对象和 ORM 模型
// 方案 16.11 节: 受控长期记忆
// ============================================================

// 下面是记忆候选状态常量.
const (
	// MemoryCandidateStatusPending 表示候选等待用户确认.
	MemoryCandidateStatusPending = "pending"
	// MemoryCandidateStatusConfirmed 表示用户已确认, 对应的正式记忆已写入 memories 表.
	MemoryCandidateStatusConfirmed = "confirmed"
	// MemoryCandidateStatusRejected 表示用户已拒绝, 不会写入正式记忆.
	MemoryCandidateStatusRejected = "rejected"
)

// 下面是正式记忆状态常量.
const (
	// MemoryStatusActive 表示记忆处于活跃状态, 检索时能查到.
	MemoryStatusActive = "active"
	// MemoryStatusDeleted 表示记忆已被软删, 检索时不会返回.
	MemoryStatusDeleted = "deleted"
)

// 下面是记忆类型常量.
const (
	// MemoryTypePreference 表示用户偏好, 如"喜欢简短的回答".
	MemoryTypePreference = "preference"
	// MemoryTypeFact 表示事实信息, 如"用户是程序员".
	MemoryTypeFact = "fact"
	// MemoryTypeInstruction 表示指令性记忆, 如"回答时附带代码示例".
	MemoryTypeInstruction = "instruction"
	// MemoryTypePersona 表示用户人设, 如"用户是小米笔记本用户".
	MemoryTypePersona = "persona"
)

// 下面是候选来源常量.
const (
	// MemoryCandidateSourceRule 表示候选由确定性规则产生.
	MemoryCandidateSourceRule = "rule"
	// MemoryCandidateSourceModel 表示候选由模型建议产生.
	MemoryCandidateSourceModel = "model"
)

// MemoryCandidate 是一条记忆候选(业务对象).
// 方案 16.11.3 节: 用户说的话不会直接写 memories, 而是先产生 pending 候选,
// 等用户确认后才写正式记忆.
type MemoryCandidate struct {
	// ID 数据库自增主键
	ID int64
	// UserID 哪个用户的候选
	UserID int64
	// SessionID 来源会话 ID
	SessionID string
	// TraceID 来源对话的 trace ID, 方便回溯
	TraceID string
	// Content 候选记忆内容, 比如"用户喜欢简短的回答"
	Content string
	// MemoryType 记忆类型: preference/fact/instruction/persona
	MemoryType string
	// Source 来源: rule(规则产生) / model(模型建议)
	Source string
	// Reason 为什么建议保存, 给用户看的解释
	Reason string
	// UsageHint 保存后可能如何使用, 给用户看的解释
	UsageHint string
	// Status 状态: pending/confirmed/rejected
	Status string
	// CreatedAt 创建时间
	CreatedAt time.Time
	// ResolvedAt 确认或拒绝时间, 零值表示未处理
	ResolvedAt time.Time
}

// ormMemoryCandidate 是 memory_candidates 表的 GORM ORM 模型.
type ormMemoryCandidate struct {
	// ID 自增主键
	ID int64 `gorm:"primaryKey;autoIncrement"`
	// UserID 用户 ID, 不允许空
	UserID int64 `gorm:"not null;index:idx_memory_candidates_user_status,priority:1"`
	// SessionID 来源会话 ID, 不允许空
	SessionID string `gorm:"not null"`
	// TraceID 来源对话的 trace ID, 允许 NULL
	TraceID *string `gorm:"index:idx_memory_candidates_trace"`
	// Content 候选记忆内容, 不允许空
	Content string `gorm:"not null"`
	// MemoryType 记忆类型, 不允许空
	MemoryType string `gorm:"not null"`
	// Source 来源, 不允许空, 默认 rule
	Source string `gorm:"not null;default:rule"`
	// Reason 为什么建议保存, 不允许空, 默认空串
	Reason string `gorm:"not null;default:''"`
	// UsageHint 保存后可能如何使用, 不允许空, 默认空串
	UsageHint string `gorm:"not null;default:''"`
	// Status 状态, 不允许空, 默认 pending
	Status string `gorm:"not null;default:pending;index:idx_memory_candidates_user_status,priority:2"`
	// CreatedAt 创建时间, 不允许空
	CreatedAt time.Time `gorm:"not null;index:idx_memory_candidates_user_status,priority:3:desc"`
	// ResolvedAt 确认或拒绝时间, 允许 NULL
	ResolvedAt *time.Time
}

// Memory 是一条正式记忆(业务对象).
// 只有用户确认的候选才能写入 memories.
// 检索时只查 status=active 的记录.
type Memory struct {
	// ID 数据库自增主键
	ID int64
	// UserID 哪个用户的记忆
	UserID int64
	// CandidateID 来源候选 ID, 0 表示手动创建
	CandidateID int64
	// SourceSessionID 来源会话 ID
	SourceSessionID string
	// Content 记忆内容
	Content string
	// MemoryType 记忆类型: preference/fact/instruction/persona
	MemoryType string
	// Keywords 关键词, 空格分隔, 用于检索
	Keywords string
	// SyncVersion 用户级单调同步版本号，C++ 设备按此增量拉取。
	SyncVersion int
	// Status 状态: active/deleted
	Status string
	// CreatedAt 创建时间
	CreatedAt time.Time
	// UpdatedAt 最后编辑时间
	UpdatedAt time.Time
}

// ormMemory 是 memories 表的 GORM ORM 模型.
type ormMemory struct {
	// ID 自增主键
	ID int64 `gorm:"primaryKey;autoIncrement"`
	// UserID 用户 ID, 不允许空
	UserID int64 `gorm:"not null;index:idx_memories_user,priority:1;index:idx_memories_user_type,priority:1;index:idx_memories_user_keywords,priority:1"`
	// CandidateID 来源候选 ID, 允许 NULL
	CandidateID *int64
	// SourceSessionID 来源会话 ID, 允许 NULL
	SourceSessionID *string
	// Content 记忆内容, 不允许空
	Content string `gorm:"not null"`
	// MemoryType 记忆类型, 不允许空
	MemoryType string `gorm:"not null;index:idx_memories_user_type,priority:2"`
	// Keywords 关键词, 不允许空, 默认空串
	Keywords string `gorm:"not null;default:''"`
	// SyncVersion 同步版本号, 不允许空, 默认 0
	SyncVersion int `gorm:"not null;default:0"`
	// Status 状态, 不允许空, 默认 active
	Status string `gorm:"not null;default:active;index:idx_memories_user,priority:2;index:idx_memories_user_type,priority:3;index:idx_memories_user_keywords,priority:2"`
	// CreatedAt 创建时间, 不允许空
	CreatedAt time.Time `gorm:"not null"`
	// UpdatedAt 最后编辑时间, 不允许空
	UpdatedAt time.Time `gorm:"not null;index:idx_memories_user,priority:3:desc"`
}

// MemoryVersion 是一条记忆编辑版本记录(业务对象).
// 用户编辑记忆内容时, 旧版本存到这里, 方便回溯修改历史.
type MemoryVersion struct {
	// ID 数据库自增主键
	ID int64
	// MemoryID 哪条记忆的版本
	MemoryID int64
	// Version 版本号, 从 1 开始递增
	Version int
	// Content 该版本的内容
	Content string
	// Keywords 该版本的关键词
	Keywords string
	// EditedBy 编辑者: user/system
	EditedBy string
	// CreatedAt 该版本的创建时间
	CreatedAt time.Time
}

// ormMemoryVersion 是 memory_versions 表的 GORM ORM 模型.
type ormMemoryVersion struct {
	// ID 自增主键
	ID int64 `gorm:"primaryKey;autoIncrement"`
	// MemoryID 哪条记忆的版本, 不允许空
	MemoryID int64 `gorm:"not null;index:idx_memory_versions_memory,priority:1"`
	// Version 版本号, 不允许空
	Version int `gorm:"not null;index:idx_memory_versions_memory,priority:2:desc"`
	// Content 该版本的内容, 不允许空
	Content string `gorm:"not null"`
	// Keywords 该版本的关键词, 不允许空, 默认空串
	Keywords string `gorm:"not null;default:''"`
	// EditedBy 编辑者, 不允许空, 默认 user
	EditedBy string `gorm:"not null;default:user"`
	// CreatedAt 创建时间, 不允许空
	CreatedAt time.Time `gorm:"not null"`
}

// MemoryTombstone 是一条记忆删除标记(业务对象).
// 方案 16.11.4 节: 删除记忆后普通查询和缓存都不再返回它.
// tombstone 防止已删除的记忆通过同步机制重新出现.
type MemoryTombstone struct {
	// ID 数据库自增主键
	ID int64
	// MemoryID 被删除的记忆 ID
	MemoryID int64
	// UserID 哪个用户删的
	UserID int64
	// SyncVersion 删除时的同步版本号
	SyncVersion int
	// DeletedAt 删除时间
	DeletedAt time.Time
}

// MemorySyncChanges 是一页严格按 sync_version 切分的增量数据。
type MemorySyncChanges struct {
	Memories    []Memory
	Tombstones  []MemoryTombstone
	NextVersion int
	HasMore     bool
}

// ormMemoryTombstone 是 memory_tombstones 表的 GORM ORM 模型.
type ormMemoryTombstone struct {
	// ID 自增主键
	ID int64 `gorm:"primaryKey;autoIncrement"`
	// MemoryID 被删除的记忆 ID, 不允许空
	MemoryID int64 `gorm:"not null"`
	// UserID 哪个用户删的, 不允许空
	UserID int64 `gorm:"not null;index:idx_memory_tombstones_user,priority:1"`
	// SyncVersion 删除时的同步版本号, 不允许空, 默认 0
	SyncVersion int `gorm:"not null;default:0"`
	// DeletedAt 删除时间, 不允许空
	DeletedAt time.Time `gorm:"not null;index:idx_memory_tombstones_user,priority:2:desc"`
}
