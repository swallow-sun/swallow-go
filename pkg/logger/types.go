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
	// LogFileName 是当前正在写入的主日志文件名；旧文件由轮转器重命名。
	LogFileName = "swallow.log"
	// DefaultMaxSizeMB 是未传配置时的单文件默认大小。
	DefaultMaxSizeMB = 100
	// DefaultMaxBackups 是未传配置时默认保留的备份数。
	DefaultMaxBackups = 30
	// DefaultMaxAgeDays 是未传配置时旧日志默认保留天数。
	DefaultMaxAgeDays = 30
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
	MaxSizeMB   int    // 单个日志文件轮转大小，单位 MB
	MaxBackups  int    // 最多保留的旧日志数量
	MaxAgeDays  int    // 旧日志保留天数
	Compress    bool   // 是否 gzip 压缩轮转后的旧日志
}
