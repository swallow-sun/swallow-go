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
	// context 传给 Provider 接口方法, 控制超时取消
	"context"
)

// Config 是 TTS Provider 的初始化配置.
// edge-tts 不需要 API key, 只需指定语音和输出格式.
type Config struct {
	// Voice 是语音名称, 如 "zh-CN-XiaoxiaoNeural" (女声晓晓), "zh-CN-YunxiNeural" (男声云希)
	Voice string
	// OutputFormat 是音频输出格式, 如 "audio-48khz-192kbitrate-mono-mp3"
	// edge-tts 用 WebSocket 协议, 通过 output format 字段控制
	OutputFormat string
	// Rate 是语速, 如 "+0%", "-10%", "+20%"
	// 正数加速, 负数减速, 0% 是正常速度
	Rate string
	// Volume 是音量, 如 "+0%", "-50%"
	// 正数加大, 负数减小, 0% 是正常音量
	Volume string
	// Pitch 是音调, 如 "+0Hz", "-50Hz"
	// 正数升高, 负数降低
	Pitch string
}

// Provider 是跟具体 TTS 供应商无关的调用接口.
type Provider interface {
	// Synthesize 把文字转成语音.
	// 传入文本, 返回音频字节切片和错误.
	Synthesize(ctx context.Context, req SynthesizeRequest) (SynthesizeResponse, error)
}

// SynthesizeRequest 是一次语音合成请求.
type SynthesizeRequest struct {
	// Text 是要合成语音的文字
	Text string
	// Voice 覆盖默认语音 (可选), 空字符串用 Config 里的默认语音
	Voice string
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

	// DefaultVoice 是默认语音 (中文女声晓晓).
	DefaultVoice = "zh-CN-XiaoxiaoNeural"

	// DefaultOutputFormat 是默认输出格式 (16kHz 16-bit 单声道 PCM WAV).
	// edge-tts 支持 "riff-16khz-16bit-mono-pcm" 格式, 输出的是裸 PCM 数据 (不带 WAV 头).
	// Go 侧在 Synthesize 完成后自动加上 WAV 头, C++ 侧 waveOut 直接播放.
	DefaultOutputFormat = "riff-16khz-16bit-mono-pcm"

	// DefaultRate 是默认语速 (正常速度).
	DefaultRate = "+0%"

	// DefaultVolume 是默认音量 (正常音量).
	DefaultVolume = "+0%"

	// DefaultPitch 是默认音调 (正常音调).
	DefaultPitch = "+0Hz"
)
