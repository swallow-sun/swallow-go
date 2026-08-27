// zhipu.go 实现智谱 GLM-TTS 及 GLM-TTS-Clone API。
//
// 音色克隆分为三步：
//  1. 上传本地 WAV，purpose=voice-clone-input，获得 file_id；
//  2. 调用 /voice/clone，获得可复用的私有 voice ID；
//  3. 调用 /audio/speech，用该 voice ID 把任意文本合成为 WAV。
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ZhipuBaseURL      = "https://open.bigmodel.cn/api/paas/v4"
	ZhipuModel        = "glm-tts"
	ZhipuCloneModel   = "glm-tts-clone"
	ZhipuDefaultVoice = "tongtong"
)

// ZhipuTTS 调用智谱文本转语音接口。克隆完成后，把返回的 voice ID 放入 Config.Voice。
type ZhipuTTS struct {
	config Config
	client *http.Client
}

func NewZhipu(cfg Config) *ZhipuTTS {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = ZhipuBaseURL
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = ZhipuModel
	}
	if strings.TrimSpace(cfg.Voice) == "" {
		cfg.Voice = ZhipuDefaultVoice
	}
	if strings.TrimSpace(cfg.OutputFormat) == "" {
		cfg.OutputFormat = "wav"
	}
	if cfg.Speed <= 0 {
		cfg.Speed = 1
	}
	return &ZhipuTTS{
		config: cfg,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

type zhipuSpeechRequest struct {
	Model          string  `json:"model"`
	Input          string  `json:"input"`
	Voice          string  `json:"voice"`
	Speed          float64 `json:"speed"`
	Volume         float64 `json:"volume"`
	ResponseFormat string  `json:"response_format"`
}

func (p *ZhipuTTS) Synthesize(ctx context.Context, req SynthesizeRequest) (SynthesizeResponse, error) {
	text := strings.TrimSpace(stripTonePrefix(req.Text))
	if text == "" {
		return SynthesizeResponse{}, fmt.Errorf("zhipu tts: input text is empty")
	}
	voice := strings.TrimSpace(req.Voice)
	if voice == "" {
		voice = p.config.Voice
	}
	body, err := json.Marshal(zhipuSpeechRequest{
		Model: p.config.Model, Input: text, Voice: voice,
		Speed: p.config.Speed, Volume: 1, ResponseFormat: p.config.OutputFormat,
	})
	if err != nil {
		return SynthesizeResponse{}, fmt.Errorf("zhipu tts: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(p.config.BaseURL, "/")+"/audio/speech", bytes.NewReader(body))
	if err != nil {
		return SynthesizeResponse{}, fmt.Errorf("zhipu tts: create request: %w", err)
	}
	setZhipuHeaders(httpReq, p.config.APIKey)
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return SynthesizeResponse{}, fmt.Errorf("zhipu tts: request: %w", err)
	}
	defer resp.Body.Close()
	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		return SynthesizeResponse{}, fmt.Errorf("zhipu tts: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return SynthesizeResponse{}, fmt.Errorf("zhipu tts: HTTP %d: %s", resp.StatusCode, limitedBody(audio))
	}
	if len(audio) < 12 || string(audio[:4]) != "RIFF" || string(audio[8:12]) != "WAVE" {
		return SynthesizeResponse{}, fmt.Errorf("zhipu tts: response is not a WAV file")
	}
	return SynthesizeResponse{AudioData: audio, AudioFormat: "wav"}, nil
}

// ZhipuCloneResult 是音色克隆接口返回的长期可复用音色标识和试听文件标识。
type ZhipuCloneResult struct {
	Voice     string `json:"voice"`
	FileID    string `json:"file_id"`
	RequestID string `json:"request_id"`
}

// CloneVoice 上传参考音频并创建私有音色。该调用会产生外部 API 用量。
func (p *ZhipuTTS) CloneVoice(ctx context.Context, refPath, voiceName, refText, previewText string) (ZhipuCloneResult, error) {
	fileID, err := p.uploadCloneInput(ctx, refPath)
	if err != nil {
		return ZhipuCloneResult{}, err
	}
	payload := struct {
		Model     string `json:"model"`
		VoiceName string `json:"voice_name"`
		Input     string `json:"input"`
		FileID    string `json:"file_id"`
		Text      string `json:"text,omitempty"`
	}{ZhipuCloneModel, voiceName, previewText, fileID, refText}
	body, err := json.Marshal(payload)
	if err != nil {
		return ZhipuCloneResult{}, fmt.Errorf("zhipu clone: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(p.config.BaseURL, "/")+"/voice/clone", bytes.NewReader(body))
	if err != nil {
		return ZhipuCloneResult{}, fmt.Errorf("zhipu clone: create request: %w", err)
	}
	setZhipuHeaders(req, p.config.APIKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return ZhipuCloneResult{}, fmt.Errorf("zhipu clone: request: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ZhipuCloneResult{}, fmt.Errorf("zhipu clone: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return ZhipuCloneResult{}, fmt.Errorf("zhipu clone: HTTP %d: %s", resp.StatusCode, limitedBody(responseBody))
	}
	var result ZhipuCloneResult
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return ZhipuCloneResult{}, fmt.Errorf("zhipu clone: decode response: %w", err)
	}
	if result.Voice == "" {
		return ZhipuCloneResult{}, fmt.Errorf("zhipu clone: response has no voice ID")
	}
	return result, nil
}

func (p *ZhipuTTS) uploadCloneInput(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("zhipu clone: open reference audio: %w", err)
	}
	defer file.Close()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return "", fmt.Errorf("zhipu clone: create multipart file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", fmt.Errorf("zhipu clone: copy reference audio: %w", err)
	}
	if err := writer.WriteField("purpose", "voice-clone-input"); err != nil {
		return "", fmt.Errorf("zhipu clone: write purpose: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("zhipu clone: finish multipart body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(p.config.BaseURL, "/")+"/files", &body)
	if err != nil {
		return "", fmt.Errorf("zhipu clone: create upload request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("zhipu clone: upload: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("zhipu clone: read upload response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("zhipu clone: upload HTTP %d: %s", resp.StatusCode, limitedBody(responseBody))
	}
	var uploaded struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(responseBody, &uploaded); err != nil {
		return "", fmt.Errorf("zhipu clone: decode upload response: %w", err)
	}
	if uploaded.ID == "" {
		return "", fmt.Errorf("zhipu clone: upload response has no file ID")
	}
	return uploaded.ID, nil
}

func setZhipuHeaders(req *http.Request, apiKey string) {
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
}

func limitedBody(body []byte) string {
	const max = 1024
	if len(body) > max {
		return string(body[:max]) + "..."
	}
	return string(body)
}
