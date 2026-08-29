// siliconflow.go 实现硅基流动 (SiliconFlow) TTS API 的 HTTP 调用.
//
// 做的事情:
//  1. NewSiliconFlow: 创建 Provider 实例, 配置 API key/模型/语音/格式.
//  2. Synthesize: POST /audio/speech (stream=false), 一次性拿到完整 WAV.
//  3. StreamSynthesize: POST /audio/speech (stream=true), 流式返回 PCM 数据.
//
// 硅基流动 TTS API (兼容 OpenAI /audio/speech 接口):
//   - 端点: POST https://api.siliconflow.cn/v1/audio/speech
//   - 认证: Bearer <api_key>
//   - 请求体 JSON: model/input/voice/response_format/sample_rate/stream/speed/gain
//   - 非流式响应: 完整音频二进制 (wav/mp3/opus/pcm)
//   - 流式响应: Transfer-Encoding: chunked, 逐块返回音频二进制
//     response_format=wav 时流式数据开头带 WAV 头, 后续是 PCM
//
// 和 edge-tts 的区别:
//   - edge-tts 走 WebSocket, 不需要 key, 但在国内不稳定 (微软会拒绝连接).
//   - 硅基流动走普通 HTTP POST, 国内直连, 稳定, 需要 api_key.
//   - 硅基流动直接输出完整 WAV 文件 (带文件头), 不需要像 edge-tts 那样手动拼 WAV 头.
//
// 合成完全在云端完成，不占用本机推理算力；该实现支持流式返回。
package tts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

func truncStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// stripTonePrefix 去除 ApplyTonePrefix 拼装的情感指令前缀, 只保留纯文本.
// ApplyTonePrefix 输出格式: "用温和的语气说 <|endofprompt|>实际文本".
// 硅基流动 API 的 references (声音克隆) 模式不支持 <|endofprompt|> 指令,
// 只支持纯文本 input, 所以用声音克隆时必须剥离前缀.
// 如果文本不含 <|endofprompt|>, 说明没有加情感前缀, 原样返回.
func stripTonePrefix(text string) string {
	idx := strings.Index(text, endOfPrompt)
	if idx < 0 {
		return text
	}
	return strings.TrimSpace(text[idx+len(endOfPrompt):])
}

// audioFileToDataURI 读本地音频文件, 转 base64 data URI.
// SiliconFlow API 的 references[].audio 字段支持 URL 或 base64 data URI.
// 用本地文件时需要转成 data URI, 格式: "data:audio/<format>;base64,<data>".
// 根据文件扩展名推断音频格式 (wav/mp3 等).
func audioFileToDataURI(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read audio file %s: %w", path, err)
	}
	ext := filepath.Ext(path)
	var mime string
	switch ext {
	case ".wav":
		mime = "audio/wav"
	case ".mp3":
		mime = "audio/mp3"
	case ".m4a":
		mime = "audio/mp4"
	case ".ogg":
		mime = "audio/ogg"
	case ".flac":
		mime = "audio/flac"
	default:
		mime = "audio/wav"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// SiliconFlowTTS 是硅基流动 TTS 的实现.
// 通过 HTTP POST 调用 /audio/speech 接口, 拿到 WAV 二进制数据.
type SiliconFlowTTS struct {
	config Config // 配置 (base_url, api_key, model, voice 等), 构造时传入, 不可变
}

// UploadVoice 把参考音频上传为可复用的硅基流动自定义音色，并返回 speech: URI。
// customName 在账户内应保持唯一，text 必须与参考音频中的实际台词一致。
func (p *SiliconFlowTTS) UploadVoice(ctx context.Context, path, customName, text string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open reference audio: %w", err)
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return "", fmt.Errorf("create reference audio form: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", fmt.Errorf("copy reference audio: %w", err)
	}
	for key, value := range map[string]string{
		"model": p.config.Model, "customName": customName, "text": text,
	} {
		if err := writer.WriteField(key, value); err != nil {
			return "", fmt.Errorf("write %s field: %w", key, err)
		}
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("finish upload form: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(p.config.BaseURL, "/")+"/uploads/audio/voice", &body)
	if err != nil {
		return "", fmt.Errorf("create voice upload request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("upload reference voice: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read voice upload response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("voice upload returned %d: %s", resp.StatusCode, limitedBody(responseBody))
	}
	var result struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return "", fmt.Errorf("decode voice upload response: %w", err)
	}
	if !strings.HasPrefix(result.URI, "speech:") {
		return "", fmt.Errorf("voice upload response has no valid speech URI")
	}
	return result.URI, nil
}

// NewSiliconFlow 用 Config 创建一个硅基流动 TTS Provider 实例.
func NewSiliconFlow(cfg Config) *SiliconFlowTTS {
	if cfg.Voice == "" {
		cfg.Voice = SFDVoice
	}
	if cfg.OutputFormat == "" {
		cfg.OutputFormat = SFDOutputFormat
	}
	// 流式接口请求的是裸 PCM，响应自身不携带采样率。若请求中省略
	// sample_rate、却在本地按 16kHz 补 WAV 头，上游默认值一旦不同就会
	// 造成变速和变调。因此构造阶段统一补齐请求与本地头共同使用的值。
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = SFDSampleRate
	}
	return &SiliconFlowTTS{config: cfg}
}

// sfSpeechReq 是硅基流动 /audio/speech 请求体的 JSON 结构.
type sfSpeechReq struct {
	Model          string        `json:"model"`                // 模型名, 如 "FunAudioLLM/CosyVoice2-0.5B"
	Input          string        `json:"input"`                // 要合成语音的文字
	Voice          string        `json:"voice,omitempty"`      // 语音名称, 如 "FunAudioLLM/CosyVoice2-0.5B:alex" (和 references 互斥)
	References     []sfReference `json:"references,omitempty"` // 声音克隆参考音频 (和 voice 互斥)
	ResponseFormat string        `json:"response_format"`      // 输出格式: "wav", "mp3", "opus", "pcm"
	SampleRate     int           `json:"sample_rate,omitempty"`
	Stream         bool          `json:"stream"` // false = 一次性返回完整音频, true = 流式返回
	Speed          float64       `json:"speed,omitempty"`
	Gain           float64       `json:"gain,omitempty"`
}

// sfReference 是声音克隆参考音频的 JSON 结构 (references 数组元素).
type sfReference struct {
	Audio string `json:"audio"`          // base64 data URI 或 URL, 如 "data:audio/wav;base64,...."
	Text  string `json:"text,omitempty"` // 参考音频的转录文本 (可选, 帮助模型对齐音色)
}

// Synthesize 把文字转成 WAV 音频数据.
// 流程: 枹造 JSON 请求体 -> POST 到硅基流动 -> 读取响应体 (二进制 WAV) -> 返回.
//
// 声音克隆逻辑:
//   - 如果请求带 References (覆盖) 或 Config.ReferenceAudio (配置), 用参考音频克隆音色.
//   - 声音克隆时 references 和 voice 互斥, 只发 references 不发 voice.
//   - 没有参考音频时用 voice 预设音色.
//
// 情感指令处理:
//   - voice 模式 (无参考音频): 保留 <|endofprompt|> 指令, API 支持.
//   - references 模式 (声音克隆): 剥离 <|endofprompt|> 指令, API 不支持, 只发纯文本.
func (p *SiliconFlowTTS) Synthesize(ctx context.Context, req SynthesizeRequest) (SynthesizeResponse, error) {
	// 确定参考音频来源: 优先用请求里的 References, 其次用 Config 里的 ReferenceAudio.
	var refs []sfReference
	if len(req.References) > 0 {
		// 请求显式传了参考音频, 直接用.
		for _, r := range req.References {
			refs = append(refs, sfReference{Audio: r.Audio, Text: r.Text})
		}
	} else if p.config.ReferenceAudio != "" {
		// Config 配了参考音频路径, 读文件转 base64 data URI.
		dataURI, err := audioFileToDataURI(p.config.ReferenceAudio)
		if err != nil {
			return SynthesizeResponse{}, fmt.Errorf("read reference audio: %w", err)
		}
		// ReferenceText 是参考音频的转录文本.
		// SiliconFlow API 实测不传 text 会导致 500 错误, 必须提供.
		refs = []sfReference{{Audio: dataURI, Text: p.config.ReferenceText}}
	}

	// 声音克隆模式剥离情感指令前缀, voice 模式保留.
	ttsText := req.Text
	if len(refs) > 0 {
		ttsText = stripTonePrefix(ttsText)
	}

	// 枹造请求体 JSON.
	// 声音克隆时只发 references 不发 voice (两者互斥).
	body := sfSpeechReq{
		Model: p.config.Model,
		Input: ttsText,
		// Provider 接口约定非流式结果一定是 WAV。这里固定请求 WAV，避免配置成
		// pcm/mp3 后仍被上层按 WAV 解析。
		ResponseFormat: "wav",
		SampleRate:     p.config.SampleRate, // 16000
		Stream:         false,
		References:     refs,
	}
	if len(refs) == 0 {
		// 没有参考音频, 用预设音色.
		voice := req.Voice
		if voice == "" {
			voice = p.config.Voice
		}
		body.Voice = voice
	}
	if p.config.Speed > 0 {
		body.Speed = p.config.Speed
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return SynthesizeResponse{}, fmt.Errorf("marshal tts request: %w", err)
	}

	// 构造完整 URL: base_url + /audio/speech.
	// base_url 形如 "https://api.siliconflow.cn/v1", 拼上 "/audio/speech".
	url := p.config.BaseURL + "/audio/speech"

	// 创建 HTTP 请求, 带 context 控制超时.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return SynthesizeResponse{}, fmt.Errorf("create tts request: %w", err)
	}
	// 设置认证头和内容类型.
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	// 发送请求, 带 30 秒超时.
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return SynthesizeResponse{}, fmt.Errorf("tts http request: %w", err)
	}
	defer resp.Body.Close()

	// 非 200 状态码, 读取错误信息返回.
	if resp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(resp.Body)
		return SynthesizeResponse{}, fmt.Errorf("tts api returned %d: %s", resp.StatusCode, string(errBytes))
	}

	// 读取响应体 — 直接是 WAV 二进制数据.
	audioData, err := io.ReadAll(resp.Body)
	if err != nil {
		return SynthesizeResponse{}, fmt.Errorf("read tts response: %w", err)
	}

	if len(audioData) == 0 {
		return SynthesizeResponse{}, fmt.Errorf("tts returned empty audio")
	}
	// 硅基流动即使 stream=false 也可能返回带流式占位长度的 WAV：
	// RIFF/data 块长度为 0xFFFFFFxx。部分播放器会把它当作损坏文件并提前停止，
	// 因此保存前按实际响应长度修复两个字段。
	if err := normalizeWAVSizes(audioData); err != nil {
		return SynthesizeResponse{}, fmt.Errorf("normalize tts wav: %w", err)
	}

	// 硅基流动直接输出完整 WAV 文件 (带文件头), 不需要手动拼 WAV 头.
	return SynthesizeResponse{
		AudioData:   audioData,
		AudioFormat: "wav",
	}, nil
}

// normalizeWAVSizes 把流式 WAV 的未知长度占位值改为当前缓冲区的实际长度。
// 它保留 fmt、LIST/AIGC 等元数据块，只修改 RIFF 和 data 的尺寸字段。
func normalizeWAVSizes(wav []byte) error {
	if len(wav) < 12 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return fmt.Errorf("response is not a RIFF/WAVE file")
	}
	if uint64(len(wav)-8) > uint64(^uint32(0)) {
		return fmt.Errorf("wav is too large")
	}
	binary.LittleEndian.PutUint32(wav[4:8], uint32(len(wav)-8))
	for offset := 12; offset+8 <= len(wav); {
		chunkID := string(wav[offset : offset+4])
		chunkSize := binary.LittleEndian.Uint32(wav[offset+4 : offset+8])
		if chunkID == "data" {
			binary.LittleEndian.PutUint32(wav[offset+4:offset+8], uint32(len(wav)-(offset+8)))
			return nil
		}
		next := uint64(offset+8) + uint64(chunkSize) + uint64(chunkSize&1)
		if next > uint64(len(wav)) {
			return fmt.Errorf("invalid %q chunk size %d", chunkID, chunkSize)
		}
		offset = int(next)
	}
	return fmt.Errorf("wav has no data chunk")
}

// StreamSynthesize 流式合成: POST /audio/speech (stream=true), 返回 io.ReadCloser.
// 上游固定请求裸 PCM，Go 侧在 reader 前补一个标准 44 字节 WAV 头。这样不会把
// SiliconFlow WAV 中可变长度的 LIST/AIGC 元数据误当成 PCM，也不依赖上游头长度。
// 调用方负责关闭 reader.
//
// 和 Synthesize 的区别:
//   - Synthesize 等全部生成完再一次性返回 (首包延迟 = 总生成时间).
//   - StreamSynthesize 边生成边返回 (首包延迟 ≈ 模型开始输出的时间).
//   - 对短句子差距不大, 对长句子流式首包延迟显著低于非流式.
//
// 情感指令处理同 Synthesize: 声音克隆模式剥离 <|endofprompt|> 前缀.
func (p *SiliconFlowTTS) StreamSynthesize(ctx context.Context, req SynthesizeRequest) (io.ReadCloser, error) {
	// 确定参考音频来源 (和 Synthesize 逻辑一致).
	var refs []sfReference
	if len(req.References) > 0 {
		for _, r := range req.References {
			refs = append(refs, sfReference{Audio: r.Audio, Text: r.Text})
		}
	} else if p.config.ReferenceAudio != "" {
		dataURI, err := audioFileToDataURI(p.config.ReferenceAudio)
		if err != nil {
			return nil, fmt.Errorf("read reference audio: %w", err)
		}
		refs = []sfReference{{Audio: dataURI, Text: p.config.ReferenceText}}
	}

	// 声音克隆模式剥离情感指令前缀, voice 模式保留.
	ttsText := req.Text
	if len(refs) > 0 {
		ttsText = stripTonePrefix(ttsText)
	}

	// 构造请求体, stream=true 开启流式。流式链路固定请求裸 PCM；非流式链路仍
	// 请求合法 WAV。二者在 Go 边界都向上层暴露 WAV 语义。
	body := sfSpeechReq{
		Model:          p.config.Model,
		Input:          ttsText,
		ResponseFormat: "pcm",
		SampleRate:     p.config.SampleRate, // 16000
		Stream:         true,                // 流式
		References:     refs,
	}
	if len(refs) == 0 {
		voice := req.Voice
		if voice == "" {
			voice = p.config.Voice
		}
		body.Voice = voice
	}
	if p.config.Speed > 0 {
		body.Speed = p.config.Speed
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal tts stream request: %w", err)
	}

	url := p.config.BaseURL + "/audio/speech"

	logger.Info("SiliconFlow: POST /audio/speech (stream=true)",
		zap.String("url", url),
		zap.Int("text_chars", len(req.Text)),
		zap.String("text_preview", truncStr(req.Text, 80)),
	)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create tts stream request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	// 流式请求不用 client 级超时, 依赖 ctx 来取消.
	streamClient := &http.Client{}
	startTime := time.Now()
	resp, err := streamClient.Do(httpReq)
	if err != nil {
		logger.Error("SiliconFlow: stream request failed",
			zap.String("url", url),
			zap.Error(err),
		)
		return nil, fmt.Errorf("tts stream http request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		logger.Error("SiliconFlow: stream returned non-200",
			zap.Int("status", resp.StatusCode),
			zap.String("body", string(errBytes)),
		)
		return nil, fmt.Errorf("tts stream api returned %d: %s", resp.StatusCode, string(errBytes))
	}

	logger.Info("SiliconFlow: stream response received",
		zap.Int("status", resp.StatusCode),
		zap.String("upstream_format", "pcm"),
		zap.Int("sample_rate", p.config.SampleRate),
		zap.Duration("connect_latency", time.Since(startTime)),
	)

	// SiliconFlow 的 pcm 输出是 16-bit 单声道裸数据。为了兼容现有 StreamProvider
	// 契约，在前面补一个标准 44 字节 WAV 头；关闭返回值时仍会关闭 HTTP body。
	sampleRate := p.config.SampleRate
	if sampleRate <= 0 {
		sampleRate = SFDSampleRate
	}
	header, err := BuildPCM16MonoWAVHeader(sampleRate, 0)
	if err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("build siliconflow streaming wav header: %w", err)
	}
	return &prefixedReadCloser{
		Reader: io.MultiReader(bytes.NewReader(header), resp.Body),
		closer: resp.Body,
	}, nil
}

// prefixedReadCloser 让 io.MultiReader 同时保留底层 HTTP body 的关闭能力。
type prefixedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *prefixedReadCloser) Close() error {
	return r.closer.Close()
}
