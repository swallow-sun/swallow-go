// Package llm 定义 LLM 服务的数据结构和 Provider 接口。
// 所有实现遵循 OpenAI Chat Completions 兼容协议，
// 换模型只需改 base_url + api_key + model 三个配置。
package llm

import (
	"bufio"
	"context"
	"io"
	"net/http"
)

// Config 是模型 Provider 的初始化配置。
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
}

// Provider 是与具体模型供应商无关的调用接口。
type Provider interface {
	Complete(ctx context.Context, req ChatRequest) (ChatResponse, error)
	Stream(ctx context.Context, req ChatRequest) (StreamReader, error)
}

// StreamReader 是流式模型响应读取接口。
type StreamReader interface {
	Next() (chunk string, done bool, err error)
	Usage() Usage
	Close() error
}

// OpenAICompat 是 OpenAI Chat Completions 兼容协议实现。
type OpenAICompat struct {
	config Config
	client *http.Client
}

// sseStreamReader 是 StreamReader 的 SSE 实现。
type sseStreamReader struct {
	scanner  *bufio.Scanner
	body     io.Closer
	finished bool
	usage    Usage
}

// Role 表示消息在对话中的身份。
type Role string

const (
	RoleSystem    Role = "system"    // 系统指令，设定助手人格和行为规则
	RoleUser      Role = "user"      // 用户输入
	RoleAssistant Role = "assistant" // 助手回复
	RoleTool      Role = "tool"      // 工具调用结果返回（新版 function calling）
	RoleFunction  Role = "function"  // 旧版函数调用（已废弃，预留兼容）
)

// ChatMessage 是一条对话消息
type ChatMessage struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// ChatRequest 是一次对话请求。
// Model 每次调用指定，便于同一 Provider 实例调不同模型。
type ChatRequest struct {
	Model         string         `json:"model"`
	Messages      []ChatMessage  `json:"messages"`
	Stream        bool           `json:"stream,omitempty"`         // true 时走 SSE 流式
	StreamOptions *StreamOptions `json:"stream_options,omitempty"` // 仅流式请求使用
}

// StreamOptions 控制流式响应的附加行为。
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"` // 在 [DONE] 前返回整次请求的 token 用量
}

// Usage 记录 token 消耗，供埋点和成本统计用。
type Usage struct {
	PromptTokens            int                     `json:"prompt_tokens"`
	CompletionTokens        int                     `json:"completion_tokens"`
	TotalTokens             int                     `json:"total_tokens"`
	PromptCacheHitTokens    int                     `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens   int                     `json:"prompt_cache_miss_tokens"`
	PromptTokensDetails     PromptTokensDetails     `json:"prompt_tokens_details"`
	CompletionTokensDetails CompletionTokensDetails `json:"completion_tokens_details"`
}

// PromptTokensDetails 兼容部分 OpenAI 风格服务返回的输入 token 明细。
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// CompletionTokensDetails 是模型输出 token 的细分统计。
type CompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// ChatResponse 是非流式对话的返回结果。
type ChatResponse struct {
	Content string // 助手回复的完整文本
	Usage   Usage  // token 消耗
	Model   string // 实际使用的模型名（API 可能返回和请求不同的值）
}

// APIResponse 是 OpenAI 兼容 API 的原始响应结构。
// 供 openai_compat.go 解析用，调用方不直接接触。
type APIResponse struct {
	Choices []APIChoice `json:"choices"`
	Usage   Usage       `json:"usage"`
	Model   string      `json:"model"`
}

// APIChoice 是响应中的一条候选回复。
type APIChoice struct {
	Message ChatMessage `json:"message"`
}

// --- 流式响应结构 ---

// StreamResponse 是 SSE 单块 JSON 的结构。
// 流式时 choices[0].delta 只含增量文本（非完整 message）。
type StreamResponse struct {
	Choices []StreamChoice `json:"choices"`
	Usage   *Usage         `json:"usage,omitempty"` // include_usage=true 时在最后一个数据块返回
}

// StreamChoice 是流式响应中的一条候选。
// Delta.Content 是本次增量文本，可能为空（首块只有 role）。
type StreamChoice struct {
	Delta struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"delta"`
	FinishReason string `json:"finish_reason"` // stop/length/null，最后一个非空
}
