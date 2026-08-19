// logger.go 放项目统一的结构化日志入口.
//
// 做的事情:
//  1. Init:初始化全局 zap.Logger,同时输出到控制台和 logs 日期文件.
//  2. Info/Warn/Error/Debug:封装 zap.Logger 的同名方法,业务代码通过本包记录日志.
//  3. Sync:刷新日志缓冲区,入口程序退出前应调用.
//  4. AddFields:为当前进程后续日志附加公共字段(如 startup_id).
//
// Debug 在 logger 未初始化时安全忽略(不 panic),其他级别会 panic.
package logger

import (
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// global 是全局 logger 实例,Init 之后可用.
// 全局变量,所有包通过 Info/Warn/Error/Debug 这些函数间接访问它.
var global *zap.Logger

// logWriter 是当前进程持有的轮转日志写入器，Sync 时关闭。
var logWriter *lumberjack.Logger

// environment 保存从 TOML 读取的当前运行环境,用于控制日志格式和最低级别.
// 默认是开发模式 "development".
var environment = EnvironmentDevelopment

// Init 初始化全局 logger；不传参数时使用开发环境、Debug 和 logs 目录。
// 开发和生产环境都以 JSON 同时输出到控制台和配置目录下的日期日志文件。
func Init(options ...Options) error {
	opt := Options{
		Environment: EnvironmentDevelopment,
		Level:       "debug",
		Directory:   LogDirectory,
		MaxSizeMB:   DefaultMaxSizeMB,
		MaxBackups:  DefaultMaxBackups,
		MaxAgeDays:  DefaultMaxAgeDays,
		Compress:    true,
	}
	if len(options) > 0 {
		if options[0].Environment != "" {
			opt.Environment = options[0].Environment
		}
		if options[0].Level != "" {
			opt.Level = options[0].Level
		}
		if options[0].Directory != "" {
			opt.Directory = options[0].Directory
		}
		if options[0].MaxSizeMB > 0 {
			opt.MaxSizeMB = options[0].MaxSizeMB
		}
		if options[0].MaxBackups > 0 {
			opt.MaxBackups = options[0].MaxBackups
		}
		if options[0].MaxAgeDays > 0 {
			opt.MaxAgeDays = options[0].MaxAgeDays
		}
		opt.Compress = options[0].Compress
	}
	environment = opt.Environment
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(opt.Level)); err != nil {
		return fmt.Errorf("parse log level %q: %w", opt.Level, err)
	}

	// 重复初始化时先刷新并关闭旧文件，避免测试或多入口初始化造成文件句柄泄漏。
	if logWriter != nil {
		_ = logWriter.Close()
		logWriter = nil
	}

	// logs 目录不存在时自动创建；创建失败直接终止启动，避免误以为日志已经持久化。
	if err := os.MkdirAll(opt.Directory, 0o755); err != nil {
		return fmt.Errorf("create log directory %s: %w", opt.Directory, err)
	}
	logPath := filepath.Join(opt.Directory, LogFileName)
	// Lumberjack 延迟到首次写入才打开文件；这里提前探测，确保路径或权限错误在启动阶段暴露。
	probe, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", logPath, err)
	}
	if err := probe.Close(); err != nil {
		return fmt.Errorf("close log file probe %s: %w", logPath, err)
	}
	logWriter = &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    opt.MaxSizeMB,
		MaxBackups: opt.MaxBackups,
		MaxAge:     opt.MaxAgeDays,
		Compress:   opt.Compress,
		LocalTime:  true,
	}

	// 编码器配置:用 JSON 格式,方便机器解析,开发和生产统一
	encoderConfig := zap.NewProductionEncoderConfig()
	// EncodeTime 用 ISO8601 格式,带时区,比默认的 epoch 毫秒好读
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoder := zapcore.NewJSONEncoder(encoderConfig)

	// 控制台和本地文件使用相同 JSON、级别和结构化字段，方便直接检索 startup_id/trace_id。
	consoleCore := zapcore.NewCore(encoder, zapcore.Lock(os.Stdout), level)
	fileCore := zapcore.NewCore(encoder.Clone(), zapcore.AddSync(logWriter), level)
	core := zapcore.NewTee(consoleCore, fileCore)

	// zap.AddCallerSkip(1) 让 caller 往上跳一层,跳过 logger.Info/Warn/Error 封装函数,
	// 记录真正调用日志的业务代码的文件名和行号.
	// zap.AddStacktrace(zap.ErrorLevel) 只在 Error 及以上级别打印调用栈,方便排查问题.
	l := zap.New(core,
		zap.AddCallerSkip(1),
		zap.AddStacktrace(zap.ErrorLevel),
	)
	global = l
	return nil
}

// IsProduction 表示当前是否为生产环境.
// 通过比对 environment 变量和 EnvironmentProduction 常量来判断.
func IsProduction() bool {
	return environment == EnvironmentProduction
}

// L 返回全局 logger,没 Init 过会 panic.
// 这样设计是因为:如果 logger 没初始化就打日志,说明启动流程有 bug,
// 直接 panic 暴露问题比静默丢日志好.
func L() *zap.Logger {
	if global == nil {
		// 全局 logger 还是 nil,说明 Init 没被调用过,直接 panic 报错
		panic("logger not initialized, call logger.Init() first")
	}
	// 返回全局 logger 实例
	return global
}

// Sync 刷新日志缓冲区.入口程序退出前应调用.
// 某些控制台环境不支持 sync,错误由调用的人按需处理.
// zap 底层可能用了带缓冲的 writer,不 Sync 的话最后几条日志可能还卡在缓冲区里没写出去.
func Sync() error {
	if global == nil {
		// logger 还没初始化,没什么可 Sync 的,直接返回 nil
		return nil
	}
	// 先刷新 zap 的控制台和文件 Core，再关闭文件句柄。
	syncErr := global.Sync()
	if logWriter == nil {
		return syncErr
	}
	closeErr := logWriter.Close()
	logWriter = nil
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return fmt.Errorf("close log file: %w", closeErr)
	}
	return nil
}

// AddFields 为当前进程后续日志附加公共字段,例如 startup_id.
// 用法:Init 之后调一次 AddFields(zap.String("startup_id", xxx)),
// 之后所有通过 logger.Info/Warn 等打的日志都会带上 startup_id 字段.
func AddFields(fields ...zap.Field) {
	if global != nil {
		// global.With(fields...) 返回一个新的 logger,带上这些公共字段,
		// 把它重新赋值给 global,后续所有日志就都带上了
		global = global.With(fields...)
	}
}

// Info 记录 Info 级别日志.
// msg 是日志消息,fields 是结构化字段(键值对),比如 zap.String("user", "alice")
// 调用 L() 拿到全局 logger,再调它的 Info 方法
func Info(msg string, fields ...zap.Field) { L().Info(msg, fields...) }

// Warn 记录 Warn 级别日志.
func Warn(msg string, fields ...zap.Field) { L().Warn(msg, fields...) }

// Error 记录 Error 级别日志.
func Error(msg string, fields ...zap.Field) { L().Error(msg, fields...) }

// Debug 记录仅开发环境需要的诊断信息.日志尚未初始化时安全忽略.
// 和 Info/Warn/Error 不同:Debug 不调 L()(因为 L() 在 global 为 nil 时会 panic),
// 而是先检查 global 是不是 nil,是 nil 就直接 return,不会 panic.
// 这样设计是因为在启动流程早期(Init 还没调),可能已经有代码打 Debug 日志了.
func Debug(msg string, fields ...zap.Field) {
	if global == nil {
		// logger 还没初始化,Debug 日志直接丢掉,不 panic
		return
	}
	// logger 已经初始化了,正常打 Debug 日志
	global.Debug(msg, fields...)
}
