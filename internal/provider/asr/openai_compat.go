// openai_compat.go 实现 OpenAI 兼容的 ASR (语音识别) 协议.
//
// 做的事情:
//  1. NewOpenAICompat: 创建 Provider 实例, 配置 API 地址, 密钥, 模型名.
//  2. Transcribe: 发 HTTP POST 请求 (multipart/form-data 上传音频), 解析 JSON 响应, 返回文字.
//
// OpenAI 兼容协议: Groq Whisper 兼容 OpenAI /v1/audio/transcriptions 接口,
// 用 multipart/form-data 上传音频文件, 返回 JSON {"text": "识别的文字"}.
package asr

import (
	// bytes 构造 multipart 请求体
	"bytes"
	// context 传给 HTTP 请求, 支持超时取消
	"context"
	// encoding/json 解析响应 JSON
	"encoding/json"
	// fmt 格式化 error 信息
	"fmt"
	// io 读取 HTTP 响应体, 出错时排干
	"io"
	// mime/multipart 构造 multipart/form-data 请求体
	"mime/multipart"
	// net/http 发 HTTP 请求
	"net/http"
	// net/textproto 设置 multipart 头的 Content-Type
	"net/textproto"
)

// NewOpenAICompat 用 Config 创建一个 ASR Provider 实例.
// http.Client 用默认配置, 超时靠 context 控制.
func NewOpenAICompat(cfg Config) *OpenAICompat {
	return &OpenAICompat{
		config: cfg,
		client: &http.Client{},
	}
}

// Transcribe 把音频数据发给 ASR 供应商, 拿回识别文字.
// 流程: 构造 multipart 请求体 -> 发 POST -> 读响应 -> 解析 JSON -> 返回文字.
func (p *OpenAICompat) Transcribe(ctx context.Context, req TranscribeRequest) (TranscribeResponse, error) {
	if len(req.AudioData) == 0 {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorInvalidInput, 0, "audio data is empty", nil,
		)
	}

	// 第一步: 拼 URL. base_url + /audio/transcriptions
	// 比如 https://api.groq.com/openai/v1 + /audio/transcriptions
	url := p.config.BaseURL + "/audio/transcriptions"

	// 第二步: 构造 multipart/form-data 请求体.
	// multipart body 用 bytes.Buffer 拼接, writer 写入字段和文件.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	// 写 model 字段 (必须): 告诉 API 用哪个模型, 如 whisper-large-v3
	if err := writer.WriteField("model", p.config.Model); err != nil {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorUnavailable, 0, "write model field", err,
		)
	}

	// 写 language 字段 (可选): 如果指定了语言就传, 帮助模型更准确识别
	language := configuredLanguage(req.Language, p.config.Language)
	if language != "" {
		if err := writer.WriteField("language", language); err != nil {
			return TranscribeResponse{}, newProviderError(
				ProviderErrorUnavailable, 0, "write language field", err,
			)
		}
	}

	// 写 response_format 字段: OpenAI 兼容接口默认返回 json, 显式指定更稳妥
	if err := writer.WriteField("response_format", "json"); err != nil {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorUnavailable, 0, "write response_format field", err,
		)
	}

	// 写音频文件字段 (必须): CreateFormFile 会自动设 Content-Type 为 application/octet-stream,
	// 但有些 ASR 接口需要音频格式的 MIME, 所以我们手动设 Content-Type.
	// filename 带扩展名, 帮助 API 识别格式, 如 "audio.wav".
	filename := "audio." + req.AudioFormat
	// CreatePart 创建一个自定义头的 multipart part, 不像 CreateFormFile 固定 application/octet-stream
	hdr := make(textproto.MIMEHeader)
	// multipart 的 Content-Disposition 固定格式, name 是字段名, filename 是文件名
	hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	// Content-Type 根据格式设对应的 MIME 类型
	hdr.Set("Content-Type", mimeTypeForFormat(req.AudioFormat))
	// CreatePart 返回一个 io.Writer, 往里面写音频数据
	part, err := writer.CreatePart(hdr)
	if err != nil {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorUnavailable, 0, "create file part", err,
		)
	}
	// 把音频字节切片写进 part
	if _, err := part.Write(req.AudioData); err != nil {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorUnavailable, 0, "write audio data", err,
		)
	}

	// writer.Close() 写入结束标记 (boundary 尾部), 之后 body 才是完整的 multipart 数据
	if err := writer.Close(); err != nil {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorUnavailable, 0, "close multipart writer", err,
		)
	}

	// 第三步: 构造 HTTP 请求.
	// bytes.NewReader 把 body 的字节切片包成 io.Reader 传给 http.NewRequestWithContext
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, &body)
	if err != nil {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorUnavailable, 0, "create request", err,
		)
	}

	// 设 HTTP 请求头.
	// Content-Type 必须是 multipart/form-data + boundary, writer.Boundary() 返回 boundary 值
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	// Authorization: Bearer gsk_xxx
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)

	// 第四步: 发请求.
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorUnavailable, 0, "http request", err,
		)
	}
	// defer 在函数返回时关闭响应体, 防止连接泄漏
	defer resp.Body.Close()

	// 第五步: 限制并读取响应体。先读取才能在失败时保留供应商错误详情，
	// 同时避免异常上游返回无限大的响应占满内存。
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

	// 第六步: 解析 JSON 响应体.
	// OpenAI 兼容接口返回: {"text": "识别的文字"}
	var apiResp struct {
		Text     string  `json:"text"`     // 识别出的文字
		Duration float64 `json:"duration"` // 音频时长 (秒), 有些供应商不返回
	}
	if err := json.Unmarshal(responseBody, &apiResp); err != nil {
		return TranscribeResponse{}, newProviderError(
			ProviderErrorInvalidResponse, resp.StatusCode, "decode response", err,
		)
	}

	// 返回识别结果
	return TranscribeResponse{
		Text:     apiResp.Text,
		Duration: apiResp.Duration,
	}, nil
}

// mimeTypeForFormat 根据音频格式返回对应的 MIME 类型.
// ASR 供应商需要正确的 Content-Type 来解析音频.
func mimeTypeForFormat(format string) string {
	switch format {
	case "wav":
		return "audio/wav"
	case "mp3":
		return "audio/mpeg"
	case "m4a":
		return "audio/mp4"
	case "ogg":
		return "audio/ogg"
	case "flac":
		return "audio/flac"
	default:
		return "application/octet-stream"
	}
}
