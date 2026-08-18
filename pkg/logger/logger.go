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
var global *zap.Logger

// environment 保存从 TOML 读取的当前运行环境，用于控制日志格式和最低级别。
var environment = EnvironmentDevelopment

// Init 初始化全局 logger。
// Phase 1 先用开发模式（控制台输出，易读），
// 后期改成可配置（开发/生产模式切换）。
func Init(environments ...string) error {
	if len(environments) > 0 && environments[0] != "" {
		environment = environments[0]
	} else {
		environment = EnvironmentDevelopment
	}
	var l *zap.Logger
	var err error
	if IsProduction() {
		l, err = zap.NewProduction(
			zap.AddCallerSkip(1),
			zap.AddStacktrace(zap.ErrorLevel),
		)
	} else {
		l, err = zap.NewDevelopment(
			zap.AddCallerSkip(1),
			zap.AddStacktrace(zap.ErrorLevel),
		)
	}
	if err != nil {
		return err
	}
	global = l
	return nil
}

// IsProduction 表示当前是否为生产环境。
func IsProduction() bool {
	return environment == EnvironmentProduction
}

// L 返回全局 logger，没 Init 过会 panic。
func L() *zap.Logger {
	if global == nil {
		panic("logger not initialized, call logger.Init() first")
	}
	return global
}

// Sync 刷新日志缓冲区。入口程序退出前应调用。
// 某些控制台环境不支持 sync，错误由调用的人按需处理。
func Sync() error {
	if global == nil {
		return nil
	}
	return global.Sync()
}

// AddFields 为当前进程后续日志附加公共字段，例如 startup_id。
func AddFields(fields ...zap.Field) {
	if global != nil {
		global = global.With(fields...)
	}
}

// Info 记录 Info 级别日志。
func Info(msg string, fields ...zap.Field) { L().Info(msg, fields...) }

// Warn 记录 Warn 级别日志。
func Warn(msg string, fields ...zap.Field) { L().Warn(msg, fields...) }

// Error 记录 Error 级别日志。
func Error(msg string, fields ...zap.Field) { L().Error(msg, fields...) }

// Debug 记录仅开发环境需要的诊断信息。日志尚未初始化时安全忽略。
func Debug(msg string, fields ...zap.Field) {
	if global == nil {
		return
	}
	global.Debug(msg, fields...)
}
