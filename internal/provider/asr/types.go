// types.go 放 ASR (语音识别) 服务的核心数据结构和接口定义.
//
// 做的事情:
//  1. 定义 Provider 接口: 跟具体 ASR 供应商无关的调用接口 (Transcribe).
//  2. 定义 TranscribeRequest/TranscribeResponse 等请求/响应结构体.
//  3. 定义 Config 配置结构体 (BaseURL/APIKey/Model).
//
// ASR = Automatic Speech Recognition，语音转文字。
// 当前支持阿里云 Qwen-ASR 和 multipart OpenAI 兼容供应商；
// 具体实现由 [asr].provider 显式选择，不做跨供应商自动降级。
package asr

import (
	// context 传给 Provider 接口方法, 控制超时取消
	"context"
	// net/http 给 OpenAICompat 用, client 字段类型是 *http.Client
	"net/http"
)

// Config 是 ASR Provider 的初始化配置。
type Config struct {
	BaseURL string // API 基础地址，具体路径由所选 Provider 追加
	APIKey  string // API 密钥, 放在 HTTP 头的 Authorization: Bearer gsk_xxx 里
	Model   string // 模型名，如 qwen3-asr-flash 或 whisper-large-v3
	// Language 是默认语种提示；auto 或空字符串表示自动检测。
	Language string
	// EnableITN 控制是否把中文数字、日期等口语结果规整成书面形式。
	// 该选项目前由阿里云 Qwen-ASR 使用，其他 Provider 会忽略。
	EnableITN bool
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
	// Language 是供应商检测到的语言，如 "zh"；未返回时为空。
	Language string
	// Emotion 是供应商识别到的语音情绪，如 "happy"；未返回时为空。
	Emotion string
}

// OpenAICompat 是 multipart /audio/transcriptions 兼容协议的 ASR 实现。
// 硅基流动和 Groq 等供应商可使用该实现。
// 实现逻辑在 openai_compat.go.
type OpenAICompat struct {
	config Config       // 配置 (base_url, api_key, model), 构造时传入, 不可变
	client *http.Client // HTTP 客户端, 复用它发请求 (底层复用 TCP 连接)
}

// Aliyun 是阿里云百炼 Qwen3-ASR-Flash 的同步 HTTP 实现。
// 阿里接口虽然位于 OpenAI 兼容路径，但音频通过 chat/completions 的
// input_audio 传递，和 multipart /audio/transcriptions 并不是同一种协议。
type Aliyun struct {
	config Config
	client *http.Client
}

// MaxAudioBytes 是单次 ASR 请求的音频大小上限 (25MB).
// Groq Whisper 限制文件大小 25MB, 超过需要分片.
const MaxAudioBytes = 25 * 1024 * 1024

// MaxAliyunAudioPayloadBytes 是阿里云 Qwen3-ASR-Flash 的 Data URL 上限。
// Base64 会让数据膨胀，因此校验的是编码后的音频载荷，而不是原始 WAV 大小。
const MaxAliyunAudioPayloadBytes = 10 * 1024 * 1024
