// Package telemetry 做轻量埋点。
// 事件写 channel，后台 goroutine 消费：打日志 + 写 SQLite events 表（可选）。
// 不直接依赖 data 包（避免循环依赖），通过 EventSink 接口注入。
// 每个事件携带 trace_id，串联同一次对话的完整调用链。
package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"

	"github.com/swallow-sun/swallow-go/internal/trace"
)

// globalRecorder 是当前进程唯一的 Telemetry 记录器实例。
var globalRecorder recorder

// Init 初始化埋点系统，启动后台消费 goroutine。
// bufSize 是 channel 缓冲大小，满了会丢弃事件（不阻塞业务）。
func Init(bufSize int) {
	globalRecorder.init(bufSize)
}

func (r *recorder) init(bufSize int) {
	if bufSize <= 0 {
		bufSize = DefaultBufferSize
	}

	r.stateMu.Lock()
	defer r.stateMu.Unlock()

	// 已经初始化过就直接返回，不重复创建 channel（否则旧 goroutine 泄漏）。
	if r.isRunning {
		return
	}

	r.eventChan = make(chan Event, bufSize)
	r.shutdownDone = make(chan struct{})
	r.sink = nil
	r.dropCount.Store(0)
	r.acceptedCount.Store(0)
	r.consumedCount.Store(0)
	r.failedCount.Store(0)
	r.panicCount.Store(0)
	r.isRunning = true
	r.isShutdown = false

	// 把本次 channel 传给消费协程，避免后续重新初始化时读取全局新 channel。
	go r.consume(r.eventChan, r.shutdownDone)
}

// SetSink 将 EventSink 接口写入全局变量，方便调用
// 传 nil 则只打日志不写 DB。
func SetSink(s EventSink) {
	globalRecorder.setSink(s)
}

func (r *recorder) setSink(s EventSink) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.sink = s
}

// Emit 发送一个事件。如果 channel 满了直接丢弃（非阻塞）。
// traceID 从 ctx 提取，自动带入事件。
func Emit(ctx context.Context, name string, fields map[string]any) {
	globalRecorder.emit(ctx, name, fields)
}

func (r *recorder) emit(ctx context.Context, name string, fields map[string]any) {
	// 读锁覆盖“检查状态 + 发送”。Shutdown 必须取得写锁后才能 close，
	// 因此不会发生检查通过后向已关闭 channel 发送的 panic。
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()

	if !r.isRunning || r.isShutdown || r.eventChan == nil {
		return // 没 Init 或已关闭，静默跳过
	}
	// 组装事件
	ev := Event{
		// 事件名，如 "llm.call"
		Name: name,
		// 从 ctx 里掏 trace ID
		TraceID: trace.FromContext(ctx),
		// 当前时间
		Timestamp: time.Now(),
		// 自定义字段，如 token 用量、耗时
		Fields: fields,
	}
	select {
	// 塞进去了，返回
	case r.eventChan <- ev:
		r.acceptedCount.Add(1)
	// channel 满了，直接丢弃
	default:
		r.dropCount.Add(1)
	}
}

// consume 是后台消费 goroutine。
func (r *recorder) consume(ch <-chan Event, done chan<- struct{}) {
	defer close(done)

	for ev := range ch {
		r.consumedCount.Add(1)
		r.processEvent(ev)
	}
}

// processEvent 处理一条事件。每条事件单独 recover，避免单条异常事件
// 杀死唯一消费者并让后续队列永久无人消费。
func (r *recorder) processEvent(ev Event) {
	defer func() {
		if recovered := recover(); recovered != nil {
			r.panicCount.Add(1)
			r.failedCount.Add(1)
			// logger 本身也可能是 panic 来源，恢复路径使用 stderr 兜底。
			_, _ = fmt.Fprintf(os.Stderr, "telemetry event panic: %v\n", recovered)
		}
	}()

	fields := make([]zap.Field, 0, len(ev.Fields)+2)
	fields = append(fields, zap.Time("ts", ev.Timestamp))
	if ev.TraceID != "" {
		fields = append(fields, zap.String("trace_id", ev.TraceID))
	}
	for k, v := range ev.Fields {
		fields = append(fields, zap.Any(k, v))
	}
	logger.Info("[telemetry] "+ev.Name, fields...)

	r.stateMu.RLock()
	currentSink := r.sink
	r.stateMu.RUnlock()
	if currentSink == nil {
		return
	}

	data, err := json.Marshal(ev.Fields)
	if err != nil {
		r.failedCount.Add(1)
		logger.Error("序列化埋点事件失败",
			zap.String("event_type", ev.Name),
			zap.String("trace_id", ev.TraceID),
			zap.Error(err),
		)
		return
	}

	durationMs := extractInt(ev.Fields, FieldDurationMS)
	success := ev.Fields[FieldStatus] != StatusError

	// Repository 使用传入的 context 执行 GORM 操作。达到期限后数据库调用
	// 应尽快返回，防止单次写入无限占住唯一消费者。
	writeCtx, cancel := context.WithTimeout(context.Background(), DefaultSinkWriteTimeout)
	err = currentSink.WriteEvent(writeCtx, ev.Name, ev.TraceID, string(data), durationMs, success)
	cancel()
	if err != nil {
		r.failedCount.Add(1)
		logger.Error("持久化埋点事件失败",
			zap.String("event_type", ev.Name),
			zap.String("trace_id", ev.TraceID),
			zap.Error(err),
		)
	}
}

// Snapshot 返回统计值副本，供健康检查、看板和测试使用。
func Snapshot() Stats {
	return globalRecorder.snapshot()
}

func (r *recorder) snapshot() Stats {
	r.stateMu.RLock()
	queued := 0
	capacity := 0
	if r.eventChan != nil {
		queued = len(r.eventChan)
		capacity = cap(r.eventChan)
	}
	r.stateMu.RUnlock()

	return Stats{
		Accepted: r.acceptedCount.Load(),
		Consumed: r.consumedCount.Load(),
		Dropped:  r.dropCount.Load(),
		Failed:   r.failedCount.Load(),
		Panics:   r.panicCount.Load(),
		Queued:   queued,
		Capacity: capacity,
	}
}

// 这是个从 map 里安全取整数的小工具函数
func extractInt(m map[string]any, key string) int64 {
	// Go 里从 map 取值有个习惯写法：返回两个值——v 是值，ok 是"取没取到"的布尔
	v, ok := m[key]
	// key 不存在，直接返回 0
	if !ok {
		return 0
	}

	switch n := v.(type) {
	// 类型恰好是 int64，直接用
	case int64:
		return n
	// 是 int，转成 int64 再返回
	case int:
		return int64(n)
	}
	// 类型既不是 int 也不是 int64（比如 string、float），返回 0
	return 0
}

// Shutdown 停止接收新事件，等待队列中已有事件全部消费完成。
// ctx 用于控制最长等待时间，超时返回 error。
func Shutdown(ctx context.Context) error {
	return globalRecorder.shutdown(ctx)
}

func (r *recorder) shutdown(ctx context.Context) error {
	r.stateMu.Lock()
	if !r.isRunning || r.eventChan == nil {
		r.stateMu.Unlock()
		return nil
	}

	// 第一次调用负责关闭 channel；重复调用等待同一个完成信号。
	if !r.isShutdown {
		r.isShutdown = true
		close(r.eventChan)
	}

	done := r.shutdownDone
	r.stateMu.Unlock()

	select {
	case <-done:
		return nil // 正常消费完
	case <-ctx.Done():
		r.stateMu.RLock()
		var remaining int64
		if r.eventChan != nil {
			remaining = int64(len(r.eventChan))
		}
		r.stateMu.RUnlock()
		return fmt.Errorf("telemetry shutdown timed out, %d events pending or dropped",
			remaining+r.dropCount.Load())
	}
}
