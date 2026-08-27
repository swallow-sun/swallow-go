// aliyun.go 实现阿里云百炼 Qwen3-ASR-Flash 的同步语音识别。
//
// 官方没有单独的 Go ASR SDK 示例，本实现直接使用标准库 net/http 调用
// OpenAI 兼容的 chat/completions 接口。音频以 Base64 Data URL 放进
// input_audio.data，响应文字位于 choices[0].message.content。
package asr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// aliyunRequest 是 Qwen3-ASR-Flash OpenAI 兼容接口的请求体。
type aliyunRequest struct {
	Model      string           `json:"model"`
	Messages   []aliyunMessage  `json:"messages"`
	Stream     bool             `json:"stream"`
	ASROptions aliyunASROptions `json:"asr_options"`
}

type aliyunMessage struct {
	Role    string          `json:"role"`
	Content []aliyunContent `json:"content"`
}

type aliyunContent struct {
	Type       string           `json:"type"`
	InputAudio aliyunInputAudio `json:"input_audio"`
}

type aliyunInputAudio struct {
	Data string `json:"data"`
}

type aliyunASROptions struct {
	Language  string `json:"language,omitempty"`
	EnableITN bool   `json:"enable_itn"`
}

// aliyunResponse 只声明当前业务需要的返回字段，供应商新增字段不会影响解析。
type aliyunResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content     string `json:"content"`
			Annotations []struct {
				Type     string `json:"type"`
				Language string `json:"language"`
				Emotion  string `json:"emotion"`
			} `json:"annotations"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		Seconds float64 `json:"seconds"`
	} `json:"usage"`
}

// NewAliyun 创建阿里云 ASR Provider。
// 30 秒是单次短语音请求的兜底超时；调用方 context 仍可设置更短超时。
func NewAliyun(cfg Config) *Aliyun {
	return &Aliyun{
		config: cfg,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Transcribe 把本地音频编码为 Data URL 后发送给 Qwen3-ASR-Flash。
func (p *Aliyun) Transcribe(ctx context.Context, req TranscribeRequest) (TranscribeResponse, error) {
	if len(req.AudioData) == 0 {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorInvalidInput, 0, "audio data is empty", nil,
		)
	}

	mimeType, err := aliyunMIMEType(req.AudioFormat)
	if err != nil {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorInvalidInput, 0, err.Error(), err,
		)
	}
	prefix := "data:" + mimeType + ";base64,"
	encodedSize := len(prefix) + base64.StdEncoding.EncodedLen(len(req.AudioData))
	if encodedSize > MaxAliyunAudioPayloadBytes {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorInvalidInput,
			0,
			fmt.Sprintf("base64 audio payload is too large: %d bytes, limit %d bytes",
				encodedSize, MaxAliyunAudioPayloadBytes,
			),
			nil,
		)
	}

	dataURL := prefix + base64.StdEncoding.EncodeToString(req.AudioData)
	payload := aliyunRequest{
		Model: p.config.Model,
		Messages: []aliyunMessage{{
			Role: "user",
			Content: []aliyunContent{{
				Type:       "input_audio",
				InputAudio: aliyunInputAudio{Data: dataURL},
			}},
		}},
		Stream: false,
		ASROptions: aliyunASROptions{
			Language:  configuredLanguage(req.Language, p.config.Language),
			EnableITN: p.config.EnableITN,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorUnavailable, 0, "encode request", err,
		)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		aliyunEndpoint(p.config.BaseURL),
		bytes.NewReader(body),
	)
	if err != nil {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorUnavailable, 0, "create request", err,
		)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorUnavailable, 0, "http request", err,
		)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxASRResponseBytes+1))
	if err != nil {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorUnavailable, resp.StatusCode, "read response", err,
		)
	}
	if len(responseBody) > maxASRResponseBytes {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorInvalidResponse,
			resp.StatusCode,
			fmt.Sprintf("response exceeds %d bytes", maxASRResponseBytes),
			nil,
		)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return TranscribeResponse{}, newProviderError(
			providerHTTPErrorKind(resp.StatusCode),
			resp.StatusCode,
			compactErrorBody(responseBody),
			nil,
		)
	}

	var apiResp aliyunResponse
	if err := json.Unmarshal(responseBody, &apiResp); err != nil {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorInvalidResponse, resp.StatusCode, "decode response", err,
		)
	}
	if len(apiResp.Choices) == 0 {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorInvalidResponse, resp.StatusCode, "response contains no choices", nil,
		)
	}
	if apiResp.Choices[0].FinishReason != "stop" {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorInvalidResponse,
			resp.StatusCode,
			fmt.Sprintf("unexpected finish_reason %q", apiResp.Choices[0].FinishReason),
			nil,
		)
	}

	result := TranscribeResponse{
		Text:     strings.TrimSpace(apiResp.Choices[0].Message.Content),
		Duration: apiResp.Usage.Seconds,
	}
	for _, annotation := range apiResp.Choices[0].Message.Annotations {
		if annotation.Type == "audio_info" || result.Language == "" {
			result.Language = annotation.Language
			result.Emotion = annotation.Emotion
		}
		if annotation.Type == "audio_info" {
			break
		}
	}
	return result, nil
}

func aliyunEndpoint(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(baseURL, "/chat/completions") {
		return baseURL
	}
	return baseURL + "/chat/completions"
}

func aliyunMIMEType(format string) (string, error) {
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(format), ".")) {
	case "wav":
		return "audio/wav", nil
	case "mp3", "mpeg":
		return "audio/mpeg", nil
	case "flac":
		return "audio/flac", nil
	case "ogg", "opus":
		return "audio/ogg", nil
	case "aac":
		return "audio/aac", nil
	case "aiff":
		return "audio/aiff", nil
	case "wma":
		return "audio/x-ms-wma", nil
	case "webm":
		return "audio/webm", nil
	case "amr":
		return "audio/amr", nil
	case "avi":
		return "video/x-msvideo", nil
	case "flv":
		return "video/x-flv", nil
	case "mkv":
		return "video/x-matroska", nil
	case "wmv":
		return "video/x-ms-wmv", nil
	default:
		return "", fmt.Errorf("aliyun asr: unsupported audio format %q", format)
	}
}
