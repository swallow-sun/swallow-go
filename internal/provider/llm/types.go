// types.go 放 LLM 服务的核心数据结构和接口定义。
//
// 做的事情：
//  1. 定义 Provider 接口：跟具体模型供应商无关的调用接口（Complete 非流式 + Stream 流式）。
//  2. 定义 StreamReader 接口：流式响应读取接口（Next 逐块读 + Usage 取用量 + Close 关闭）。
//  3. 定义消息和请求/响应结构体：ChatMessage、ChatRequest、ChatResponse、Usage、StreamResponse 等。
//  4. 定义角色常量：system、user、assistant、tool、function。
//  5. 定义 OpenAICompat 和 sseStreamReader 的结构体骨架（实现分别在 openai_compat.go）。
//
// 所有实现遵循 OpenAI Chat Completions 兼容协议，
// 换模型只需改 base_url + api_key + model 三个配置。
//
// 什么是 OpenAI Chat Completions 协议：
//   OpenAI 定义的对话 API 格式，请求体里有 model 和 messages（消息列表），
//   每条消息有 role（角色）和 content（内容）。响应体里有 choices（候选回复）。
//   因为这套协议用的人最多，其他厂商基本都兼容它，所以叫"兼容协议"。
package llm

import (
	// bufio 给 SSE 扫描器用。sseStreamReader 里面用 *bufio.Scanner 逐行读 SSE 流
	"bufio"
	// context 传给 Provider 接口方法，控制超时取消
	"context"
	// io 给 sseStreamReader 用。body 字段类型是 io.Closer，Close 时关掉 HTTP 响应体
	"io"
	// net/http 给 OpenAICompat 用。client 字段类型是 *http.Client，发 HTTP 请求
	"net/http"
)

// Config 是模型 Provider 的初始化配置。
// 比如用 DeepSeek 就是：BaseURL="https://api.deepseek.com/v1"，APIKey="sk-xxx"，Model="deepseek-chat"
type Config struct {
	BaseURL string // API 基础地址，如 https://api.deepseek.com/v1，后面拼 /chat/completions
	APIKey  string // API 密钥，如 sk-xxx，放在 HTTP 头的 Authorization: Bearer sk-xxx 里
	Model   string // 默认模型名，比如 deepseek-chat。实际调用时也可以在 ChatRequest 里覆盖
}

// Provider 是跟具体模型供应商无关的调用接口。
// 定义接口的好处：将来换模型厂商时，调用方不用改代码，只要写一个新实现替换 OpenAICompat。
//
// 比如现在用 DeepSeek（OpenAICompat），将来换成本地部署的 Ollama，
// 只要写一个 OllamaProvider 实现这两个方法，调用方代码不变。
type Provider interface {
	// Complete 是非流式调用：调一次 API，等模型把完整回复生成好，一次性返回
	Complete(ctx context.Context, req ChatRequest) (ChatResponse, error)
	// Stream 是流式调用：调一次 API，返回 StreamReader，调用方逐块读增量文本
	Stream(ctx context.Context, req ChatRequest) (StreamReader, error)
}

// StreamReader 是流式模型响应读取接口。
// Stream 方法返回这个接口，调用的人循环调 Next() 读增量文本，读完调 Close()。
//
// 用法示例：
//
//	reader, _ := provider.Stream(ctx, req)
//	defer reader.Close()                          // 别忘了关
//	for {
//	    chunk, done, err := reader.Next()          // 读一块
//	    if err != nil { return err }              // 出错了
//	    if done { break }                          // 读完了
//	    fmt.Print(chunk)                           // 处理这块文本
//	}
//	usage := reader.Usage()                        // 拿 token 用量
type StreamReader interface {
	// Next 读下一块。chunk 是增量文本，done=true 表示流结束，err 非 nil 表示出错
	Next() (chunk string, done bool, err error)
	// Usage 返回 token 用量统计。在流读完后调，因为用量在流末尾才返回
	Usage() Usage
	// Close 关闭底层 HTTP 响应体。读完后必须调，不调会连接泄漏
	Close() error
}

// OpenAICompat 是 OpenAI Chat Completions 兼容协议实现。
// 实现 Provider 和 StreamReader 接口的具体逻辑在 openai_compat.go。
type OpenAICompat struct {
	config Config        // 配置（base_url、api_key、model），构造时传入，不可变
	client *http.Client  // HTTP 客户端，复用它发请求（底层复用 TCP 连接）
}

// sseStreamReader 是 StreamReader 的 SSE 实现。
// SSE = Server-Sent Events，服务器推流用的 HTTP 协议。
// 模型一边生成一边推，每个增量文本块用 "data: {JSON}\n\n" 的格式发过来。
type sseStreamReader struct {
	scanner  *bufio.Scanner // 逐行扫描器，从 HTTP 响应体读 SSE 行
	body     io.Closer      // HTTP 响应体，Close 时用
	finished bool           // 是否已读完（收到 [DONE] 或扫描器到底）
	usage    Usage          // 流末尾解析到的 token 用量统计
}

// Role 表示消息在对话中的身份。
// 用自定义类型 Role（底层是 string）而不是直接用 string，
// 好处是编译时能检查参数类型，防止传错别的字符串进来
type Role string

const (
	// MaxErrorBodyDrainBytes 是出错时最多读多少字节的响应体。
	// 限 4096 字节是为了把响应体排干让连接复用，但又不会读太多占内存
	MaxErrorBodyDrainBytes int64 = 4096

	// 下面五个是消息角色常量，对应 OpenAI 协议里的 role 字段取值
	RoleSystem    Role = "system"    // 系统指令，设定助手人格和行为规则
	RoleUser      Role = "user"      // 用户输入
	RoleAssistant Role = "assistant" // 助手回复
	RoleTool      Role = "tool"      // 工具调用结果返回（新版 function calling）
	RoleFunction  Role = "function"  // 旧版函数调用（已废弃，预留兼容）
)

// ChatMessage 是一条对话消息
// 比如 {Role:"user", Content:"你好"} 表示用户说了"你好"
type ChatMessage struct {
	Role    Role   `json:"role"`    // 消息角色：system/user/assistant/tool/function
	Content string `json:"content"` // 消息内容
}

// ChatRequest 是一次对话请求。
// Model 每次调用指定，便于同一 Provider 实例调不同模型。
//
// 举例：你用同一个 OpenAICompat 实例，这次传 Model="deepseek-chat"，
// 下次传 Model="deepseek-reasoner"，就能调不同模型，不用重建 Provider
type ChatRequest struct {
	Model    string        `json:"model"`                    // 要调的模型名，如 deepseek-chat
	Messages []ChatMessage `json:"messages"`                // 对话历史，包含所有轮次的消息
	Stream        bool           `json:"stream,omitempty"`         // true 时走 SSE 流式，false 走非流式
	StreamOptions *StreamOptions `json:"stream_options,omitempty"` // 仅流式请求用，控制流式行为（如返回用量）
}

// StreamOptions 控制流式响应的附加行为。
type StreamOptions struct {
	// IncludeUsage=true 时，API 在流结束前（[DONE] 前）返回整次请求的 token 用量统计
	// 默认不返回，需要手动开启
	IncludeUsage bool `json:"include_usage"`
}

// Usage 记录 token 消耗，给埋点和成本统计用。
//
// 什么是 token：模型不是按字数计费，而是按 token 计费。
// token 大致等于"一个词或几个字"。比如"你好"可能算 2 个 token，"hello"算 1 个 token。
// PromptTokens 是输入消耗，CompletionTokens 是输出消耗，TotalTokens 是两者之和。
type Usage struct {
	PromptTokens            int                     `json:"prompt_tokens"`              // 输入 token 数（你发给模型的内容）
	CompletionTokens        int                     `json:"completion_tokens"`          // 输出 token 数（模型生成的内容）
	TotalTokens             int                     `json:"total_tokens"`               // 总 token 数 = Prompt + Completion
	PromptCacheHitTokens    int                     `json:"prompt_cache_hit_tokens"`    // 缓存命中的 token 数（省钱的）
	PromptCacheMissTokens   int                     `json:"prompt_cache_miss_tokens"`  // 缓存未命中的 token 数（没省到钱的）
	PromptTokensDetails     PromptTokensDetails     `json:"prompt_tokens_details"`     // 输入 token 明细（有些 API 用这个字段返回缓存命中数）
	CompletionTokensDetails CompletionTokensDetails `json:"completion_tokens_details"` // 输出 token 明细（如推理 token）
}

// PromptTokensDetails 兼容部分 OpenAI 风格服务返回的输入 token 明细。
// 有些 API（如 OpenAI 官方）不直接返回 prompt_cache_hit_tokens，
// 而是放在这个子结构体的 cached_tokens 字段里，所以两个地方都要看
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"` // 缓存命中的输入 token 数
}

// CompletionTokensDetails 是模型输出 token 的细分统计。
// 比如推理模型（如 DeepSeek-R1）会输出"思考过程"，这部分也消耗 token，
// 但和最终回复分开统计
type CompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"` // 推理 token 数（模型内部思考过程消耗的 token）
}

// ChatResponse 是非流式对话的返回结果。
// Complete 方法返回这个。流式对话不返回它，而是返回 StreamReader
type ChatResponse struct {
	Content string // 助手回复的完整文本
	Usage   Usage  // token 消耗
	Model   string // 实际使用的模型名（API 可能返回和请求不同的值）
}

// APIResponse 是 OpenAI 兼容 API 的原始响应结构。
// 给 openai_compat.go 解析用，外面的人不直接碰。
//
// 一个典型的响应 JSON 长这样：
//
//	{"choices": [{"message": {"role": "assistant", "content": "你好！"}}],
//	 "usage": {"prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13},
//	 "model": "deepseek-chat"}
type APIResponse struct {
	Choices []APIChoice `json:"choices"` // 候选回复列表，一般只有一个
	Usage   Usage       `json:"usage"`   // token 用量统计
	Model   string      `json:"model"`   // 实际使用的模型名
}

// APIChoice 是响应中的一条候选回复。
// OpenAI 协议里 choices 是数组（理论上支持一次返回多个候选），但实际一般只用第一个
type APIChoice struct {
	Message ChatMessage `json:"message"` // 模型生成的完整回复（有 role 和 content）
}

// --- 流式响应结构 ---

// StreamResponse 是 SSE 单块 JSON 的结构。
// 流式时 choices[0].delta 只含增量文本（非完整 message）。
//
// 和非流式 APIResponse 的区别：
//   非流式里叫 message（完整回复），流式里叫 delta（增量文本）。
//   比如模型回复"你好"，流式会分成两块：
//	  第一块 delta={"role":"assistant", "content":""}  （首块只有 role）
//	  第二块 delta={"content":"你"}                    （增量文本）
//	  第三块 delta={"content":"好"}                    （增量文本）
type StreamResponse struct {
	Choices []StreamChoice `json:"choices"`           // 候选回复列表
	Usage   *Usage         `json:"usage,omitempty"`    // include_usage=true 时在最后一个数据块返回（可能为 nil）
}

// StreamChoice 是流式响应中的一条候选。
// Delta.Content 是本次增量文本，可能为空（首块只有 role）。
type StreamChoice struct {
	Delta struct {
		Role    string `json:"role"`    // 角色，首块会有 "assistant"，后续块一般为空
		Content string `json:"content"` // 增量文本，每次一小段
	} `json:"delta"`
	// FinishReason 是结束原因："stop"=正常说完，"length"=达到长度上限被截断，nil=还没结束
	FinishReason string `json:"finish_reason"`
}
