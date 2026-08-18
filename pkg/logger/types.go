// types.go 放 logger 包的类型定义和常量。
//
// 做的事情：
//  1. 定义运行环境常量：development 和 production。
//  2. 定义 HertzAdapter 结构体：把 Hertz 框架日志转发到项目全局 logger，带级别过滤。
package logger

import "github.com/cloudwego/hertz/pkg/common/hlog"

const (
	// EnvironmentDevelopment 开发环境，对应 zap 的 NewDevelopment 模式。
	EnvironmentDevelopment = "development"
	// EnvironmentProduction 生产环境，对应 zap 的 NewProduction 模式。
	EnvironmentProduction = "production"
)

// HertzAdapter 把 Hertz 框架日志转发到项目唯一的全局 logger。
// 适配器模式：Hertz 框架自己有日志接口（hlog.FullLogger），
// 我们不直接用它的，而是实现一套适配器把 Hertz 的日志全转到我们的 zap logger。
// level 字段控制当前允许输出的最低日志级别。
type HertzAdapter struct {
	level hlog.Level // 当前允许输出的最低 Hertz 日志级别
}
