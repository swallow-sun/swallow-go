// types.go 统一定义 metrics 模块的常量和自定义类型。
package metrics

const (
	// 组件标签必须使用有限枚举值，避免产生高基数指标。
	ComponentGo      = "go"
	ComponentDevice  = "device"
	ComponentDesktop = "desktop"
	ComponentMCU     = "mcu"

	// 状态标签描述操作最终结果。
	StatusOK        = "ok"
	StatusFailed    = "failed"
	StatusDenied    = "denied"
	StatusTimeout   = "timeout"
	StatusCancelled = "cancelled"

	// Token 类型标签用于拆分模型用量。
	TokenTypeInput         = "input"
	TokenTypeOutput        = "output"
	TokenTypeCachedInput   = "cached_input"
	TokenTypeCacheCreation = "cache_creation"
	TokenTypeReasoning     = "reasoning"
)
