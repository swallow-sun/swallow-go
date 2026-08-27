// types.go 放 TTS (语音合成) 服务的核心数据结构和接口定义.
//
// 做的事情:
//  1. 定义 Provider 接口: 跟具体 TTS 供应商无关的调用接口 (Synthesize).
//  2. 定义 SynthesizeRequest/SynthesizeResponse 等请求/响应结构体.
//  3. 定义 Config 配置结构体 (Voice/OutputFormat).
//
// TTS = Text To Speech, 文字转语音.
// 当前用 edge-tts, 走微软免费 TTS 服务, 不需要 API key.
// 通过 WebSocket 连接微软 Azure Speech 服务, 发 SSML 请求, 收回 MP3 音频.
package tts

import (
	"context"
	"io"
)

// Config 是 TTS Provider 的初始化配置.
// edge-tts 不需要 API key, 只需指定语音和输出格式.
// 硅基流动需要 API key, 用普通 HTTP POST.
type Config struct {
	// Provider 是 TTS 供应商名称, 如 "edge" (微软 edge-tts) 或 "siliconflow" (硅基流动)
	// 用于 deps.go 工厂函数选择创建哪个 provider
	Provider string `toml:"-"`
	// BaseURL 是 TTS 服务的 API 基础地址 (硅基流动用, edge-tts 不需要)
	// 如 "https://api.siliconflow.cn/v1"
	BaseURL string `toml:"base_url"`
	// APIKey 是调 TTS 用的密钥 (硅基流动用, edge-tts 不需要)
	APIKey string `toml:"api_key"`
	// WorkspaceID 是阿里云百炼业务空间 ID。使用公共兼容域名时可为空；
	// 使用业务空间专属域名时建议同时传入，便于服务端校验归属。
	WorkspaceID string `toml:"workspace_id"`
	// Model 是 TTS 模型名 (硅基流动用, edge-tts 不需要)
	// 如 "FunAudioLLM/CosyVoice2-0.5B"
	Model string `toml:"model"`
	// Voice 是语音名称
	// edge-tts: "zh-CN-XiaoxiaoNeural" (女声晓晓)
	// 硅基流动: "FunAudioLLM/CosyVoice2-0.5B:alex"
	Voice string `toml:"voice"`
	// ReferenceAudio 是声音克隆参考音频的本地路径 (硅基流动 CosyVoice2 用).
	// 配置后 Synthesize 会读这个文件, 转 base64 data URI, 通过 references 字段发给 API,
	// 用参考音频的音色合成语音, 此时 voice 字段不发送 (两者互斥).
	// 不配则用 voice 预设音色. 支持 .wav 格式, 建议 3-10 秒清晰人声.
	ReferenceAudio string `toml:"reference_audio"`
	// ReferenceText 是参考音频里说的文字 (转录文本).
	// SiliconFlow API 虽然文档标注可选, 但实测不传会导致 500 错误.
	// 和 ReferenceAudio 配合使用, 帮助模型对齐音色和节奏.
	ReferenceText string `toml:"reference_text"`
	// OutputFormat 是音频输出格式
	// edge-tts: "riff-16khz-16bit-mono-pcm" (输出裸 PCM, Go 侧拼 WAV 头)
	// 硅基流动: "wav" (直接输出完整 WAV 文件)
	OutputFormat string `toml:"output_format"`
	// SampleRate 是输出采样率 (硅基流动用, edge-tts 不支持)
	// 如 16000 (和 C++ waveOut 匹配)
	SampleRate int `toml:"sample_rate"`
	// Speed 是语速 (硅基流动用), 0.25~4.0, 默认 1.0
	Speed float64 `toml:"speed"`
	// Rate 是语速 (edge-tts 用), 如 "+0%", "-10%", "+20%"
	// 正数加速, 负数减速, 0% 是正常速度
	Rate string `toml:"rate"`
	// Volume 是音量 (edge-tts 用), 如 "+0%", "-50%"
	// 正数加大, 负数减小, 0% 是正常音量
	Volume string `toml:"volume"`
	// Pitch 是音调 (edge-tts 用), 如 "+0Hz", "-50Hz"
	// 正数升高, 负数降低
	Pitch string `toml:"pitch"`
}

// Provider 是跟具体 TTS 供应商无关的调用接口.
type Provider interface {
	// Synthesize 把文字转成语音.
	// 传入文本, 返回音频字节切片和错误.
	Synthesize(ctx context.Context, req SynthesizeRequest) (SynthesizeResponse, error)
}

// StreamProvider 是支持流式合成的 TTS Provider 接口.
// 实现此接口的 Provider 可以边生成边返回 PCM 数据, 降低首包延迟.
// 调用方从返回的 reader 读取: 前 44 字节是 WAV 头, 后续是 PCM16 数据块.
type StreamProvider interface {
	Provider
	// StreamSynthesize 流式合成, 返回可读取的音频流.
	// 调用方负责关闭 reader.
	StreamSynthesize(ctx context.Context, req SynthesizeRequest) (io.ReadCloser, error)
}

// SynthesizeRequest 是一次语音合成请求.
type SynthesizeRequest struct {
	// Text 是要合成语音的文字
	Text string
	// Tone 是结构化的助手语气标签。支持独立 instruction 参数的供应商直接使用；
	// 旧供应商仍可继续读取 Text 中的 <|endofprompt|> 前缀。
	Tone string
	// Voice 覆盖默认语音 (可选), 空字符串用 Config 里的默认语音
	Voice string
	// References 覆盖默认参考音频 (声音克隆, 可选).
	// 非空时用参考音频的音色合成, 忽略 Voice 字段.
	// 每个元素: audio=base64 data URI, text=参考音频的转录文本
	References []ReferenceAudio
}

// ReferenceAudio 是声音克隆的一条参考音频.
type ReferenceAudio struct {
	// Audio 是参考音频的 base64 data URI, 如 "data:audio/wav;base64,...."
	Audio string `json:"audio"`
	// Text 是参考音频中说的文字 (转录文本, 帮助模型对齐音色)
	Text string `json:"text,omitempty"`
}

// SynthesizeResponse 是语音合成的结果.
type SynthesizeResponse struct {
	// AudioData 是合成后的音频字节切片 (WAV 格式, 包含 WAV 头 + PCM 数据)
	AudioData []byte
	// AudioFormat 是音频格式, 如 "wav"
	AudioFormat string
}

// EdgeTTS 是微软 edge-tts 的实现.
// 通过 WebSocket 连接微软免费 TTS 服务, 不需要 API key.
// 实现逻辑在 edge.go.
type EdgeTTS struct {
	config Config // 配置 (voice, output_format 等), 构造时传入, 不可变
}

// 常量: edge-tts WebSocket 端点和默认值.
// 这些是微软公开的端点, 不需要认证.
const (
	// EdgeTTSWsURL 是 edge-tts 的 WebSocket 端点地址.
	// 这个地址是微软 Azure 语音服务的公开端点, 不需要 API key.
	EdgeTTSWsURL = "wss://speech.platform.bing.com/consumer/speech/synthesize/readaloud/edge/v1?TrustedClientToken=6A5AA1D5EAFF4E9FB37E23D68491D6F4"

	// DefaultVoice 是 edge-tts 默认语音 (中文女声晓晓).
	DefaultVoice = "zh-CN-XiaoxiaoNeural"

	// DefaultOutputFormat 是 edge-tts 默认输出格式 (16kHz 16-bit 单声道 PCM).
	// edge-tts 支持 "riff-16khz-16bit-mono-pcm" 格式, 输出的是裸 PCM 数据 (不带 WAV 头).
	// Go 侧在 Synthesize 完成后自动加上 WAV 头, C++ 侧 waveOut 直接播放.
	DefaultOutputFormat = "riff-16khz-16bit-mono-pcm"

	// DefaultRate 是 edge-tts 默认语速 (正常速度).
	DefaultRate = "+0%"

	// DefaultVolume 是 edge-tts 默认音量 (正常音量).
	DefaultVolume = "+0%"

	// DefaultPitch 是 edge-tts 默认音调 (正常音调).
	DefaultPitch = "+0Hz"
)

// 阿里云百炼实时 TTS 默认值。公共域名无需提前知道 Workspace ID，
// 后续可直接把 BaseURL 换成业务空间专属 wss 地址。
const (
	AliyunTTSWebSocketURL = "wss://dashscope.aliyuncs.com/api-ws/v1/inference"
	AliyunTTSModel        = "cosyvoice-v3-flash"
	AliyunTTSVoice        = "longanhuan"
	AliyunTTSSampleRate   = 16000
)

// 常量: 硅基流动 TTS 默认值.
const (
	// SFDModel 是硅基流动默认 TTS 模型 (阿里 CosyVoice2, 中文合成优秀).
	SFDModel = "FunAudioLLM/CosyVoice2-0.5B"

	// SFDVoice 是硅基流动默认语音 (Alex, 男声).
	// 可选: alex/anna/bella/benjamin/charles/claire/david/diana
	SFDVoice = "FunAudioLLM/CosyVoice2-0.5B:alex"

	// SFDOutputFormat 是硅基流动默认输出格式 (WAV, 直接输出完整文件).
	SFDOutputFormat = "wav"

	// SFDSampleRate 是硅基流动默认采样率 (16kHz, 和 C++ waveOut 匹配).
	SFDSampleRate = 16000
)
