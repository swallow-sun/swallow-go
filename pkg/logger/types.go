// types.go 放 logger 包的类型定义和常量.
//
// 做的事情:
//  1. 定义运行环境常量:development 和 production.
//  2. 定义 HertzAdapter 结构体:把 Hertz 框架日志转发到项目全局 logger,带级别过滤.
package logger

import "github.com/cloudwego/hertz/pkg/common/hlog"

const (
	// EnvironmentDevelopment 开发环境,对应 zap 的 NewDevelopment 模式.
	EnvironmentDevelopment = "development"
	// EnvironmentProduction 生产环境,对应 zap 的 NewProduction 模式.
	EnvironmentProduction = "production"
	// MaxDebugSQLLength 限制开发环境单条 SQL 日志长度，防止异常语句撑爆日志文件。
	MaxDebugSQLLength = 4096
	// LogDirectory 是本地日志文件的固定目录，相对于程序工作目录。
	LogDirectory = "logs"
	// LogFileDateLayout 用日期拆分日志文件，避免所有历史日志写入同一个文件。
	LogFileDateLayout = "2006-01-02"
	// LogFilePrefix 是日志文件名前缀，最终文件名如 swallow-2026-08-19.log。
	LogFilePrefix = "swallow-"
)

// HertzAdapter 把 Hertz 框架日志转发到项目唯一的全局 logger.
// 适配器模式:Hertz 框架自己有日志接口(hlog.FullLogger),
// 我们不直接用它的,而是实现一套适配器把 Hertz 的日志全转到我们的 zap logger.
// level 字段控制当前允许输出的最低日志级别.
type HertzAdapter struct {
	level hlog.Level // 当前允许输出的最低 Hertz 日志级别
}

// gormLogger 把 GORM 内部日志安全转发到项目 logger。
type gormLogger struct{}

// Options 是日志初始化参数，由 TOML 的 app 和 log 配置组装。
type Options struct {
	Environment string // development 或 production，控制敏感 SQL 是否允许输出
	Level       string // debug、info、warn 或 error
	Directory   string // 本地日志文件目录
}
