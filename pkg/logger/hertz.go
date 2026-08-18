// hertz.go 放 Hertz 框架日志适配器,把框架内部日志转发到项目全局 logger.
//
// 做的事情:
//  1. NewHertzAdapter:创建适配器,级别固定 Info,过滤掉 Debug/Trace(主要是路由注册日志,格式吵且改不了).
//  2. 实现全级别日志方法:Trace/Debug/Info/Notice/Warn/Error/Fatal 及其 Format 版本和带 context 版本.
//  3. 所有方法通过 write 统一出口:级别过滤后加上 "HERTZ: " 前缀转发到 logger.
//  4. SetLevel/SetOutput:支持运行时调整级别和输出目标.
package logger

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/hertz/pkg/common/hlog"
)

// NewHertzAdapter 创建直接使用项目全局 logger 的 Hertz 日志适配器.
// 返回的 *HertzAdapter 实现了 hlog.FullLogger 接口,可以替换 Hertz 默认的日志输出.
// 级别固定设为 Info:Hertz 的 Debug 日志主要是路由注册信息(格式很吵且改不了),
// 对开发没帮助;项目自己的 Debug 日志走 zap 直接输出,不走 hlog,不受影响.
func NewHertzAdapter() *HertzAdapter {
	return &HertzAdapter{level: hlog.LevelInfo}
}

// enabled 判断 Hertz 日志级别是否达到当前环境允许输出的最低级别.
// hlog 的级别是数字常量,数字越大级别越高:
//   LevelTrace(0) < LevelDebug(1) < LevelInfo(2) < LevelNotice(3)
//   < LevelWarn(4) < LevelError(5) < LevelFatal(6)
// 如果传进来的 level >= l.level,说明这条日志级别够高,允许输出.
// 比如当前 l.level 是 Info(2),那么 Debug(1) < 2 就不输出,Info(2) >= 2 就输出.
func (l *HertzAdapter) enabled(level hlog.Level) bool { return level >= l.level }

// write 为所有 Hertz 日志方法的唯一出口,负责级别过滤,来源标识和统一转发.
// level 是这条日志的级别,message 是日志内容.
func (l *HertzAdapter) write(level hlog.Level, message string) {
	// 先判断这条日志级别够不够高,不够就直接 return,不打
	if !l.enabled(level) {
		return
	}
	// 在消息前面加上 "HERTZ: " 前缀,这样看日志的时候一眼能分清
	// 哪些是业务代码打的,哪些是 Hertz 框架内部打的
	// Hertz 框架自己打的路由注册日志已经带了 "HERTZ: " 前缀,不要再重复加
	if !strings.HasPrefix(message, "HERTZ: ") {
		message = "HERTZ: " + message
	}
	// 按 Hertz 的级别映射到我们项目 logger 的级别
	// Hertz 有 7 个级别,我们项目只有 4 个(Debug/Info/Warn/Error),需要合并
	switch level {
	case hlog.LevelTrace, hlog.LevelDebug:
		// Hertz 的 Trace 和 Debug 都映射到我们的 Debug
		Debug(message)
	case hlog.LevelInfo, hlog.LevelNotice:
		// Hertz 的 Info 和 Notice 都映射到我们的 Info
		Info(message)
	case hlog.LevelWarn:
		// Hertz 的 Warn 直接映射到我们的 Warn
		Warn(message)
	case hlog.LevelError:
		// Hertz 的 Error 直接映射到我们的 Error
		Error(message)
	case hlog.LevelFatal:
		// Fatal 是最严重的:打完日志后 zap 会调 os.Exit(1) 直接退出进程
		// 用 L() 而不是 Error(),因为 Fatal 要触发进程退出
		L().Fatal(message)
	default:
		// 理论上不会走到这里,保底按 Warn 输出
		Warn(message)
	}
}

// Trace 接收 Hertz 最细粒度的跟踪日志,并按 Debug 级别转发.
// v ...interface{} 是可变参数,Hertz 把要打印的东西一个个传进来.
// fmt.Sprint(v...) 把这些参数拼成一个字符串,再交给 write 去转发.
func (l *HertzAdapter) Trace(v ...interface{}) { l.write(hlog.LevelTrace, fmt.Sprint(v...)) }

// Debug 接收 Hertz 开发诊断日志;当前级别固定 Info,此方法不会输出.
func (l *HertzAdapter) Debug(v ...interface{}) { l.write(hlog.LevelDebug, fmt.Sprint(v...)) }

// Info 接收 Hertz 正常运行日志,并转发到项目 Info 日志.
func (l *HertzAdapter) Info(v ...interface{}) { l.write(hlog.LevelInfo, fmt.Sprint(v...)) }

// Notice 接收 Hertz 提示日志;项目没有 Notice 级别,因此按 Info 转发.
func (l *HertzAdapter) Notice(v ...interface{}) { l.write(hlog.LevelNotice, fmt.Sprint(v...)) }

// Warn 接收 Hertz 可恢复异常日志,并转发到项目 Warn 日志.
func (l *HertzAdapter) Warn(v ...interface{}) { l.write(hlog.LevelWarn, fmt.Sprint(v...)) }

// Error 接收 Hertz 运行错误,并转发到项目 Error 日志.
func (l *HertzAdapter) Error(v ...interface{}) { l.write(hlog.LevelError, fmt.Sprint(v...)) }

// Fatal 记录 Hertz 致命错误,随后由 Zap 终止当前进程.
func (l *HertzAdapter) Fatal(v ...interface{}) { l.write(hlog.LevelFatal, fmt.Sprint(v...)) }

// Tracef 格式化并转发 Hertz 跟踪日志.
// fmt.Sprintf(format, v...) 按 format 模板把参数格式化成字符串,
// 比如 fmt.Sprintf("user=%s", "alice") → "user=alice"
func (l *HertzAdapter) Tracef(format string, v ...interface{}) {
	l.write(hlog.LevelTrace, fmt.Sprintf(format, v...))
}

// Debugf 格式化并转发 Hertz 开发诊断日志.
func (l *HertzAdapter) Debugf(format string, v ...interface{}) {
	l.write(hlog.LevelDebug, fmt.Sprintf(format, v...))
}

// Infof 格式化并转发 Hertz 正常运行日志.
func (l *HertzAdapter) Infof(format string, v ...interface{}) {
	l.write(hlog.LevelInfo, fmt.Sprintf(format, v...))
}

// Noticef 格式化 Hertz 提示日志,并按 Info 级别转发.
func (l *HertzAdapter) Noticef(format string, v ...interface{}) {
	l.write(hlog.LevelNotice, fmt.Sprintf(format, v...))
}

// Warnf 格式化并转发 Hertz 可恢复异常日志.
func (l *HertzAdapter) Warnf(format string, v ...interface{}) {
	l.write(hlog.LevelWarn, fmt.Sprintf(format, v...))
}

// Errorf 格式化并转发 Hertz 运行错误.
func (l *HertzAdapter) Errorf(format string, v ...interface{}) {
	l.write(hlog.LevelError, fmt.Sprintf(format, v...))
}

// Fatalf 格式化 Hertz 致命错误,记录后终止当前进程.
func (l *HertzAdapter) Fatalf(format string, v ...interface{}) {
	l.write(hlog.LevelFatal, fmt.Sprintf(format, v...))
}

// CtxTracef 实现 Hertz 上下文跟踪日志接口;当前公共字段由全局 logger 带上.
// 第一个参数 _ context.Context 用下划线丢掉,因为我们不需要从 ctx 里取东西——
// trace ID 等公共字段在全局 logger 里已经通过 AddFields 带上了.
// Hertz 框架要求这个方法签名必须接收 context,所以参数要留着,只是不用.
func (l *HertzAdapter) CtxTracef(_ context.Context, format string, v ...interface{}) {
	l.Tracef(format, v...)
}

// CtxDebugf 实现 Hertz 上下文 Debug 日志接口.
func (l *HertzAdapter) CtxDebugf(_ context.Context, format string, v ...interface{}) {
	l.Debugf(format, v...)
}

// CtxInfof 实现 Hertz 上下文 Info 日志接口.
func (l *HertzAdapter) CtxInfof(_ context.Context, format string, v ...interface{}) {
	l.Infof(format, v...)
}

// CtxNoticef 实现 Hertz 上下文 Notice 日志接口,并按 Info 转发.
func (l *HertzAdapter) CtxNoticef(_ context.Context, format string, v ...interface{}) {
	l.Noticef(format, v...)
}

// CtxWarnf 实现 Hertz 上下文 Warn 日志接口.
func (l *HertzAdapter) CtxWarnf(_ context.Context, format string, v ...interface{}) {
	l.Warnf(format, v...)
}

// CtxErrorf 实现 Hertz 上下文 Error 日志接口.
func (l *HertzAdapter) CtxErrorf(_ context.Context, format string, v ...interface{}) {
	l.Errorf(format, v...)
}

// CtxFatalf 实现 Hertz 上下文 Fatal 日志接口,记录后终止当前进程.
func (l *HertzAdapter) CtxFatalf(_ context.Context, format string, v ...interface{}) {
	l.Fatalf(format, v...)
}

// SetLevel 接收 Hertz 的动态级别设置,并更新适配器的最低输出级别.
// Hertz 框架在运行时可能调这个方法来动态调整日志级别,我们把它的级别存下来.
func (l *HertzAdapter) SetLevel(level hlog.Level) { l.level = level }

// SetOutput 保留 Hertz 接口兼容性;输出目标由项目 logger 统一管理.
// Hertz 默认有一个日志输出目标(比如文件或 stdout),但我们的日志走全局 logger 统一管理,
// 所以这个方法什么都不做(空函数体),纯粹是为了满足 hlog.FullLogger 接口的要求.
// 参数 _ io.Writer 用下划线丢掉,因为我们不使用它.
func (l *HertzAdapter) SetOutput(_ io.Writer) {}

// 这行代码是编译期的接口检查:
// var _ hlog.FullLogger = (*HertzAdapter)(nil)
// 意思是声明一个空变量,类型是 hlog.FullLogger,赋值为一个 nil 指针转换成的 *HertzAdapter.
// 如果 *HertzAdapter 没有完整实现 hlog.FullLogger 接口的所有方法,编译就会报错.
// 这是一种"编译期接口合规检查"的惯用写法,运行时不产生任何开销.
var _ hlog.FullLogger = (*HertzAdapter)(nil)
