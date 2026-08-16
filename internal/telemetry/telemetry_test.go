package telemetry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
)

// blockingSink 是测试用事件接收器。
// 第一个事件进入 WriteEvent 后会阻塞，测试借此占住消费者并填满队列，
// 从而稳定验证队列溢出，而不是依赖 goroutine 调度速度。
type blockingSink struct {
	mu sync.Mutex

	events  []string
	entered chan struct{}
	release chan struct{}
	written chan struct{}
	once    sync.Once
}

func newBlockingSink() *blockingSink {
	return &blockingSink{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		written: make(chan struct{}, 128),
	}
}

func (s *blockingSink) WriteEvent(
	ctx context.Context,
	eventType string,
	traceID string,
	data string,
	durationMs int64,
	success bool,
) error {
	if eventType == "panic-event" {
		panic("测试 Sink panic")
	}
	if eventType == "failed-event" {
		return errors.New("测试 Sink 写入失败")
	}

	// 只阻塞第一次写入。通知测试消费者已经取走第一个事件，
	// 此时容量为 1 的 eventChan 已经空出，可以精确放入第二个事件。
	s.once.Do(func() {
		close(s.entered)
	})
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}

	s.mu.Lock()
	s.events = append(s.events, eventType)
	s.mu.Unlock()

	// 通知测试一条事件已经完整写入 Sink。
	s.written <- struct{}{}
	return nil
}

func (s *blockingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

// TestTelemetryLifecycle 在同一个 Telemetry 生命周期中验证：
//
//  1. 队列满时增加丢弃计数；
//  2. Shutdown 会刷新已经成功入队的事件；
//  3. Emit 与 Shutdown 并发时不会向已关闭 channel 发送；
//  4. Shutdown 可以安全重复调用。
//
// Telemetry 当前是进程级单例，关闭后不允许重新 Init，
// 因此相关场景集中在一个测试中，避免多个测试互相污染全局生命周期。
func TestTelemetryLifecycle(t *testing.T) {
	if err := logger.Init(); err != nil {
		t.Fatalf("初始化测试日志失败: %v", err)
	}

	sink := newBlockingSink()
	Init(1)
	SetSink(sink)

	ctx := context.Background()

	// 第一个事件被消费者取走后阻塞在 Sink 中。
	Emit(ctx, "event-1", map[string]any{"status": "ok"})
	select {
	case <-sink.entered:
	case <-time.After(time.Second):
		t.Fatal("消费者未在规定时间内进入 Sink")
	}

	// 此时消费者被阻塞：第二个事件占满容量为 1 的队列，
	// 第三个事件必须走 default 分支并被计入 dropCount。
	Emit(ctx, "event-2", map[string]any{"status": "ok"})
	Emit(ctx, "event-3-dropped", map[string]any{"status": "ok"})

	if got := Snapshot().Dropped; got != 1 {
		t.Fatalf("丢弃事件数 = %d，期望 1", got)
	}

	// 释放 Sink，等待前两条成功入队的事件真正写完。
	close(sink.release)
	for i := 0; i < 2; i++ {
		select {
		case <-sink.written:
		case <-time.After(time.Second):
			t.Fatalf("等待第 %d 条事件写入超时", i+1)
		}
	}

	// 单条事件的 Sink panic 必须被 processEvent 隔离，消费者随后仍能处理事件。
	Emit(ctx, "panic-event", map[string]any{"status": "ok"})
	deadline := time.Now().Add(time.Second)
	for Snapshot().Panics < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if stats := Snapshot(); stats.Panics != 1 || stats.Failed < 1 {
		t.Fatalf("panic 统计不正确: %+v", stats)
	}

	// Sink 返回 error 时记录失败，但不终止消费者。
	Emit(ctx, "failed-event", map[string]any{"status": "ok"})
	deadline = time.Now().Add(time.Second)
	for Snapshot().Failed < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if stats := Snapshot(); stats.Failed < 2 {
		t.Fatalf("Sink 写入失败未被统计: %+v", stats)
	}

	// panic 和 error 之后再写一条正常事件，证明唯一消费者仍然存活。
	Emit(ctx, "event-after-failure", map[string]any{"status": "ok"})
	select {
	case <-sink.written:
	case <-time.After(time.Second):
		t.Fatal("消费者在单条事件失败后没有继续处理后续事件")
	}

	// 同时启动多个 Emit 和一个 Shutdown。
	// 如果状态检查与 close 没有被同一把锁保护，这里可能触发
	// send on closed channel。测试只要求所有 goroutine 能正常返回。
	const emitterCount = 32
	start := make(chan struct{})
	var emitWG sync.WaitGroup
	emitWG.Add(emitterCount)

	for i := 0; i < emitterCount; i++ {
		go func() {
			defer emitWG.Done()
			<-start
			Emit(ctx, "concurrent-event", map[string]any{"status": "ok"})
		}()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	shutdownResult := make(chan error, 1)
	go func() {
		<-start
		shutdownResult <- Shutdown(shutdownCtx)
	}()

	close(start)
	emitWG.Wait()

	select {
	case err := <-shutdownResult:
		if err != nil {
			t.Fatalf("第一次 Shutdown 失败: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("等待第一次 Shutdown 返回超时")
	}

	// 无论并发事件有多少被接受，关闭前已确认入队的前两条必须落库。
	if got := sink.count(); got < 2 {
		t.Fatalf("Sink 收到 %d 条事件，至少应包含关闭前的 2 条", got)
	}

	// 重复关闭必须直接等待同一个已完成信号并成功返回，不能再次 close。
	repeatCtx, repeatCancel := context.WithTimeout(context.Background(), time.Second)
	defer repeatCancel()
	if err := Shutdown(repeatCtx); err != nil {
		t.Fatalf("重复 Shutdown 失败: %v", err)
	}

	// 关闭后的 Emit 应被忽略，不能写入 Sink 或触发 panic。
	before := sink.count()
	Emit(ctx, "after-shutdown", map[string]any{"status": "ok"})
	if after := sink.count(); after != before {
		t.Fatalf("关闭后 Emit 仍写入事件：关闭前 %d，关闭后 %d", before, after)
	}
}
