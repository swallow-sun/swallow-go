// types.go 放 service 层共用的类型定义。
//
// 做的事情：
//  1. 定义 Deps 结构体：service 层的底层依赖集合（cfg/repo/idm/mem/llm），由 NewDeps 构造函数传进来。
//  2. 定义 ChatError：业务错误，带 HTTP 状态码和稳定错误码，handler 拿到后直接映射 HTTP 响应。
//  3. 定义 ChatEvent/UsageData：流式对话事件类型，service 通过 channel 发给 handler，handler 转 SSE。
//  4. 定义 SessionService/HistoryService 的返回值结构体：CreateSessionResult、HistoryResult 等。
//  5. 定义 chat 接口的稳定错误码常量和 client_message_id 最大长度限制。
//
// service 不依赖任何 HTTP 框架类型（不 import hertz），
// handler 只负责 HTTP 解析和 SSE 协议转换。
package service

import (
	"errors"

	"github.com/swallow-sun/swallow-go/internal/config"
	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/identity"
	"github.com/swallow-sun/swallow-go/internal/memory"
	"github.com/swallow-sun/swallow-go/internal/provider/llm"
)

// 以下常量是 chat 接口在鉴权/幂等校验失败时返回给客户端的稳定错误码。
// 客户端根据错误码判断是重试、换 session 还是直接展示错误。
// const ( ... ) 是一组常量的集合声明，每个常量代表一种错误场景
const (
	// 下面每个常量都是 string 类型，值就是错误码字符串
	ChatErrorAgentInit      = "agent_init_failed"        // Agent 创建失败（系统提示词文件缺失等）
	ChatErrorConnect        = "llm_connect_failed"       // LLM 流式连接失败（网络/鉴权问题）
	ChatErrorStreamRead     = "llm_stream_read_failed"   // 流式读取中途出错（连接断开等）
	ChatErrorClientClosed  = "client_stream_closed"     // 客户端断开连接（写 SSE 失败）
	ChatErrorUserMissing   = "user_dialogue_missing"   // 连接成功但用户消息未找到（内部状态不一致）
	ChatErrorAssistantSave = "assistant_save_failed"    // 助手回复保存失败（数据库写入失败）
	ChatErrorRequestState  = "request_state_failed"    // 幂等请求状态更新失败
	ChatErrorRequestFailed = "chat_request_failed"     // 之前的同幂等键请求已失败，不会自动重试
	ChatErrorResultMissing = "chat_result_missing"      // 请求标记已完成但找不到助手消息
	ChatErrorRequestRunning = "chat_request_in_progress" // 同一幂等键请求仍在进行中

	// MaxClientMessageIDLength 是 client_message_id 的最大长度，防止超长字符串撑爆数据库。
	MaxClientMessageIDLength = 128
)

// ChatError 是 service 层返回给 handler 的业务错误。
// handler 拿到后直接用 StatusCode 做 HTTP 响应码，用 Code 做业务错误码。
// chat 接口的幂等冲突也走这个结构（StatusCode=409）。
// ChatError 实现了 Error() string 方法，所以它满足 Go 的 error 接口
type ChatError struct {
	StatusCode int    // HTTP 状态码：400（请求参数问题）、409（幂等冲突）、500（内部错误）
	Code       string // 稳定错误码，客户端据此判断怎么处理
	TraceID    string // 关联的 trace ID，写入响应体供排查
}

// Error 方法让 ChatError 满足 Go 的 error 接口。
// error 接口只有一个方法 Error() string，任何类型只要实现了这个方法就能当 error 用
func (e *ChatError) Error() string { return e.Code }

// NewChatError 创建一个 ChatError，用于 handler 直接映射 HTTP 响应。
// 返回 *ChatError 是指针，避免结构体拷贝
func NewChatError(statusCode int, code, traceID string) *ChatError {
	// &ChatError{...} 取结构体字面量的指针，返回堆上的地址
	return &ChatError{StatusCode: statusCode, Code: code, TraceID: traceID}
}

// FromChatError 尝试把 error 转成 *ChatError，不是就返回 nil。
// handler 用这个判断是不是业务错误（需要带 code 响应），还是内部错误（统一 500）。
func FromChatError(err error) *ChatError {
	// 声明一个 *ChatError 类型的变量 ce，初始值为 nil
	var ce *ChatError
	// errors.As 是 Go 标准库 errors 包里的函数
	// 它检查 err 链上有没有 *ChatError 类型，有就塞进 ce 并返回 true
	// 举个例子，err 是 fmt.Errorf("...: %w", chatErr) 包装过的，errors.As 能剥开包装找到里面的 *ChatError
	if errors.As(err, &ce) {
		return ce
	}
	return nil
}

// ChatEventType 标识 SSE 事件的类型，handler 根据它决定写哪个 SSE event 名。
// 用 int 类型而不是 string，因为 int 比较比 string 快
type ChatEventType int

// const ( ... iota ) 是 Go 的枚举写法
// iota 是 Go 的常量计数器，从 0 开始，每出现一行 const 自动 +1
// iota + 1 让值从 1 开始（0 在 Go 里常表示"零值"，不方便区分"未设置"和"第一个"）
const (
	ChatEventMessage ChatEventType = iota + 1 // 消息内容片段，值为 1
	ChatEventUsage                           // token 用量和性能指标，值为 2
	ChatEventDone                            // 正常结束，值为 3
	ChatEventReplayDone                      // 幂等重放结束（区别于正常 done），值为 4
)

// ChatEvent 是 service 通过 channel 发给 handler 的事件。
// handler 从 channel 读到事件后，转成对应的 SSE 帧写给客户端。
type ChatEvent struct {
	Type     ChatEventType // 事件类型
	Content  string        // message 事件的文本片段
	Usage    *UsageData    // usage/done 事件的 token 用量（nil 表示不附带）
	Replayed bool          // 是否幂等重放（true 表示这是历史结果的重放）
	TraceID  string        // 关联的 trace ID
}

// UsageData 是 token 用量和性能指标的快照。
// service 在流式读取结束后从 streamReader.Usage() 和 agent.GetStreamMetrics() 收集，
// 通过 ChatEvent.Usage 传给 handler，由 handler 编码进 SSE usage 事件。
type UsageData struct {
	PromptTokens     int   // 输入 token 数
	CompletionTokens int   // 输出 token 数
	CacheHitTokens    int   // 缓存命中的 token 数
	CacheMissTokens   int   // 缓存未命中的 token 数
	ReasoningTokens   int   // 推理 token 数（思维链）
	TotalTokens       int   // 总 token 数
	FirstTokenMs      int64 // 首 token 耗时（毫秒）
	TotalDurationMs   int64 // 总耗时（毫秒）
}

// chatParams 是调 ChatService.Chat 时传进来的请求参数。
// 由 handler 在做 HTTP 解析后填充。
type chatParams struct {
	sessionID       string
	clientMessageID string
	message         string
}

// CreateSessionResult 是 SessionService.CreateSession 的返回值。
// handler 把它转成 JSON 响应体发给客户端。
type CreateSessionResult struct {
	SessionID string // 新创建的会话 ID（UUID）
	UserName  string // 用户名（可能是客户端传的，也可能是默认的 "owner"）
	UserID    int64  // 用户在数据库里的自增 ID
}

// HistoryItem 是一条对话记录，对应数据库 dialogues 表里的一行。
type HistoryItem struct {
	Role      string // 角色："user"（用户说的）或 "assistant"（助手回的）
	Content   string // 消息内容
	Timestamp string // 发生时间，已格式化成可读字符串
}

// HistoryResult 是 HistoryService.GetHistory 的返回值。
type HistoryResult struct {
	SessionID string        // 哪个会话的历史
	Items     []HistoryItem // 对话记录列表，按时间正序排列
}

// Deps 是三个 Service 共用的底层依赖集合。
// service 层不自己创建 repo/idm/mem/llm，而是由 handler 层的 NewDeps 组装好后传进来。
// 字段是小写（包外不可见），外部通过 NewDeps 构造函数创建。
// 小写字段意味着 biz/service 包外面不能直接改这些字段，保证依赖组装只在 NewDeps 里做
type Deps struct {
	cfg  *config.Config
	repo data.Repository
	idm  *identity.Manager
	mem  *memory.Store
	llm  llm.Provider
}

// NewDeps 创建 service 层的依赖集合。
// 由 handler 层的 NewDeps 调，把底层依赖传进来。
// 返回 *Deps 是指针，这样所有用到 Deps 的地方共用同一份实例
func NewDeps(cfg *config.Config, repo data.Repository, idm *identity.Manager, mem *memory.Store, llm llm.Provider) *Deps {
	return &Deps{
		cfg:  cfg,  // 配置
		repo: repo, // 数据仓库接口
		idm:  idm,  // 身份管理器
		mem:  mem,  // 记忆存储
		llm:  llm,  // LLM 客户端
	}
}
