// types.go 放 ASR (语音识别) 服务的核心数据结构和接口定义.
//
// 做的事情:
//  1. 定义 Provider 接口: 跟具体 ASR 供应商无关的调用接口 (Transcribe).
//  2. 定义 TranscribeRequest/TranscribeResponse 等请求/响应结构体.
//  3. 定义 Config 配置结构体 (BaseURL/APIKey/Model).
//
// ASR = Automatic Speech Recognition, 语音转文字.
// 当前用 Groq Whisper, 兼容 OpenAI /v1/audio/transcriptions 接口.
// 换供应商只需改 base_url + api_key + model 三个配置.
package asr

import (
	// context 传给 Provider 接口方法, 控制超时取消
	"context"
	// net/http 给 OpenAICompat 用, client 字段类型是 *http.Client
	"net/http"
)

// Config 是 ASR Provider 的初始化配置.
// 比如用 Groq Whisper 就是: BaseURL="https://api.groq.com/openai/v1", APIKey="gsk_xxx", Model="whisper-large-v3".
type Config struct {
	BaseURL string // API 基础地址, 如 https://api.groq.com/openai/v1, 后面拼 /audio/transcriptions
	APIKey  string // API 密钥, 放在 HTTP 头的 Authorization: Bearer gsk_xxx 里
	Model   string // 模型名, 如 whisper-large-v3
}

// Provider 是跟具体 ASR 供应商无关的调用接口.
// 定义接口的好处: 将来换供应商时, 调用方不用改代码, 只要写一个新实现替换.
type Provider interface {
	// Transcribe 把音频数据转成文字.
	// 传入音频字节切片和格式 (如 "wav"), 返回识别文本和错误.
	Transcribe(ctx context.Context, req TranscribeRequest) (TranscribeResponse, error)
}

// TranscribeRequest 是一次语音识别请求.
type TranscribeRequest struct {
	// AudioData 是原始音频字节切片, 比如 WAV 文件的完整内容
	AudioData []byte
	// AudioFormat 是音频格式, 如 "wav", "mp3", "m4a"
	AudioFormat string
	// Language 是语言提示 (可选), 如 "zh", "en". 空字符串表示自动检测
	Language string
}

// TranscribeResponse 是语音识别的结果.
type TranscribeResponse struct {
	// Text 是识别出的完整文字
	Text string
	// Duration 是音频时长 (秒), 供应商没返回时为 0
	Duration float64
}

// OpenAICompat 是 OpenAI 兼容协议的 ASR 实现.
// Groq Whisper 兼容 OpenAI /v1/audio/transcriptions 接口, 所以可以直接用.
// 实现逻辑在 openai_compat.go.
type OpenAICompat struct {
	config Config       // 配置 (base_url, api_key, model), 构造时传入, 不可变
	client *http.Client // HTTP 客户端, 复用它发请求 (底层复用 TCP 连接)
}

// MaxAudioBytes 是单次 ASR 请求的音频大小上限 (25MB).
// Groq Whisper 限制文件大小 25MB, 超过需要分片.
const MaxAudioBytes = 25 * 1024 * 1024
