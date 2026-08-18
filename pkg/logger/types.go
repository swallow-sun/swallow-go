// types.go 放 logger 包的类型定义和常量。
//
// 做的事情：
//  1. 定义运行环境常量：development 和 production。
//  2. 定义 HertzAdapter 结构体：把 Hertz 框架日志转发到项目全局 logger，带级别过滤。
package logger

import "github.com/cloudwego/hertz/pkg/common/hlog"

const (
	EnvironmentDevelopment = "development"
	EnvironmentProduction  = "production"
)

// HertzAdapter 把 Hertz 框架日志转发到项目唯一的全局 logger。
type HertzAdapter struct {
	level hlog.Level // 当前允许输出的最低 Hertz 日志级别
}
