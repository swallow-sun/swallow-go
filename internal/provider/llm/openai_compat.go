// 本文件实现 OpenAI Chat Completions 兼容协议及 SSE 流解析。
package llm

import (
	"bufio"         // SSE 逐行扫描
	"bytes"         // 把请求体 JSON 转成 io.Reader
	"context"       // 传 context 给 HTTP 请求
	"encoding/json" // 编解码 JSON
	"fmt"           // 格式化 error
	"io"            // 读取 HTTP 响应体
	"net/http"      // 发 HTTP 请求
	"strings"       // 前缀匹配 SSE data 行
)

// NewOpenAICompat 用 Config 创建一个 Provider 实例。
func NewOpenAICompat(cfg Config) *OpenAICompat {
	return &OpenAICompat{
		config: cfg,
		client: &http.Client{},
	}
}

// Complete 实现非流式对话。
func (p *OpenAICompat) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	// 1. 拼 URL：base_url + "/chat/completions"
	url := p.config.BaseURL + "/chat/completions"

	// 2. JSON 编码请求体
	body, err := json.Marshal(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	// 3. 构造 HTTP 请求
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)

	// 4. 发请求
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	// 5. 检查状态码，非 2xx 把 body 读出来放进 error
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return ChatResponse{}, fmt.Errorf("api error: status %d, body: %s", resp.StatusCode, errBody)
	}

	// 6. 解析响应
	var apiResp APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return ChatResponse{}, fmt.Errorf("decode response: %w", err)
	}

	// 7. 组装返回
	if len(apiResp.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("no choices in response")
	}

	return ChatResponse{
		Content: apiResp.Choices[0].Message.Content,
		Usage:   apiResp.Usage,
		Model:   apiResp.Model,
	}, nil
}

// Stream 实现流式对话。
// 请求带 stream:true，响应是 SSE 格式（每行 data: {JSON}，末尾 data: [DONE]）。
// 调用方拿到 StreamReader 后循环调 Next() 逐块读取。
func (p *OpenAICompat) Stream(ctx context.Context, req ChatRequest) (StreamReader, error) {
	// 1. 标记流式
	req.Stream = true
	req.StreamOptions = &StreamOptions{IncludeUsage: true}

	// 2. 编码请求体
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// 3. 构造 HTTP 请求
	url := p.config.BaseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	// 4. 发请求（不关闭 body，留给 StreamReader 逐行读）
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}

	// 5. 检查状态码
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("api error: status %d, body: %s", resp.StatusCode, errBody)
	}

	// 6. 返回 StreamReader，调用方负责读完后 Close
	return &sseStreamReader{
		scanner:  newSSEScanner(resp.Body),
		body:     resp.Body,
		finished: false,
	}, nil
}

// newSSEScanner 创建用于读取 SSE 响应的逐行扫描器。
// 最大单行限制提升到 1 MiB，避免较大增量块触发 Scanner 默认上限。
func newSSEScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	return scanner
}

// Next 读取下一块增量文本。
// chunk 非空 = 有内容，done=true = 流结束，err 非 nil = 出错。
func (s *sseStreamReader) Next() (chunk string, done bool, err error) {
	if s.finished {
		return "", true, nil
	}

	for s.scanner.Scan() {
		line := s.scanner.Text()

		// SSE 格式：以 "data: " 开头
		if !strings.HasPrefix(line, "data: ") {
			continue // 跳过空行、注释行、event 行等
		}

		data := strings.TrimPrefix(line, "data: ")

		// [DONE] 标记流结束
		if data == "[DONE]" {
			s.finished = true
			return "", true, nil
		}

		// 解析 JSON 块
		var streamResp StreamResponse
		if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
			// 跳过无法解析的块（某些实现会发心跳注释）
			continue
		}
		if streamResp.Usage != nil {
			s.usage = *streamResp.Usage
		}

		if len(streamResp.Choices) == 0 {
			continue
		}

		content := streamResp.Choices[0].Delta.Content
		if content == "" {
			continue // 首块通常只有 role 没有 content，跳过
		}

		return content, false, nil
	}

	// scanner 结束，检查是否有读错误
	if err := s.scanner.Err(); err != nil {
		return "", true, fmt.Errorf("read stream: %w", err)
	}

	// 正常结束但没收到 [DONE]
	s.finished = true
	return "", true, nil
}

// Usage 返回流中最后解析到的 token 用量。
// 上游通常在 [DONE] 前的独立数据块中返回，因此应在读取完成后调用。
func (s *sseStreamReader) Usage() Usage {
	return s.usage
}

// Close 关闭底层 HTTP 响应体，必须调用。
func (s *sseStreamReader) Close() error {
	return s.body.Close()
}
