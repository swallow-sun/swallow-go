// hertz.go 放 Hertz 框架日志适配器，把框架内部日志转发到项目全局 logger。
//
// 做的事情：
//  1. NewHertzAdapter：创建适配器，生产环境过滤 Debug/Trace 级别日志。
//  2. 实现全级别日志方法：Trace/Debug/Info/Notice/Warn/Error/Fatal 及其 Format 版本和带 context 版本。
//  3. 所有方法通过 write 统一出口：级别过滤后加上 "HERTZ: " 前缀转发到 logger。
//  4. SetLevel/SetOutput：支持运行时调整级别和输出目标。
package logger

import (
	"context"
	"fmt"
	"io"

	"github.com/cloudwego/hertz/pkg/common/hlog"
)

// NewHertzAdapter 创建直接使用项目全局 logger 的 Hertz 日志适配器。
func NewHertzAdapter() *HertzAdapter {
	level := hlog.LevelDebug
	if IsProduction() {
		level = hlog.LevelInfo
	}
	return &HertzAdapter{level: level}
}

// enabled 判断 Hertz 日志级别是否达到当前环境允许输出的最低级别。
func (l *HertzAdapter) enabled(level hlog.Level) bool { return level >= l.level }

// write 为所有 Hertz 日志方法的唯一出口，负责级别过滤、来源标识和统一转发。
func (l *HertzAdapter) write(level hlog.Level, message string) {
	if !l.enabled(level) {
		return
	}
	message = "HERTZ: " + message
	switch level {
	case hlog.LevelTrace, hlog.LevelDebug:
		Debug(message)
	case hlog.LevelInfo, hlog.LevelNotice:
		Info(message)
	case hlog.LevelWarn:
		Warn(message)
	case hlog.LevelError:
		Error(message)
	case hlog.LevelFatal:
		L().Fatal(message)
	default:
		Warn(message)
	}
}

// Trace 接收 Hertz 最细粒度的跟踪日志，并按 Debug 级别转发。
func (l *HertzAdapter) Trace(v ...interface{}) { l.write(hlog.LevelTrace, fmt.Sprint(v...)) }

// Debug 接收 Hertz 开发诊断日志；生产环境会过滤此级别。
func (l *HertzAdapter) Debug(v ...interface{}) { l.write(hlog.LevelDebug, fmt.Sprint(v...)) }

// Info 接收 Hertz 正常运行日志，并转发到项目 Info 日志。
func (l *HertzAdapter) Info(v ...interface{}) { l.write(hlog.LevelInfo, fmt.Sprint(v...)) }

// Notice 接收 Hertz 提示日志；项目没有 Notice 级别，因此按 Info 转发。
func (l *HertzAdapter) Notice(v ...interface{}) { l.write(hlog.LevelNotice, fmt.Sprint(v...)) }

// Warn 接收 Hertz 可恢复异常日志，并转发到项目 Warn 日志。
func (l *HertzAdapter) Warn(v ...interface{}) { l.write(hlog.LevelWarn, fmt.Sprint(v...)) }

// Error 接收 Hertz 运行错误，并转发到项目 Error 日志。
func (l *HertzAdapter) Error(v ...interface{}) { l.write(hlog.LevelError, fmt.Sprint(v...)) }

// Fatal 记录 Hertz 致命错误，随后由 Zap 终止当前进程。
func (l *HertzAdapter) Fatal(v ...interface{}) { l.write(hlog.LevelFatal, fmt.Sprint(v...)) }

// Tracef 格式化并转发 Hertz 跟踪日志。
func (l *HertzAdapter) Tracef(format string, v ...interface{}) {
	l.write(hlog.LevelTrace, fmt.Sprintf(format, v...))
}

// Debugf 格式化并转发 Hertz 开发诊断日志。
func (l *HertzAdapter) Debugf(format string, v ...interface{}) {
	l.write(hlog.LevelDebug, fmt.Sprintf(format, v...))
}

// Infof 格式化并转发 Hertz 正常运行日志。
func (l *HertzAdapter) Infof(format string, v ...interface{}) {
	l.write(hlog.LevelInfo, fmt.Sprintf(format, v...))
}

// Noticef 格式化 Hertz 提示日志，并按 Info 级别转发。
func (l *HertzAdapter) Noticef(format string, v ...interface{}) {
	l.write(hlog.LevelNotice, fmt.Sprintf(format, v...))
}

// Warnf 格式化并转发 Hertz 可恢复异常日志。
func (l *HertzAdapter) Warnf(format string, v ...interface{}) {
	l.write(hlog.LevelWarn, fmt.Sprintf(format, v...))
}

// Errorf 格式化并转发 Hertz 运行错误。
func (l *HertzAdapter) Errorf(format string, v ...interface{}) {
	l.write(hlog.LevelError, fmt.Sprintf(format, v...))
}

// Fatalf 格式化 Hertz 致命错误，记录后终止当前进程。
func (l *HertzAdapter) Fatalf(format string, v ...interface{}) {
	l.write(hlog.LevelFatal, fmt.Sprintf(format, v...))
}

// CtxTracef 实现 Hertz 上下文跟踪日志接口；当前公共字段由全局 logger 带上。
func (l *HertzAdapter) CtxTracef(_ context.Context, format string, v ...interface{}) {
	l.Tracef(format, v...)
}

// CtxDebugf 实现 Hertz 上下文 Debug 日志接口。
func (l *HertzAdapter) CtxDebugf(_ context.Context, format string, v ...interface{}) {
	l.Debugf(format, v...)
}

// CtxInfof 实现 Hertz 上下文 Info 日志接口。
func (l *HertzAdapter) CtxInfof(_ context.Context, format string, v ...interface{}) {
	l.Infof(format, v...)
}

// CtxNoticef 实现 Hertz 上下文 Notice 日志接口，并按 Info 转发。
func (l *HertzAdapter) CtxNoticef(_ context.Context, format string, v ...interface{}) {
	l.Noticef(format, v...)
}

// CtxWarnf 实现 Hertz 上下文 Warn 日志接口。
func (l *HertzAdapter) CtxWarnf(_ context.Context, format string, v ...interface{}) {
	l.Warnf(format, v...)
}

// CtxErrorf 实现 Hertz 上下文 Error 日志接口。
func (l *HertzAdapter) CtxErrorf(_ context.Context, format string, v ...interface{}) {
	l.Errorf(format, v...)
}

// CtxFatalf 实现 Hertz 上下文 Fatal 日志接口，记录后终止当前进程。
func (l *HertzAdapter) CtxFatalf(_ context.Context, format string, v ...interface{}) {
	l.Fatalf(format, v...)
}

// SetLevel 接收 Hertz 的动态级别设置，并更新适配器的最低输出级别。
func (l *HertzAdapter) SetLevel(level hlog.Level) { l.level = level }

// SetOutput 保留 Hertz 接口兼容性；输出目标由项目 logger 统一管理。
func (l *HertzAdapter) SetOutput(_ io.Writer) {}

var _ hlog.FullLogger = (*HertzAdapter)(nil)
