// types.go 放 trace 包的类型定义。
//
// 做的事情：
//  定义 traceKey 类型：trace ID 在 context.Context 中使用的私有键，防止外部包直接访问。
package trace

// traceKey 是 trace ID 在 context.Context 中使用的私有键类型。
type traceKey struct{}
