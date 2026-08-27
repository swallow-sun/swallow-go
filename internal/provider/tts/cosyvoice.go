// cosyvoice.go 实现本地 CosyVoice2 TTS provider.
//
// 和 siliconflow.go 的区别:
//   - siliconflow: 调硅基流动云 API, 互联网中转, 非本地 GPU.
//   - cosyvoice: 调本地 tts_server.py (FastAPI + CosyVoice2), GPU 直推理, 低延迟.
//
// tts_server.py 端点:
//   - POST /tts:        非流式, 返回完整 WAV.
//   - POST /tts/stream: 流式, 先发 44 字节 WAV 头, 再逐块发 PCM16.
//
// 语气控制:
//   tts_server.py 内部有 tone -> 情感指令的映射表, 和 tone.go 完全一致.
//   但 Go 侧已经用 ApplyTonePrefix 拼好了情感前缀, 传给 tts_server.py 时 tone 留空.
//   tts_server.py 收到空 tone 不加前缀, 直接用 Go 拼好的文本.
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// CosyVoiceConfig 是本地 CosyVoice2 TTS 服务的配置.
type CosyVoiceConfig struct {
	// BaseURL 是 tts_server.py 的地址, 如 "http://127.0.0.1:9880".
	BaseURL string `toml:"base_url"`
}

// CosyVoiceTTS 是本地 CosyVoice2 TTS 的实现.
// 通过 HTTP 调用 tts_server.py, 支持流式和非流式两种模式.
type CosyVoiceTTS struct {
	config CosyVoiceConfig
	client *http.Client
}

// NewCosyVoice 用配置创建一个本地 CosyVoice2 TTS Provider 实例.
func NewCosyVoice(cfg CosyVoiceConfig) *CosyVoiceTTS {
	return &CosyVoiceTTS{
		config: cfg,
		// 非流式请求超时 120s (GPU 推理可能较慢).
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

// cosyVoiceReq 是 tts_server.py /tts 和 /tts/stream 的请求体.
// 和 tts_server.py 里 TtsRequest 对应.
type cosyVoiceReq struct {
	Text string `json:"text"` // 要合成的文本 (已加情感前缀)
	Tone string `json:"tone"` // 语气标签 (空串表示不加, Go 侧已处理)
}

// Synthesize 非流式合成: POST /tts, 返回完整 WAV.
func (p *CosyVoiceTTS) Synthesize(ctx context.Context, req SynthesizeRequest) (SynthesizeResponse, error) {
	// 构造请求体. tone 留空: Go 侧已用 ApplyTonePrefix 拼好情感前缀.
	body, err := json.Marshal(cosyVoiceReq{
		Text: req.Text,
		Tone: "",
	})
	if err != nil {
		return SynthesizeResponse{}, fmt.Errorf("marshal cosyvoice request: %w", err)
	}

	url := p.config.BaseURL + "/tts"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return SynthesizeResponse{}, fmt.Errorf("create cosyvoice request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return SynthesizeResponse{}, fmt.Errorf("cosyvoice http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(resp.Body)
		return SynthesizeResponse{}, fmt.Errorf("cosyvoice api returned %d: %s", resp.StatusCode, string(errBytes))
	}

	audioData, err := io.ReadAll(resp.Body)
	if err != nil {
		return SynthesizeResponse{}, fmt.Errorf("read cosyvoice response: %w", err)
	}

	if len(audioData) == 0 {
		return SynthesizeResponse{}, fmt.Errorf("cosyvoice returned empty audio")
	}

	return SynthesizeResponse{
		AudioData:   audioData,
		AudioFormat: "wav",
	}, nil
}

// StreamSynthesize 流式合成: POST /tts/stream, 返回 io.ReadCloser.
// 调用方从 reader 读取: 前 44 字节是 WAV 头, 后续是 PCM16 数据块.
// 调用方负责关闭 reader.
func (p *CosyVoiceTTS) StreamSynthesize(ctx context.Context, req SynthesizeRequest) (io.ReadCloser, error) {
	body, err := json.Marshal(cosyVoiceReq{
		Text: req.Text,
		Tone: "",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal cosyvoice stream request: %w", err)
	}

	url := p.config.BaseURL + "/tts/stream"
	logger.Info("CosyVoice: POST /tts/stream",
		zap.String("url", url),
		zap.Int("text_chars", len(req.Text)),
		zap.String("text_preview", truncStr(req.Text, 80)),
	)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create cosyvoice stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// 流式请求不用 client 级超时, 用 context 控制.
	// 创建一个不带 timeout 的 client, 依赖 ctx 来取消.
	streamClient := &http.Client{}
	startTime := time.Now()
	resp, err := streamClient.Do(httpReq)
	if err != nil {
		logger.Error("CosyVoice: stream request failed",
			zap.String("url", url),
			zap.Error(err),
		)
		return nil, fmt.Errorf("cosyvoice stream http request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		logger.Error("CosyVoice: stream returned non-200",
			zap.Int("status", resp.StatusCode),
			zap.String("body", string(errBytes)),
		)
		return nil, fmt.Errorf("cosyvoice stream returned %d: %s", resp.StatusCode, string(errBytes))
	}

	logger.Info("CosyVoice: stream response received",
		zap.Int("status", resp.StatusCode),
		zap.Duration("connect_latency", time.Since(startTime)),
	)

	return resp.Body, nil
}

// truncStr 截断字符串到 maxLen 字符, 超出加 "...", 用于日志预览.
func truncStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
