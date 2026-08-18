// metrics.go 放 Prometheus 指标定义和记录辅助函数。
//
// 做的事情：
//  1. Init：注册所有指标到默认 Register（Go 进程全局唯一）。
//  2. RecordRequest：记录一次请求的计数器 + 耗时直方图。
//  3. RecordModelCall：记录一次模型调用的 Token 计数器 + 请求计数器。
//  4. RecordMemoryQuery：记录一次记忆查询的计数器 + 耗时直方图。
//
// 指标命名遵循项目方案 15.5 节：
//
//	swallow_requests_total{component,operation,status}
//	swallow_request_duration_ms{component,operation}
//	swallow_model_tokens_total{provider,model,token_type}
//	swallow_model_requests_total{provider,model,status}
//	swallow_memory_queries_total{status}
//	swallow_memory_query_duration_ms{status}
//
// 标签只用可枚举值，不用 user_id/session_id/trace_id 等高基数字段。
// 延迟用直方图（Histogram）而不是只存平均值，Prometheus 自动算 P50/P95/P99。
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// 标签的合法取值，防止手拼写错产生高基数。
const (
	// ComponentGo 是 Go 服务端组件标签
	ComponentGo = "go"
	// ComponentDevice 是移动设备组件标签（预留，当前阶段不产生）
	ComponentDevice = "device"
	// ComponentDesktop 是 Rust 电脑代理组件标签（预留）
	ComponentDesktop = "desktop"
	// ComponentMCU 是 MCU 组件标签（预留）
	ComponentMCU = "mcu"

	// StatusOK 操作成功
	StatusOK = "ok"
	// StatusFailed 操作失败
	StatusFailed = "failed"
	// StatusDenied 权限拒绝（预留，沙箱阶段产生）
	StatusDenied = "denied"
	// StatusTimeout 超时
	StatusTimeout = "timeout"
	// StatusCancelled 客户端取消
	StatusCancelled = "cancelled"

	// TokenTypeInput 输入 Token
	TokenTypeInput = "input"
	// TokenTypeOutput 输出 Token
	TokenTypeOutput = "output"
	// TokenTypeCachedInput 缓存命中输入 Token
	TokenTypeCachedInput = "cached_input"
	// TokenTypeCacheCreation 缓存创建 Token（Anthropic 概念，预留）
	TokenTypeCacheCreation = "cache_creation"
	// TokenTypeReasoning 推理 Token（思维链）
	TokenTypeReasoning = "reasoning"
)

// 下面是进程级唯一的指标变量。
// 用 promauto 包创建，自动注册到 prometheus.DefaultRegisterer，
// 不需要手动调 Register——Init 里统一做一次注册确认。
var (
	// requestsTotal 记录请求总数（Counter，只增不减）。
	// 标签：component（go/device/desktop/mcu）、operation（chat/session/history）、status（ok/failed/timeout/cancelled）。
	// 用途：算错误率、QPS、成功率。
	requestsTotal *prometheus.CounterVec

	// requestDurationMs 记录请求耗时（Histogram，支持 P50/P95/P99）。
	// 标签：component、operation。
	// 单位：毫秒。桶大小覆盖 5ms~10s，适合 API 请求延迟分布。
	requestDurationMs *prometheus.HistogramVec

	// modelTokensTotal 记录模型 Token 消耗总量（Counter）。
	// 标签：provider（deepseek/openai）、model（deepseek-chat/gpt-4o）、token_type（input/output/cached_input/cache_creation/reasoning）。
	// 用途：按供应商/模型/类型聚合 Token 用量趋势。
	modelTokensTotal *prometheus.CounterVec

	// modelRequestsTotal 记录模型调用次数（Counter）。
	// 标签：provider、model、status（ok/failed/timeout）。
	// 用途：按供应商/模型统计调用次数和失败率。
	modelRequestsTotal *prometheus.CounterVec

	// memoryQueriesTotal 记录记忆查询次数（Counter）。
	// 标签：status（ok/failed）。
	memoryQueriesTotal *prometheus.CounterVec

	// memoryQueryDurationMs 记录记忆查询耗时（Histogram）。
	// 标签：status。
	// 记忆查询通常很快（<10ms），桶大小覆盖 1ms~5s。
	memoryQueryDurationMs *prometheus.HistogramVec
)

// Init 注册所有指标到默认 Registerer。
// 在程序启动时调一次。重复调不会 panic（promauto 创建时已自动注册），
// 但需要调一次确保指标变量已初始化。
//
// 用 promauto 创建指标时会自动注册到 prometheus.DefaultRegisterer，
// 所以这个函数主要保证"指标变量已创建"，
// 防止有人在 Init 之前就调 RecordRequest 导致空指针。
func Init() {
	// requestsTotal：请求总数计数器
	// promauto.NewCounterVec 创建一个 CounterVec 并自动注册
	// Namespace="swallow" 是指标名前缀，最终指标名是 swallow_requests_total
	// Help 是指标的说明文字，Prometheus UI 上会显示
	requestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "swallow",
			Name:      "requests_total",
			Help:      "Total number of requests by component, operation and status.",
		},
		[]string{"component", "operation", "status"},
	)

	// requestDurationMs：请求耗时直方图
	// Buckets 定义直方图的桶上界，Prometheus 按这些桶统计落在各区间的请求次数
	// 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000 毫秒
	// 覆盖 5ms（极快）到 10s（很慢），适合 API 延迟分布
	// Prometheus 自动计算 P50/P95/P99，不需要自己算
	requestDurationMs = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "swallow",
			Name:      "request_duration_ms",
			Help:      "Request duration in milliseconds, by component and operation.",
			Buckets:   []float64{5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000},
		},
		[]string{"component", "operation"},
	)

	// modelTokensTotal：模型 Token 消耗总量
	// 每次模型调用结束后，按 token_type 分别记录 input/output/cached_input/reasoning
	modelTokensTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "swallow",
			Name:      "model_tokens_total",
			Help:      "Total model tokens consumed, by provider, model and token type.",
		},
		[]string{"provider", "model", "token_type"},
	)

	// modelRequestsTotal：模型调用次数
	// 每次模型调用结束后记录一次，status 区分成功/失败/超时
	modelRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "swallow",
			Name:      "model_requests_total",
			Help:      "Total model API calls, by provider, model and status.",
		},
		[]string{"provider", "model", "status"},
	)

	// memoryQueriesTotal：记忆查询次数
	memoryQueriesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "swallow",
			Name:      "memory_queries_total",
			Help:      "Total memory query calls, by status.",
		},
		[]string{"status"},
	)

	// memoryQueryDurationMs：记忆查询耗时直方图
	// 记忆查询通常很快，桶从 1ms 开始，更密集覆盖小值
	memoryQueryDurationMs = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "swallow",
			Name:      "memory_query_duration_ms",
			Help:      "Memory query duration in milliseconds, by status.",
			Buckets:   []float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000},
		},
		[]string{"status"},
	)
}

// RecordRequest 记录一次请求的指标：计数器 +1，耗时直方图记录。
// component 是组件名（go/device/desktop/mcu），用 ComponentGo 等常量。
// operation 是操作名（chat/session/history），跟路由对应。
// status 是结果状态（ok/failed/timeout/cancelled），用 StatusOK 等常量。
// durationMs 是耗时毫秒数。
func RecordRequest(component, operation, status string, durationMs float64) {
	// CounterVec.WithLabelValues 拿到带标签的 Counter 实例，再调 Inc() 计数 +1
	// 如果指标还没初始化（Init 没调），requestsTotal 是 nil，WithLabelValues 会 panic
	// 所以必须确保 Init 在程序启动时先调
	if requestsTotal != nil {
		requestsTotal.WithLabelValues(component, operation, status).Inc()
	}
	// HistogramVec.WithLabelValues 拿到带标签的 Histogram 实例，再调 Observe 记录一个观测值
	// Prometheus 自动把这个值归入对应的桶，后续可以算 P50/P95/P99
	if requestDurationMs != nil {
		requestDurationMs.WithLabelValues(component, operation).Observe(durationMs)
	}
}

// RecordModelCall 记录一次模型调用的指标：Token 消耗 + 请求计数。
// provider 是供应商名（deepseek/openai）。
// model 是模型名（deepseek-chat/gpt-4o）。
// status 是调用结果（ok/failed/timeout）。
// inputTokens/outputTokens/cachedInputTokens/reasoningTokens 是各类 Token 数。
// cacheCreationTokens 当前阶段不记录（DeepSeek/OpenAI 不返回），留空。
func RecordModelCall(
	provider, model, status string,
	inputTokens, outputTokens, cachedInputTokens, reasoningTokens float64,
) {
	// 按每种 token_type 分别记录，方便看板按类型聚合
	if modelTokensTotal != nil {
		// 输入 Token
		modelTokensTotal.WithLabelValues(provider, model, TokenTypeInput).Add(inputTokens)
		// 输出 Token
		modelTokensTotal.WithLabelValues(provider, model, TokenTypeOutput).Add(outputTokens)
		// 缓存命中输入 Token
		modelTokensTotal.WithLabelValues(provider, model, TokenTypeCachedInput).Add(cachedInputTokens)
		// 推理 Token
		modelTokensTotal.WithLabelValues(provider, model, TokenTypeReasoning).Add(reasoningTokens)
	}

	// 模型调用次数 +1
	if modelRequestsTotal != nil {
		modelRequestsTotal.WithLabelValues(provider, model, status).Inc()
	}
}

// RecordMemoryQuery 记录一次记忆查询的指标：查询次数 + 耗时直方图。
// status 是查询结果（ok/failed）。
// durationMs 是查询耗时毫秒数。
func RecordMemoryQuery(status string, durationMs float64) {
	// 记忆查询次数 +1
	if memoryQueriesTotal != nil {
		memoryQueriesTotal.WithLabelValues(status).Inc()
	}
	// 记忆查询耗时直方图记录
	if memoryQueryDurationMs != nil {
		memoryQueryDurationMs.WithLabelValues(status).Observe(durationMs)
	}
}
