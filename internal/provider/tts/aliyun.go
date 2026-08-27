// aliyun.go 实现阿里云百炼 CosyVoice WebSocket 实时语音合成。
//
// 协议顺序：run-task -> task-started -> continue-task -> finish-task。
// 服务端通过 WebSocket binary 消息持续返回裸 PCM16；Provider 在最前面补标准
// WAV 头，以复用现有 gRPC 和 C++ 播放链路，不再做多余音频转码。
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// AliyunTTS 是阿里云百炼实时 TTS Provider。
type AliyunTTS struct {
	config Config
	dialer *websocket.Dialer
}

// NewAliyun 创建阿里云实时 TTS Provider，并补齐官方公共端点和模型默认值。
func NewAliyun(cfg Config) *AliyunTTS {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = AliyunTTSWebSocketURL
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = AliyunTTSModel
	}
	if strings.TrimSpace(cfg.Voice) == "" {
		cfg.Voice = AliyunTTSVoice
	}
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = AliyunTTSSampleRate
	}
	if cfg.Speed <= 0 {
		cfg.Speed = 1.0
	}
	return &AliyunTTS{config: cfg, dialer: websocket.DefaultDialer}
}

// aliyunEvent 只解析流程控制需要的响应头，忽略供应商新增字段。
type aliyunEvent struct {
	Header struct {
		TaskID       string `json:"task_id"`
		Event        string `json:"event"`
		ErrorCode    string `json:"error_code"`
		ErrorMessage string `json:"error_message"`
	} `json:"header"`
}

// aliyunStreamReader 在上层关闭 reader 时同步关闭 WebSocket，确保用户打断播放后
// 上游任务不会继续占用连接和计费字符。
type aliyunStreamReader struct {
	*io.PipeReader
	conn *websocket.Conn
	once sync.Once
}

func (r *aliyunStreamReader) Close() error {
	var closeErr error
	r.once.Do(func() {
		closeErr = r.PipeReader.Close()
		_ = r.conn.Close()
	})
	return closeErr
}

// Synthesize 使用同一套 WebSocket 流合成，最后收齐为带准确 dataSize 的标准 WAV。
func (p *AliyunTTS) Synthesize(ctx context.Context, req SynthesizeRequest) (SynthesizeResponse, error) {
	reader, err := p.StreamSynthesize(ctx, req)
	if err != nil {
		return SynthesizeResponse{}, err
	}
	defer reader.Close()

	format, initialPCM, err := ReadStreamingWAVHeader(reader)
	if err != nil {
		return SynthesizeResponse{}, fmt.Errorf("aliyun tts: parse stream header: %w", err)
	}
	rest, err := io.ReadAll(reader)
	if err != nil {
		return SynthesizeResponse{}, fmt.Errorf("aliyun tts: read PCM: %w", err)
	}
	pcm := append(initialPCM, rest...)
	if len(pcm) == 0 {
		return SynthesizeResponse{}, errors.New("aliyun tts: empty PCM response")
	}
	header, err := BuildPCM16MonoWAVHeader(int(format.SampleRate), uint32(len(pcm)))
	if err != nil {
		return SynthesizeResponse{}, err
	}
	return SynthesizeResponse{
		AudioData:   append(header, pcm...),
		AudioFormat: "wav",
	}, nil
}

// StreamSynthesize 建立一次阿里云双向流式任务。完整文本只发送一次；阿里服务端会
// 按完整语句连续生成同一条音频流，因此既能尽早返回首句，又不会创建多个声学会话。
func (p *AliyunTTS) StreamSynthesize(ctx context.Context, req SynthesizeRequest) (io.ReadCloser, error) {
	if strings.TrimSpace(p.config.APIKey) == "" {
		return nil, errors.New("aliyun tts: api_key is required")
	}
	text := strings.TrimSpace(stripTonePrefix(req.Text))
	if text == "" {
		return nil, errors.New("aliyun tts: text is required")
	}

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+strings.TrimSpace(p.config.APIKey))
	headers.Set("User-Agent", "swallow-go/1.0")
	if workspaceID := strings.TrimSpace(p.config.WorkspaceID); workspaceID != "" {
		headers.Set("X-DashScope-WorkSpace", workspaceID)
	}

	conn, response, err := p.dialer.DialContext(ctx, p.config.BaseURL, headers)
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("aliyun tts: websocket handshake HTTP %d: %w", response.StatusCode, err)
		}
		return nil, fmt.Errorf("aliyun tts: websocket dial: %w", err)
	}

	taskID := uuid.NewString()
	parameters := map[string]any{
		"text_type":   "PlainText",
		"voice":       p.config.Voice,
		"format":      "pcm",
		"sample_rate": p.config.SampleRate,
		"volume":      50,
		"rate":        p.config.Speed,
		"pitch":       1.0,
	}
	if instruction := aliyunInstruction(p.config.Voice, req.Tone); instruction != "" {
		parameters["instruction"] = instruction
	}

	runTask := map[string]any{
		"header": map[string]any{
			"action": "run-task", "task_id": taskID, "streaming": "duplex",
		},
		"payload": map[string]any{
			"task_group": "audio", "task": "tts", "function": "SpeechSynthesizer",
			"model": p.config.Model, "parameters": parameters, "input": map[string]any{},
		},
	}
	if err := conn.WriteJSON(runTask); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("aliyun tts: send run-task: %w", err)
	}
	if err := waitAliyunTaskStarted(conn, taskID); err != nil {
		_ = conn.Close()
		return nil, err
	}

	continueTask := map[string]any{
		"header": map[string]any{
			"action": "continue-task", "task_id": taskID, "streaming": "duplex",
		},
		"payload": map[string]any{"input": map[string]any{"text": text}},
	}
	finishTask := map[string]any{
		"header": map[string]any{
			"action": "finish-task", "task_id": taskID, "streaming": "duplex",
		},
		"payload": map[string]any{"input": map[string]any{}},
	}
	if err := conn.WriteJSON(continueTask); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("aliyun tts: send continue-task: %w", err)
	}
	if err := conn.WriteJSON(finishTask); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("aliyun tts: send finish-task: %w", err)
	}

	header, err := BuildPCM16MonoWAVHeader(p.config.SampleRate, 0)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	pipeReader, pipeWriter := io.Pipe()
	streamReader := &aliyunStreamReader{PipeReader: pipeReader, conn: conn}
	streamDone := make(chan struct{})

	go func() {
		defer close(streamDone)
		defer conn.Close()
		// 上层先读 WAV 头初始化声卡；随后每个 binary frame 原样写入 PCM 流。
		if _, writeErr := pipeWriter.Write(header); writeErr != nil {
			_ = pipeWriter.CloseWithError(writeErr)
			return
		}

		for {
			messageType, payload, readErr := conn.ReadMessage()
			if readErr != nil {
				_ = pipeWriter.CloseWithError(fmt.Errorf("aliyun tts: read websocket: %w", readErr))
				return
			}
			if messageType == websocket.BinaryMessage {
				if len(payload) > 0 {
					if _, writeErr := pipeWriter.Write(payload); writeErr != nil {
						_ = pipeWriter.CloseWithError(writeErr)
						return
					}
				}
				continue
			}

			event, parseErr := parseAliyunEvent(payload)
			if parseErr != nil {
				_ = pipeWriter.CloseWithError(parseErr)
				return
			}
			switch event.Header.Event {
			case "task-finished":
				_ = pipeWriter.Close()
				return
			case "task-failed":
				_ = pipeWriter.CloseWithError(aliyunTaskError(event))
				return
			}
		}
	}()

	// context 被取消时主动关闭连接，解除正在阻塞的 ReadMessage。
	go func() {
		select {
		case <-ctx.Done():
			_ = streamReader.Close()
		case <-streamDone:
		}
	}()

	return streamReader, nil
}

func waitAliyunTaskStarted(conn *websocket.Conn, taskID string) error {
	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("aliyun tts: wait task-started: %w", err)
		}
		if messageType != websocket.TextMessage {
			continue
		}
		event, err := parseAliyunEvent(payload)
		if err != nil {
			return err
		}
		if event.Header.TaskID != "" && event.Header.TaskID != taskID {
			continue
		}
		switch event.Header.Event {
		case "task-started":
			return nil
		case "task-failed":
			return aliyunTaskError(event)
		}
	}
}

func parseAliyunEvent(payload []byte) (aliyunEvent, error) {
	var event aliyunEvent
	if err := json.Unmarshal(bytes.TrimSpace(payload), &event); err != nil {
		return aliyunEvent{}, fmt.Errorf("aliyun tts: decode event: %w", err)
	}
	return event, nil
}

func aliyunTaskError(event aliyunEvent) error {
	return fmt.Errorf("aliyun tts task failed: %s: %s",
		event.Header.ErrorCode, event.Header.ErrorMessage)
}

// aliyunInstruction 只给官方明确支持情绪指令的系统音色发送 instruction。
// 龙安柔等自然陪伴音色不支持 Instruct；对它们发送该参数会导致合成失败。
func aliyunInstruction(voice, tone string) string {
	switch strings.ToLower(strings.TrimSpace(voice)) {
	case "longanhuan", "longanyang", "longhuhu_v3":
		// 这些标杆音色支持官方规定的七类结构化情绪。
	default:
		return ""
	}

	emotion := "neutral"
	switch strings.ToLower(strings.TrimSpace(tone)) {
	case "cheerful", "energetic", "smug", "coquettish":
		emotion = "happy"
	case "sad", "melancholy", "wronged", "disappointed":
		emotion = "sad"
	case "angry", "frustrated", "exasperated":
		emotion = "angry"
	}
	return "你说话的情感是" + emotion + "。"
}
