// logger.go 放项目统一的结构化日志入口。
//
// 做的事情：
//  1. Init：初始化全局 zap.Logger，开发模式用控制台输出，生产模式用 JSON 格式。
//  2. Info/Warn/Error/Debug：封装 zap.Logger 的同名方法，业务代码通过本包记录日志。
//  3. Sync：刷新日志缓冲区，入口程序退出前应调用。
//  4. AddFields：为当前进程后续日志附加公共字段（如 startup_id）。
//
// Debug 在 logger 未初始化时安全忽略（不 panic），其他级别会 panic。
package logger

import "go.uber.org/zap"

// global 是全局 logger 实例，Init 之后可用。
// 全局变量，所有包通过 Info/Warn/Error/Debug 这些函数间接访问它。
var global *zap.Logger

// environment 保存从 TOML 读取的当前运行环境，用于控制日志格式和最低级别。
// 默认是开发模式 "development"。
var environment = EnvironmentDevelopment

// Init 初始化全局 logger。
// environments 是可选参数，传 "production" 就用生产模式，不传或传空就用开发模式。
// Phase 1 先用开发模式（控制台输出，易读），
// 后期改成可配置（开发/生产模式切换）。
func Init(environments ...string) error {
	// environments 是可变参数（...string），外面调用可以传也可以不传
	// 如果传了且第一个参数不是空串，就用它作为运行环境
	if len(environments) > 0 && environments[0] != "" {
		environment = environments[0]
	} else {
		// 没传就默认用开发模式
		environment = EnvironmentDevelopment
	}

	// 声明两个变量：l 是 zap.Logger 实例，err 是初始化错误
	var l *zap.Logger
	var err error

	// zap 是一个高性能结构化日志库（go.uber.org/zap）。
	// 它有两种内置模式：
	//   zap.NewProduction() → 生产模式：输出 JSON 格式，方便机器解析，级别从 Info 开始（Debug 不输出）
	//   zap.NewDevelopment() → 开发模式：输出控制台彩色文本，人眼易读，级别从 Debug 开始
	//
	// zap.AddCallerSkip(1) 的作用：
	//   zap 默认会记录"谁调用了日志方法"，也就是调用者的文件名和行号。
	//   AddCallerSkip(1) 让它往上多跳一层。
	//   因为我们有 logger.Info/Warn/Error 这些封装函数，
	//   如果不加 skip，zap 记的调用者就是 logger.go 里的 Info 函数（第 79 行），而不是真正打日志的业务代码。
	//   加了 skip(1) 之后，zap 跳过封装函数，记录真正调用 logger.Info 的那行业务代码的文件名和行号。
	//
	// zap.AddStacktrace(zap.ErrorLevel) 的作用：
	//   只在 Error 及以上级别额外打印调用栈（函数调用链）。
	//   方便排查"到底是从哪条代码路径触发的这个 Error"。
	//   只对 Error 级别加，不会给 Info/Debug 也加（那太吵了）。
	if IsProduction() {
		// 生产环境：JSON 格式输出，便于 ELK 等日志收集系统解析
		l, err = zap.NewProduction(
			zap.AddCallerSkip(1),
			zap.AddStacktrace(zap.ErrorLevel),
		)
	} else {
		// 开发环境：控制台彩色文本，人眼易读
		l, err = zap.NewDevelopment(
			zap.AddCallerSkip(1),
			zap.AddStacktrace(zap.ErrorLevel),
		)
	}
	if err != nil {
		// zap 初始化失败（极少见，通常不会出错），把错误返回给调用的人
		return err
	}
	// 初始化成功，赋值给全局变量 global
	global = l
	return nil
}

// IsProduction 表示当前是否为生产环境。
// 通过比对 environment 变量和 EnvironmentProduction 常量来判断。
func IsProduction() bool {
	return environment == EnvironmentProduction
}

// L 返回全局 logger，没 Init 过会 panic。
// 这样设计是因为：如果 logger 没初始化就打日志，说明启动流程有 bug，
// 直接 panic 暴露问题比静默丢日志好。
func L() *zap.Logger {
	if global == nil {
		// 全局 logger 还是 nil，说明 Init 没被调用过，直接 panic 报错
		panic("logger not initialized, call logger.Init() first")
	}
	// 返回全局 logger 实例
	return global
}

// Sync 刷新日志缓冲区。入口程序退出前应调用。
// 某些控制台环境不支持 sync，错误由调用的人按需处理。
// zap 底层可能用了带缓冲的 writer，不 Sync 的话最后几条日志可能还卡在缓冲区里没写出去。
func Sync() error {
	if global == nil {
		// logger 还没初始化，没什么可 Sync 的，直接返回 nil
		return nil
	}
	// global.Sync() 是 zap.Logger 的方法，把缓冲区里的日志刷到输出目标
	return global.Sync()
}

// AddFields 为当前进程后续日志附加公共字段，例如 startup_id。
// 用法：Init 之后调一次 AddFields(zap.String("startup_id", xxx))，
// 之后所有通过 logger.Info/Warn 等打的日志都会带上 startup_id 字段。
func AddFields(fields ...zap.Field) {
	if global != nil {
		// global.With(fields...) 返回一个新的 logger，带上这些公共字段，
		// 把它重新赋值给 global，后续所有日志就都带上了
		global = global.With(fields...)
	}
}

// Info 记录 Info 级别日志。
// msg 是日志消息，fields 是结构化字段（键值对），比如 zap.String("user", "alice")
// 调用 L() 拿到全局 logger，再调它的 Info 方法
func Info(msg string, fields ...zap.Field) { L().Info(msg, fields...) }

// Warn 记录 Warn 级别日志。
func Warn(msg string, fields ...zap.Field) { L().Warn(msg, fields...) }

// Error 记录 Error 级别日志。
func Error(msg string, fields ...zap.Field) { L().Error(msg, fields...) }

// Debug 记录仅开发环境需要的诊断信息。日志尚未初始化时安全忽略。
// 和 Info/Warn/Error 不同：Debug 不调 L()（因为 L() 在 global 为 nil 时会 panic），
// 而是先检查 global 是不是 nil，是 nil 就直接 return，不会 panic。
// 这样设计是因为在启动流程早期（Init 还没调），可能已经有代码打 Debug 日志了。
func Debug(msg string, fields ...zap.Field) {
	if global == nil {
		// logger 还没初始化，Debug 日志直接丢掉，不 panic
		return
	}
	// logger 已经初始化了，正常打 Debug 日志
	global.Debug(msg, fields...)
}
