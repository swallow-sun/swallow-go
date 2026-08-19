// types.go 放 telemetry 包的类型定义和常量.
//
// 做的事情:
//  1. 定义对话、模型、记忆查询、记忆确认和记忆安全拒绝等事件类型常量.
//  2. 定义通用字段名和状态值常量:status(ok/error/connected),ms(耗时毫秒).
//  3. 定义 Event 结构体:一条埋点事件,包含类型,数据,trace ID,时间戳等.
//  4. 定义 EventSink 接口:写库用的 Sink,由 data.EventSinkAdapter 实现,免得 telemetry 直接依赖 data 包.
//  5. 定义 Stats 和 recorder:原子计数器统计事件数,用于运行时观察埋点队列状态.
package telemetry

import (
	// context 给 EventSink 接口的方法参数用,控制写库超时
	"context"
	// sync 给 recorder 用 sync.RWMutex 读写锁,保护并发访问状态字段
	"sync"
	// sync/atomic 给 recorder 用 atomic.Int64 原子计数器,统计事件数
	"sync/atomic"
	// time 给 Event.Timestamp 和 DefaultSinkWriteTimeout 用
	"time"
)

const (
	// DefaultBufferSize 是调用 Init 时没传有效容量时用的默认队列长度.
	// 比如调 Init(0) 或 Init(-1),就用 256
	DefaultBufferSize = 256

	// DefaultSinkWriteTimeout 是单条事件写进数据库的最长等待时间.
	// 3 秒还没写完就超时放弃,不拖住消费者 goroutine
	DefaultSinkWriteTimeout = 3 * time.Second

	// 下面是事件类型常量, 业务代码调 Emit 时传的 name 参数.
	// 方案 16.10.1 节要求的最先支持的 6 种事件类型:
	//   session_created          — 会话创建
	//   message_received         — 收到用户消息
	//   model_request_started    — 模型调用开始
	//   model_request_completed  — 模型调用完成
	//   model_request_failed     — 模型调用失败
	//   message_completed        — 整轮消息处理完成
	//
	// 另外保留两个业务事件(方案 15.2 节: "现有 events 表继续作为业务事件入口"):
	//   dialogue     — 对话消息持久化
	//   memory_query — 记忆查询
	EventSessionCreated        = "session_created"         // 会话创建事件: 用户登录并创建新会话
	EventMessageReceived       = "message_received"        // 消息接收事件: 服务端收到用户发来的消息
	EventModelRequestStarted   = "model_request_started"   // 模型调用开始事件: 开始调模型供应商
	EventModelRequestCompleted = "model_request_completed" // 模型调用完成事件: 模型返回结果
	EventModelRequestFailed    = "model_request_failed"    // 模型调用失败事件: 模型调用出错
	EventMessageCompleted      = "message_completed"       // 消息处理完成事件: 整轮对话结束

	EventDialogue               = "dialogue"                 // 对话事件: 用户发消息或助手回复(业务事件, 非 6 种之一)
	EventMemoryQuery            = "memory_query"             // 记忆查询事件: 检索长期记忆(业务事件, 非 6 种之一)
	EventMemoryConfirmed        = "memory_confirmed"         // 记忆确认事件: 候选确认后写正式记忆(方案 16.11.3 节)
	EventMemoryCandidateBlocked = "memory_candidate_blocked" // 记忆候选安全拒绝事件: 只记录敏感类别, 不记录原文

	// 下面是通用事件字段名,放在 Fields map 里的 key.
	FieldStatus     = "status" // 状态字段:值是 ok/error/connected
	FieldDurationMS = "ms"     // 耗时字段:值是毫秒数

	// 下面是通用事件状态值,放在 Fields map[FieldStatus] 里的值.
	StatusOK        = "ok"        // 成功
	StatusError     = "error"     // 失败
	StatusConnected = "connected" // 连接成功(如 WebSocket 连上)
	StatusRejected  = "rejected"  // 被业务安全策略拒绝
)

// Event 是一条等待记录的埋点事件.
// TraceID 用于关联同一请求产生的日志,对话和其他事件;
// Fields 只保存该事件特有的扩展字段.
//
// 比如一条 LLM 调用事件:
//
//	Event{
//	    Name:      "llm.call",
//	    TraceID:   "a1b2c3d4-...",
//	    Timestamp: time.Now(),
//	    Fields:    map[string]any{"ms": 1500, "status": "ok", "model": "deepseek-chat"},
//	}
type Event struct {
	Name      string         // 事件类型名,如 "llm.call"
	TraceID   string         // 链路追踪 ID,串联同一次请求的所有事件
	Timestamp time.Time      // 事件发生时间
	Fields    map[string]any // 事件自定义字段,不同事件类型字段不同
}

// EventSink 是事件写库的目标接口.
// 由 data.EventSinkAdapter 实现(把事件写进 SQLite events 表).
//
// 为什么用接口而不用直接调 data 包:
//
//	telemetry 是底层包,data 是上层包.如果 telemetry 直接 import data,
//	data 又 import telemetry(因为 data 要调 Emit 打埋点),就循环依赖了.
//	用接口把方向反过来--data 实现 EventSink,telemetry 调接口,不依赖 data.
//
// 实现必须用传进来的 Context 做阻塞操作,Context 取消后赶紧返回.
// 返回 error 说明这次事件没写成功.
type EventSink interface {
	// WriteEvent 把一条事件写进数据库.
	// ctx 控制超时,eventType 是事件名,traceID 是链路 ID,
	// data 是 Fields 转成的 JSON 字符串,durationMs 是耗时毫秒,success 是成功还是失败
	WriteEvent(
		ctx context.Context,
		eventType string,
		traceID string,
		data string,
		durationMs int64,
		success bool,
	) error
}

// Stats 是 Telemetry 当前生命周期的统计快照.
// 调 Snapshot() 拿到当前各项计数,给健康检查,看板和测试用.
type Stats struct {
	Accepted int64 // 成功写进 channel 的事件总数
	Consumed int64 // 消费者已处理的事件总数
	Dropped  int64 // 被丢弃的事件总数(channel 满了时丢的)
	Failed   int64 // 处理失败的事件总数(写库失败,序列化失败,panic)
	Panics   int64 // 处理时 panic 的事件总数
	Queued   int   // 当前队列里还没处理的事件数
	Capacity int   // channel 的缓冲容量
}

// recorder 持有 Telemetry 在一个进程生命周期中的全部运行状态.
// 类型定义集中在 types.go;状态初始化,消费和关闭逻辑在 telemetry.go.
//
// 全局唯一实例 globalRecorder 在 telemetry.go 里定义.
type recorder struct {
	// stateMu 是读写锁,保护下面的状态字段不被并发踩.
	// emit 用读锁(RLock),init/shutdown/setSink 用写锁(Lock)
	stateMu sync.RWMutex

	eventChan    chan Event    // 事件 channel,业务代码往里写,消费者 goroutine 从里读
	sink         EventSink     // 写库目标,nil 表示只打日志不写库
	shutdownDone chan struct{} // 关闭信号 channel,消费者处理完所有事件后 close 它

	// 下面五个是原子计数器(atomic.Int64),多个 goroutine 同时读写不用加锁.
	// .Add(n) 原子加 n,.Load() 原子读当前值,.Store(n) 原子设值
	dropCount     atomic.Int64 // 被丢弃的事件数
	acceptedCount atomic.Int64 // 写进 channel 的事件数
	consumedCount atomic.Int64 // 消费者已处理的事件数
	failedCount   atomic.Int64 // 处理失败的事件数
	panicCount    atomic.Int64 // 处理时 panic 的事件数

	isRunning  bool // 是否已初始化并正在运行
	isShutdown bool // 是否已关闭(防止重复 close channel)
}
