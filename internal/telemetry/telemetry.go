// telemetry.go 放轻量埋点系统:事件写 channel,后台 goroutine 消费.
//
// 做的事情:
//  1. Init:初始化事件 channel 和后台消费 goroutine,设置容量和 Sink.
//  2. Emit:把事件写入 channel(非阻塞,满了丢弃并打 Warn),给业务代码调.
//  3. consume:后台 goroutine 从 channel 读事件,打日志 + 写 Sink(可选 SQLite events 表).
//  4. Shutdown:关闭 channel,等待消费 goroutine 排空剩余事件后退出.
//  5. Stats:返回当前队列中的事件数等运行时统计.
//
// 不直接依赖 data 包(免得循环依赖),通过 EventSink 接口传进来.
// 每个事件携带 trace_id,串联同一次对话的完整调用链.
//
// 什么是埋点:
//   就是在代码关键位置插桩,记录"这里发生了什么".比如 LLM 调用开始/结束,
//   对话发生,记忆查询等.埋点数据写进数据库后可以统计调用次数,耗时,成功率等.
//   这里的设计是异步的--业务代码把事件丢进 channel 就返回了,不等写库,
//   后台 goroutine 慢慢消费写库,不影响业务响应速度.
package telemetry

import (
	// context 给写 Sink 操作加超时,防止写库太慢拖住消费者 goroutine
	"context"
	// encoding/json 把事件 Fields(map)转成 JSON 字符串存进数据库
	"encoding/json"
	// fmt 格式化 error,以及 panic 恢复时把错误信息打到 stderr
	"fmt"
	// os 在 logger 也 panic 的极端情况下,用 stderr 兜底输出
	"os"
	// time 获取事件时间戳,控制 Sink 写超时
	"time"

	// logger 项目自己的日志包,打 Info/Error/Warn 日志
	"github.com/swallow-sun/swallow-go/pkg/logger"
	// zap 结构化日志库,logger 包底层用的就是它.这里用 zap.Field 构建结构化日志字段
	"go.uber.org/zap"

	// trace 从 context 里取 trace ID,每个事件都带上它
	"github.com/swallow-sun/swallow-go/internal/trace"
)

// globalRecorder 是当前进程唯一的 Telemetry 记录器实例.
// 用全局变量的好处:业务代码在任何地方调 telemetry.Emit 就行,不用传来传去传实例.
var globalRecorder recorder

// Init 初始化埋点系统,启动后台消费 goroutine.
// bufSize 是 channel 缓冲大小,满了会丢弃事件(不阻塞业务).
//
// 一般在程序启动时调一次.如果传的 bufSize<=0,用默认值 256.
func Init(bufSize int) {
	globalRecorder.init(bufSize)
}

func (r *recorder) init(bufSize int) {
	// bufSize 传了 0 或负数,用默认容量 256
	if bufSize <= 0 {
		bufSize = DefaultBufferSize
	}

	// stateMu 是读写锁(sync.RWMutex),保护状态字段不被并发踩.
	// 这里用写锁(Lock),因为要改状态字段
	r.stateMu.Lock()
	// defer 解锁,函数返回时自动释放写锁
	defer r.stateMu.Unlock()

	// 已经初始化过就直接返回,不重复创建 channel(否则旧 goroutine 泄漏).
	// 泄漏是指:如果再 make 一个新 channel,旧 goroutine 还在等旧 channel,
	// 但旧 channel 没人写了,它就永远阻塞着,占用一个 goroutine 不释放
	if r.isRunning {
		return
	}

	// 创建带缓冲的 channel,容量是 bufSize(比如 256).
	// 带缓冲的好处:channel 没满时写进去立刻返回,不阻塞;满了才丢弃.
	r.eventChan = make(chan Event, bufSize)
	// 创建关闭信号 channel,用于通知 Shutdown"消费者已经把所有事件处理完了"
	r.shutdownDone = make(chan struct{})
	// 先把 Sink 设成 nil,后面用 SetSink 设置
	r.sink = nil
	// 下面几个是原子计数器(atomic.Int64),记录各种统计数字.
	// .Store(0) 把计数器清零,防止之前初始化过的数据残留
	r.dropCount.Store(0)     // 被丢弃的事件数
	r.acceptedCount.Store(0)  // 成功写进 channel 的事件数
	r.consumedCount.Store(0)  // 消费者已处理的事件数
	r.failedCount.Store(0)   // 处理失败的事件数
	r.panicCount.Store(0)    // 处理时 panic 的事件数
	// 标记正在运行
	r.isRunning = true
	r.isShutdown = false

	// 启动后台 goroutine 消费 channel 里的事件.
	// go 关键字开一个新 goroutine,和主逻辑并发执行.
	// 把当前 channel 和 shutdownDone 作为参数传进去,
	// 而不是直接用 r.eventChan,是为了避免后续重新 init 时读全局新 channel.
	// 比如 init 了两次:第一次 channel A,第二次 channel B,
	// 第一次的 goroutine 应该只消费 A,不应该跳到 B
	go r.consume(r.eventChan, r.shutdownDone)
}

// SetSink 将 EventSink 接口写入全局变量,方便调用
// 传 nil 则只打日志不写 DB.
//
// Sink 是什么:就是事件最终写到哪里.默认只打日志,
// 设置了 Sink 后,事件会写进数据库的 events 表(由 data.EventSinkAdapter 实现).
func SetSink(s EventSink) {
	globalRecorder.setSink(s)
}

func (r *recorder) setSink(s EventSink) {
	// 写锁保护,因为 setSink 可能和消费者 goroutine 同时读写 sink 字段
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.sink = s
}

// Emit 发送一个事件.如果 channel 满了直接丢弃(非阻塞).
// traceID 从 ctx 提取,自动带入事件.
//
// 这个函数是业务代码最常调的,比如:
//
//	telemetry.Emit(ctx, "llm.call", map[string]any{"ms": 1500, "status": "ok"})
//
// 调完立刻返回,不等事件写库,不影响业务响应速度.
func Emit(ctx context.Context, name string, fields map[string]any) {
	globalRecorder.emit(ctx, name, fields)
}

func (r *recorder) emit(ctx context.Context, name string, fields map[string]any) {
	// 读锁覆盖"检查状态 + 发送".Shutdown 必须拿到写锁后才能 close,
	// 因此不会发生检查通过后向已关闭 channel 发送的 panic.
	//
	// 为什么用读锁(RLock)不用写锁(Lock):
	//   emit 是高频操作,多个 goroutine 同时调 emit 是正常的,
	//   只要没人改状态,大家能同时读 isRunning/eventChan,用读锁性能更好.
	//   Shutdown 要改状态(close channel),用写锁互斥,保证 emit 时 channel 不会被 close.
	r.stateMu.RLock()
	// defer 释放读锁
	defer r.stateMu.RUnlock()

	// 没初始化,已经关闭,或者 channel 是 nil,直接返回不做事
	if !r.isRunning || r.isShutdown || r.eventChan == nil {
		return // 没 Init 或已关闭,静默跳过
	}
	// 组装事件
	ev := Event{
		// 事件名,如 "llm.call"
		Name: name,
		// 从 ctx 里掏 trace ID.trace.FromContext 从 context 里取 trace ID,
		// 没有就返回空字符串.这个 ID 是在请求入口处生成并塞进 context 的
		TraceID: trace.FromContext(ctx),
		// 当前时间,记录事件发生的时间点
		Timestamp: time.Now(),
		// 自定义字段,如 token 用量,耗时,状态等
		Fields: fields,
	}
	// select 语句尝试往 channel 写事件,非阻塞方式:
	//   case r.eventChan <- ev:写进去了,acceptedCount+1
	//   default:channel 满了,走默认分支丢弃事件,dropCount+1
	// 这样保证 emit 永远不会阻塞业务,最多丢事件
	select {
	// 塞进去了,返回
	case r.eventChan <- ev:
		// atomic.Int64.Add 原子加1,统计成功写入 channel 的事件数
		r.acceptedCount.Add(1)
	// channel 满了,直接丢弃
	default:
		// 丢弃数 +1,方便通过 Stats 发现"队列太小时满了"
		r.dropCount.Add(1)
	}
}

// consume 是后台消费 goroutine.
// ch 是事件 channel(只读端 <-chan Event),done 是关闭信号 channel(只写端 chan<- struct{}).
//
// 这个 goroutine 在 init 时启动,一直从 ch 读事件处理,直到 ch 被 close.
func (r *recorder) consume(ch <-chan Event, done chan<- struct{}) {
	// defer close(done):consume 退出时关闭 done channel.
	// Shutdown 在等这个 done 信号,收到说明所有事件都处理完了
	defer close(done)

	// for range ch 是 Go 里从 channel 读数据的标准写法:
	//   不断从 ch 读事件,读到就处理;ch 被 close 后,range 结束循环.
	//   这是优雅关闭的核心--close 后消费者会把剩余事件全部处理完再退出
	for ev := range ch {
		// 已处理数 +1
		r.consumedCount.Add(1)
		// 处理这条事件(打日志 + 写库)
		r.processEvent(ev)
	}
}

// processEvent 处理一条事件.每条事件单独 recover,避免单条异常事件
// 杀死唯一消费者并让后续队列永久无人消费.
//
// recover 是什么:Go 里如果一个 goroutine panic 了没被 recover,整个程序崩溃.
// recover 能拦住 panic,让 goroutine 不死.这里给每条事件单独 recover,
// 就算某条事件处理时 panic 了,下一条事件还能继续处理.
func (r *recorder) processEvent(ev Event) {
	// defer + recover:如果 processEvent 里任何地方 panic 了,
	// 这里会拦住,不会让消费者 goroutine 崩溃
	defer func() {
		if recovered := recover(); recovered != nil {
			// panic 次数 +1
			r.panicCount.Add(1)
			// 失败次数 +1
			r.failedCount.Add(1)
			// 为什么用 fmt.Fprintf 到 os.Stderr 而不用 logger:
			// logger 本身也可能是 panic 来源(比如写日志时也 panic),
			// 恢复路径用最原始的 stderr 输出,保证至少能看到错误信息
			_, _ = fmt.Fprintf(os.Stderr, "telemetry event panic: %v\n", recovered)
		}
	}()

	// 构建结构化日志字段.预分配容量为 Fields 数量 +2(时间戳和 trace_id),避免扩容
	fields := make([]zap.Field, 0, len(ev.Fields)+2)
	// 加时间戳字段
	fields = append(fields, zap.Time("ts", ev.Timestamp))
	// trace ID 非空才加,避免日志里出现空 trace_id
	if ev.TraceID != "" {
		fields = append(fields, zap.String("trace_id", ev.TraceID))
	}
	// 把事件自带的 Fields(map[string]any)逐个转成 zap.Field 加进去
	// zap.Any 把任意类型转成日志字段,不挑类型
	for k, v := range ev.Fields {
		fields = append(fields, zap.Any(k, v))
	}
	// 打 Info 日志,前缀加 [telemetry] 区分业务日志
	logger.Info("[telemetry] "+ev.Name, fields...)

	// 读锁检查有没有设置 Sink.用读锁因为只是读不写
	r.stateMu.RLock()
	currentSink := r.sink
	r.stateMu.RUnlock()
	// 没设 Sink 就只打日志不写库,直接返回
	if currentSink == nil {
		return
	}

	// 把事件的 Fields(map)转成 JSON 字符串,写进数据库的 data 字段
	data, err := json.Marshal(ev.Fields)
	if err != nil {
		// JSON 转换失败,失败数 +1,打日志
		r.failedCount.Add(1)
		logger.Error("Failed to serialize telemetry event",
			zap.String("event_type", ev.Name),
			zap.String("trace_id", ev.TraceID),
			zap.Error(err),
		)
		return
	}

	// 从 Fields 里取耗时毫秒数(如果有的话)
	durationMs := extractInt(ev.Fields, FieldDurationMS)
	// 从 Fields 里取状态值,判断这次事件是成功还是失败
	success := ev.Fields[FieldStatus] != StatusError

	// 给写库操作加超时,防止写库太慢拖住唯一消费者 goroutine.
	// context.WithTimeout 创建一个 3 秒超时的 context,
	// 到 3 秒还没写完就自动取消,消费者 goroutine 可以继续处理下一条事件
	writeCtx, cancel := context.WithTimeout(context.Background(), DefaultSinkWriteTimeout)
	// defer cancel() 在函数返回时释放 context 关联的资源
	// 这里在 WriteEvent 返回后立刻调 cancel,不 defer,因为后面还有逻辑
	err = currentSink.WriteEvent(writeCtx, ev.Name, ev.TraceID, string(data), durationMs, success)
	cancel() // 手动调 cancel,尽早释放资源
	if err != nil {
		// 写库失败,失败数 +1,打日志
		r.failedCount.Add(1)
		logger.Error("Failed to persist telemetry event",
			zap.String("event_type", ev.Name),
			zap.String("trace_id", ev.TraceID),
			zap.Error(err),
		)
	}
}

// Snapshot 返回统计值副本,给健康检查,看板和测试用.
// 调完拿到当前队列里有多少事件,总共处理了多少,丢了多少等统计.
func Snapshot() Stats {
	return globalRecorder.snapshot()
}

func (r *recorder) snapshot() Stats {
	// 读锁保护读 channel 长度和容量.len() 和 cap() 对 channel 是线程安全的,
	// 但和 init/shutdown 改 channel 时可能冲突,所以加读锁
	r.stateMu.RLock()
	queued := 0   // 当前队列里还没处理的事件数
	capacity := 0 // channel 容量
	if r.eventChan != nil {
		// len(r.eventChan) 返回 channel 里当前有多少事件还没被消费
		queued = len(r.eventChan)
		// cap(r.eventChan) 返回 channel 的缓冲容量
		capacity = cap(r.eventChan)
	}
	r.stateMu.RUnlock()

	// 组装统计快照返回.
	// atomic.Int64.Load() 原子读取当前值,安全地在读端使用
	return Stats{
		Accepted: r.acceptedCount.Load(),  // 成功写进 channel 的总数
		Consumed: r.consumedCount.Load(),  // 消费者已处理的总数
		Dropped:  r.dropCount.Load(),      // 被丢弃的总数
		Failed:   r.failedCount.Load(),     // 处理失败的总数
		Panics:   r.panicCount.Load(),     // 处理时 panic 的总数
		Queued:   queued,                  // 当前队列积压数
		Capacity: capacity,               // 队列容量
	}
}

// 这是个从 map 里安全取整数的小工具函数
// 为什么需要它:Fields 是 map[string]any,值类型是 any(interface{}),
// 比如耗时可能存成 int 也可能存成 int64,直接用会出错,需要安全转换.
func extractInt(m map[string]any, key string) int64 {
	// Go 里从 map 取值有个习惯写法:返回两个值--v 是值,ok 是"取没取到"的布尔
	v, ok := m[key]
	// key 不存在,直接返回 0
	if !ok {
		return 0
	}

	// type switch:根据值的具体类型走不同分支
	switch n := v.(type) {
	// 类型恰好是 int64,直接用
	case int64:
		return n
	// 是 int,转成 int64 再返回
	case int:
		return int64(n)
	}
	// 类型既不是 int 也不是 int64(比如 string,float),返回 0
	return 0
}

// Shutdown 停止接收新事件,等待队列中已有事件全部消费完成.
// ctx 用于控制最长等待时间,超时返回 error.
//
// 一般在程序退出时调.如果不调 Shutdown 直接退出,
// channel 里还没消费的事件就丢了.
func Shutdown(ctx context.Context) error {
	return globalRecorder.shutdown(ctx)
}

func (r *recorder) shutdown(ctx context.Context) error {
	// 写锁保护,因为要改状态(标记 isShutdown,close channel)
	r.stateMu.Lock()
	// 没初始化或 channel 是 nil,没什么好关的,直接返回
	if !r.isRunning || r.eventChan == nil {
		r.stateMu.Unlock()
		return nil
	}

	// 第一次调用负责关闭 channel;重复调用等待同一个完成信号.
	// close(r.eventChan) 关闭 channel 后,emit 再写会 panic,但 consume 的 for range 会正常退出.
	// isShutdown 标记防止重复 close(重复 close 一个 channel 会 panic)
	if !r.isShutdown {
		r.isShutdown = true
		close(r.eventChan)
	}

	// 拿到本次的 done channel,释放写锁后等它
	done := r.shutdownDone
	r.stateMu.Unlock()

	// select 同时等两个 channel:
	//   done:消费者把所有事件处理完了,正常关闭
	//   ctx.Done():等待超时了,消费者还没处理完,返回超时错误
	select {
	case <-done:
		return nil // 正常消费完,所有事件都处理了
	case <-ctx.Done():
		// 超时了,看看还剩多少事件没处理
		r.stateMu.RLock()
		var remaining int64
		if r.eventChan != nil {
			remaining = int64(len(r.eventChan))
		}
		r.stateMu.RUnlock()
		// 返回超时错误,把剩余事件数 + 丢弃数一起报出来
		return fmt.Errorf("telemetry shutdown timed out, %d events pending or dropped",
			remaining+r.dropCount.Load())
	}
}
