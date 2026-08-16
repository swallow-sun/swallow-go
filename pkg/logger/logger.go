// Package logger 提供项目统一的结构化日志入口。
// 业务代码应通过本包记录日志，避免在各模块中分别创建 logger 实例。
package logger

import "go.uber.org/zap"

// global 是全局 logger 实例，Init 之后可用。
var global *zap.Logger

// Init 初始化全局 logger。
// Phase 1 先用开发模式（控制台输出，易读），
// 后期改成可配置（开发/生产模式切换）。
func Init() error {
	l, err := zap.NewDevelopment()
	if err != nil {
		return err
	}
	global = l
	return nil
}

// L 返回全局 logger，没 Init 过会 panic。
func L() *zap.Logger {
	if global == nil {
		panic("logger not initialized, call logger.Init() first")
	}
	return global
}

// Sync 刷新日志缓冲区。入口程序退出前应调用。
// 某些控制台环境不支持 sync，错误由调用方按需处理。
func Sync() error {
	if global == nil {
		return nil
	}
	return global.Sync()
}

// Info 记录 Info 级别日志。
func Info(msg string, fields ...zap.Field) { L().Info(msg, fields...) }

// Warn 记录 Warn 级别日志。
func Warn(msg string, fields ...zap.Field) { L().Warn(msg, fields...) }

// Error 记录 Error 级别日志。
func Error(msg string, fields ...zap.Field) { L().Error(msg, fields...) }

// Debug 记录 Debug 级别日志。
func Debug(msg string, fields ...zap.Field) { L().Debug(msg, fields...) }
