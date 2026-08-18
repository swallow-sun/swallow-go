// sqlite_repo.go 用 GORM 实现 Repository 接口定义的 SQLite 数据访问方法.
//
// 做的事情:
//  1. 实现 Repository 接口的全部方法:用户 CRUD,会话 CRUD,对话 CRUD,事件插入.
//  2. 显式指定 ORM 模型对应的表名(不靠 GORM 默认的复数命名规则).
//  3. 提供 ORM 模型和业务实体之间的转换函数(xxxFromORM / xxxToORM).
//  4. 提供 repositoryError 辅助函数:把 GORM 的 ErrRecordNotFound 转成 sql.ErrNoRows.
package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// 显式指定 ORM 模型对应的数据库表名,不靠 GORM 默认的复数命名规则.
// GORM 默认会把结构体名转成复数当表名,比如 ormUser → orm_users,这明显不是我们要的.
// 所以每个模型都实现 TableName() 方法,写死表名,让 GORM 按我们指定的来.

// ormUser 对应 users 表:注册用户
func (ormUser) TableName() string { return "users" }
// ormSession 对应 sessions 表:聊天会话
func (ormSession) TableName() string { return "sessions" }
// ormDialogue 对应 dialogues 表:对话消息(每轮对话里的一条消息)
func (ormDialogue) TableName() string { return "dialogues" }
// ormEvent 对应 events 表:会话事件埋点
func (ormEvent) TableName() string { return "events" }
// ormChatRequest 对应 chat_requests 表:聊天请求记录(幂等用的)
func (ormChatRequest) TableName() string { return "chat_requests" }
// ormAppSetting 对应 app_settings 表:运行配置(明文保存)
func (ormAppSetting) TableName() string { return "app_settings" }
// ormEncryptedSecret 对应 encrypted_secrets 表:加密密钥(密文保存)
func (ormEncryptedSecret) TableName() string { return "encrypted_secrets" }

// CreateUser 新增用户,并返回数据库生成的 ID 和时间字段.
// 参数 name 是用户名,role 是角色(比如 "owner").
// 返回的 User 结构体里 ID,CreatedAt 等字段是数据库自动填好的.
func (r *sqliteRepo) CreateUser(ctx context.Context, name, role string) (User, error) {
	// 先把传进来的 name 和 role 塞进 ORM 模型,ID 留空让数据库自增
	model := ormUser{Name: name, Role: role}

	// r.db.WithContext(ctx) 给这次查询挂上 context,支持超时取消
	// .Create(&model) 执行 INSERT INTO users (name, role) VALUES (?, ?)
	// 执行完 model 里会回填数据库生成的 ID 和时间字段(GORM 自动干的)
	// .Error 拿错误信息,没报错就是 nil
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		logger.Error("users insert failed",
			zap.String("name", name),
			zap.String("role", role),
			zap.Error(err),
		)
		return User{}, fmt.Errorf("insert user: %w", err)
	}

	// 写库成功后打 Debug 日志,用 zap.Any 打写入后的完整 model
	logger.Debug("users insert succeeded",
		zap.Any("row", model),
	)

	// 把 ORM 模型转成业务对象返回,去掉 GORM 的标签和指针类型
	return userFromORM(model), nil
}

// GetUser 按用户 ID 查询用户.
// 参数 id 是用户主键.查不到返回的 error 是 sql.ErrNoRows.
func (r *sqliteRepo) GetUser(ctx context.Context, id int64) (User, error) {
	// 声明一个空的 ORM 模型变量,准备接收查出来的数据
	var model ormUser

	// r.db.WithContext(ctx) 挂上 context
	// .Select(userColumns) 只查需要的列,不用 SELECT *
	// .First(&model, id) 相当于 SELECT ... FROM users WHERE id = ? LIMIT 1
	if err := r.db.WithContext(ctx).Select(userColumns).First(&model, id).Error; err != nil {
		return User{}, fmt.Errorf("get user %d: %w", id, repositoryError(err))
	}

	// 转成业务对象返回
	return userFromORM(model), nil
}

// GetUserByName 按用户名查询第一条匹配的用户记录.
// 参数 name 是用户名.查不到返回 sql.ErrNoRows.
func (r *sqliteRepo) GetUserByName(ctx context.Context, name string) (User, error) {
	// 空的 ORM 模型变量,准备接收查询结果
	var model ormUser

	// r.db.WithContext(ctx) 挂上 context
	// .Select(userColumns) 只查需要的列,不用 SELECT *
	// .Where("name = ?", name) 加 WHERE 条件,? 是占位符,防 SQL 注入
	if err := r.db.WithContext(ctx).Select(userColumns).Where("name = ?", name).First(&model).Error; err != nil {
		return User{}, fmt.Errorf("get user by name %q: %w", name, repositoryError(err))
	}

	return userFromORM(model), nil
}

// UpdateUserActive 将用户最后活跃时间更新为当前时间.
// 参数 id 是用户 ID.每次用户有操作(发消息等)就调一次.
func (r *sqliteRepo) UpdateUserActive(ctx context.Context, id int64) error {
	// r.db.WithContext(ctx) 挂上 context
	// .Model(&ormUser{}) 指定要操作哪张表(users 表)
	//   这一步相当于告诉 GORM 后面的 .Where .Update 都是针对 users 表的
	// .Where("id = ?", id) 加 WHERE 条件,按主键找
	// .Update("last_active_at", time.Now()) 更新单个字段
	//   相当于 UPDATE users SET last_active_at = ? WHERE id = ?
	//   time.Now() 拿当前时间
	if err := r.db.WithContext(ctx).Model(&ormUser{}).Where("id = ?", id).Update("last_active_at", time.Now()).Error; err != nil {
		logger.Error("users update failed",
			zap.Int64("user_id", id),
			zap.Error(err),
		)
		return fmt.Errorf("update user active: %w", err)
	}
	logger.Debug("users update succeeded",
		zap.Int64("user_id", id),
	)
	return nil
}

// CreateSession 为用户新增会话,并返回完整会话记录.
// 参数 sessionID 是外部生成的会话 ID(比如 UUID),userID 是用户 ID.
// 返回的 Session 里有数据库自动填的 StartedAt,LastActiveAt 等字段.
func (r *sqliteRepo) CreateSession(ctx context.Context, sessionID string, userID int64) (Session, error) {
	// 构造 ORM 模型:ID 用传进来的 sessionID,UserID 用传进来的用户 ID
	// Status 设成 "active"(虽然表定义里有默认值,这里显式写更稳)
	model := ormSession{ID: sessionID, UserID: userID, Status: "active"}

	// .Create(&model) 执行 INSERT INTO sessions (id, user_id, status) VALUES (?, ?, ?)
	// 执行完 GORM 自动回填 StartedAt,LastActiveAt(因为 tag 里写了 autoCreateTime)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		logger.Error("sessions insert failed",
			zap.String("session_id", sessionID),
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		return Session{}, fmt.Errorf("insert session: %w", err)
	}

	logger.Debug("sessions insert succeeded",
		zap.Any("row", model),
	)
	return sessionFromORM(model), nil
}

// GetSession 按 session ID 查询会话.
// 参数 sessionID 是会话 ID(字符串).查不到返回 sql.ErrNoRows.
func (r *sqliteRepo) GetSession(ctx context.Context, sessionID string) (Session, error) {
	// 空的 ORM 模型变量,准备接收查询结果
	var model ormSession

	// .Select(sessionColumns) 只查需要的列
	// .First(&model, "id = ?", sessionID) 按主键查一条
	if err := r.db.WithContext(ctx).Select(sessionColumns).First(&model, "id = ?", sessionID).Error; err != nil {
		return Session{}, fmt.Errorf("get session %s: %w", sessionID, repositoryError(err))
	}

	return sessionFromORM(model), nil
}

// UpdateSessionActive 将会话最后活跃时间更新为当前时间.
// 参数 sessionID 是会话 ID.每次这个会话有新消息就调一次.
func (r *sqliteRepo) UpdateSessionActive(ctx context.Context, sessionID string) error {
	// .Model(&ormSession{}) 指定操作 sessions 表
	// .Where("id = ?", sessionID) 按主键找
	// .Update("last_active_at", time.Now()) 更新单个字段
	//   相当于 UPDATE sessions SET last_active_at = ? WHERE id = ?
	if err := r.db.WithContext(ctx).Model(&ormSession{}).Where("id = ?", sessionID).Update("last_active_at", time.Now()).Error; err != nil {
		logger.Error("sessions update failed",
			zap.String("session_id", sessionID),
			zap.Error(err),
		)
		return fmt.Errorf("update session active: %w", err)
	}
	logger.Debug("sessions update succeeded",
		zap.String("session_id", sessionID),
	)
	return nil
}

// InsertDialogue 保存一条对话消息,并返回包含自增 ID 和时间的领域对象.
// 参数说明:
//   - sessionID: 这条消息属于哪个会话
//   - userID: 哪个用户发的(如果是助手消息也记下用户 ID 方便关联)
//   - role: 角色,"user" 或 "assistant"
//   - content: 消息内容
//   - usage: token 用量统计
//   - traceID: 链路追踪 ID
func (r *sqliteRepo) InsertDialogue(ctx context.Context, sessionID string, userID int64, role, content string, usage TokenUsage, traceID string) (Dialogue, error) {
	// 把传进来的参数都塞进 ORM 模型里
	// token 用量的字段一个个拆开存,方便后面按用量统计
	model := ormDialogue{
		SessionID: sessionID, UserID: userID, Role: role, Content: content,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens, CacheHitTokens: usage.CacheHitTokens,
		CacheMissTokens: usage.CacheMissTokens, ReasoningTokens: usage.ReasoningTokens,
		TotalTokens: usage.TotalTokens, TraceID: traceID,
	}

	// .Create(&model) 执行 INSERT,把这条对话消息存进 dialogues 表
	// 执行完 GORM 自动回填 ID(自增主键)和 Timestamp(autoCreateTime)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		logger.Error("dialogues insert failed",
			zap.String("session_id", sessionID),
			zap.Int64("user_id", userID),
			zap.String("role", role),
			zap.Int("content_chars", len(content)),
			zap.String("trace_id", traceID),
			zap.Error(err),
		)
		return Dialogue{}, fmt.Errorf("insert dialogue: %w", err)
	}

	// 写库成功后打 Debug 日志,用 zap.Any 打写入后的完整 model(含 content,token,timestamp 等所有字段)
	logger.Debug("dialogues insert succeeded",
		zap.Any("row", model),
	)

	// 转成业务对象返回,调用的人能拿到 ID 和时间
	return dialogueFromORM(model), nil
}

// GetDialogue 按主键读取一条对话,用于幂等重试时返回已经完成的结果.
// 参数 id 是 dialogues 表的自增主键.查不到返回 sql.ErrNoRows.
func (r *sqliteRepo) GetDialogue(ctx context.Context, id int64) (Dialogue, error) {
	// 空的 ORM 模型变量
	var model ormDialogue

	// .Select(dialogueColumns) 只查需要的列
	// .First(&model, id) 按主键查一条
	if err := r.db.WithContext(ctx).Select(dialogueColumns).First(&model, id).Error; err != nil {
		return Dialogue{}, fmt.Errorf("get dialogue %d: %w", id, repositoryError(err))
	}

	return dialogueFromORM(model), nil
}

// GetDialogueByTraceAndRole 查询某次请求保存的指定角色消息.
// 比如幂等重试时,要拿之前保存的 assistant 消息,就传 traceID 和 "assistant".
func (r *sqliteRepo) GetDialogueByTraceAndRole(ctx context.Context, traceID, role string) (Dialogue, error) {
	// 空的 ORM 模型变量
	var model ormDialogue

	// .Select(dialogueColumns) 只查需要的列
	// .Where("trace_id = ? AND role = ?", traceID, role) 按 traceID 和 role 一起查
	// .Order("id DESC") 按自增 ID 倒序排(最新的在前)
	// .First(&model) 取第一条
	if err := r.db.WithContext(ctx).Select(dialogueColumns).
		Where("trace_id = ? AND role = ?", traceID, role).
		Order("id DESC").First(&model).Error; err != nil {
		return Dialogue{}, fmt.Errorf("get dialogue by trace %s and role %s: %w", traceID, role, repositoryError(err))
	}

	return dialogueFromORM(model), nil
}

// GetRecentDialogues 查询最近 limit 条消息,并按从旧到新的顺序返回.
// 参数 sessionID 指定查哪个会话,limit 限制条数.
// 返回的消息是按时间从旧到新排的,方便上层直接拼进上下文发给模型.
func (r *sqliteRepo) GetRecentDialogues(ctx context.Context, sessionID string, limit int) ([]Dialogue, error) {
	// 切片变量,准备接收查询结果(GORM 会把多条记录塞进来)
	var models []ormDialogue

	// .Select(dialogueColumns) 只查需要的列
	// .Where("session_id = ?", sessionID) 只查这个会话的消息
	// .Order("timestamp DESC") 按时间倒序(最新的在前)
	// .Order("id DESC") 同一时间戳内按自增 ID 倒序,保证高频连续写入时顺序仍然稳定
	// .Limit(limit) 最多取 limit 条
	// .Find(&models) 把所有匹配的记录塞进 models 切片
	if err := r.db.WithContext(ctx).Select(dialogueColumns).Where("session_id = ?", sessionID).
		Order("timestamp DESC").Order("id DESC").Limit(limit).Find(&models).Error; err != nil {
		return nil, fmt.Errorf("query dialogues: %w", err)
	}

	// 先取查询结果做反序:数据库返回的是"最新在前",我们要返回"从旧到新"
	// 预分配一个长度相等的切片
	result := make([]Dialogue, len(models))
	// 把 models[0](最新)放到 result 最后,models[len-1](最旧)放到 result[0]
	// 举个例子,models 是 [3条新, 2条, 1条旧](倒序)
	// result 就变成 [1条旧, 2条, 3条新](正序)
	for i := range models {
		result[len(models)-1-i] = dialogueFromORM(models[i])
	}
	return result, nil
}

// InsertEvent 保存一条埋点事件.
// 参数说明:
//   - eventType: 事件类型,比如 "chat_request","llm_call"
//   - userID: 关联的用户 ID,传 nil 表示不关联具体用户(系统级事件)
//   - data: 事件数据,一般是 JSON 字符串
//   - durationMs: 耗时毫秒
//   - success: 成功没有
//   - traceID: 链路追踪 ID
func (r *sqliteRepo) InsertEvent(ctx context.Context, eventType string, userID *int64, data string, durationMs int64, success bool, traceID string) error {
	// 把参数塞进 ORM 模型
	// Success 传的是指针(&success),因为表里用 *bool 可空布尔类型
	model := ormEvent{EventType: eventType, UserID: userID, EventData: data, TraceID: traceID, DurationMs: durationMs, Success: &success}

	// .Create(&model) 执行 INSERT,存进 events 表
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		logger.Error("events insert failed",
			zap.String("event_type", eventType),
			zap.Int64p("user_id", userID),
			zap.Int64("duration_ms", durationMs),
			zap.Bool("success", success),
			zap.String("trace_id", traceID),
			zap.Int("data_chars", len(data)),
			zap.Error(err),
		)
		return fmt.Errorf("insert event: %w", err)
	}

	// 写库成功后打 Debug 日志,用 zap.Any 打写入后的完整 model(含 data,duration,timestamp 等所有字段)
	logger.Debug("events insert succeeded",
		zap.Any("row", model),
	)

	return nil
}

// Close 关闭 GORM 使用的底层 sql.DB 连接池.
// 程序退出时调,释放数据库连接资源.
func (r *sqliteRepo) Close() error {
	// r.db.DB() 从 GORM 取出底层的 *sql.DB 连接池对象
	sqlDB, err := r.db.DB()
	if err != nil {
		return fmt.Errorf("get sqlite connection pool: %w", err)
	}

	// sqlDB.Close() 关闭连接池,释放资源
	return sqlDB.Close()
}

// repositoryError 保持 Repository 原有的 sql.ErrNoRows 错误约定.
// 上层代码用 errors.Is(err, sql.ErrNoRows) 判断"没找到",不直接用 GORM 的错误.
// 这里把 GORM 的 ErrRecordNotFound 转成 sql.ErrNoRows,对上层透明.
func repositoryError(err error) error {
	// errors.Is(err, gorm.ErrRecordNotFound) 判断 err 是不是(或包装了)gorm.ErrRecordNotFound
	// GORM 查不到记录时返回这个错误
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 转成 sql.ErrNoRows,这是 Go 标准库 database/sql 的"没找到"错误
		return sql.ErrNoRows
	}
	// 不是"没找到"的错误,原样返回
	return err
}

// 以下 fromORM 函数把 GORM ORM 模型转成业务对象(去掉 GORM tag,只留业务字段).
// 为什么要转？因为 ORM 模型里有指针类型(*string,*int64)和 GORM 标签,
// 业务代码用起来不方便.业务对象里都是普通类型,nil 自动转成零值.

// userFromORM 把 ORM 用户模型转成业务对象.
// VoicePrint,FacePrint 是 *string 指针,用 stringValue 转成普通 string(nil → "").
func userFromORM(model ormUser) User {
	return User{ID: model.ID, Name: model.Name, Role: model.Role, VoicePrint: stringValue(model.VoicePrint), FacePrint: stringValue(model.FacePrint), CreatedAt: model.CreatedAt, LastActiveAt: model.LastActiveAt}
}

// sessionFromORM 把 ORM 会话模型转成业务对象.
func sessionFromORM(model ormSession) Session {
	return Session{ID: model.ID, UserID: model.UserID, StartedAt: model.StartedAt, LastActiveAt: model.LastActiveAt, Status: model.Status}
}

// dialogueFromORM 把 ORM 对话模型转成业务对象.
// token 用量的字段一个个搬过去.
func dialogueFromORM(model ormDialogue) Dialogue {
	return Dialogue{
		ID: model.ID, SessionID: model.SessionID, UserID: model.UserID, Role: model.Role, Content: model.Content,
		PromptTokens: model.PromptTokens, CompletionTokens: model.CompletionTokens,
		CacheHitTokens: model.CacheHitTokens, CacheMissTokens: model.CacheMissTokens,
		ReasoningTokens: model.ReasoningTokens, TotalTokens: model.TotalTokens,
		TraceID: model.TraceID, Timestamp: model.Timestamp,
	}
}

// chatRequestFromORM 把 ORM 聊天请求模型转成业务对象.
// UserDialogueID,AssistantDialogueID,ErrorCode 是指针类型,直接搬过去(业务对象里也是指针).
func chatRequestFromORM(model ormChatRequest) ChatRequest {
	return ChatRequest{
		ID: model.ID, ClientMessageID: model.ClientMessageID,
		SessionID: model.SessionID, UserID: model.UserID, Status: model.Status,
		UserDialogueID: model.UserDialogueID, AssistantDialogueID: model.AssistantDialogueID,
		ErrorCode: model.ErrorCode, TraceID: model.TraceID,
		CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt, CompletedAt: model.CompletedAt,
	}
}

// stringValue 把可空 *string 转成 string,nil 返回空字符串.
// 用于 voice_print,face_print 等可空字段.
// 数据库里这些字段可以是 NULL,用指针类型 *string 来接收;
// 但业务对象里不想用指针,所以转成普通 string,NULL 就当空字符串处理.
func stringValue(value *string) string {
	// 指针是 nil,说明数据库里是 NULL,返回空字符串
	if value == nil {
		return ""
	}
	// *value 取出指针指向的字符串
	return *value
}
