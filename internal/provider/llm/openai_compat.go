// openai_compat.go 实现 OpenAI Chat Completions 兼容协议及 SSE 流解析.
//
// 做的事情:
//  1. NewOpenAICompat:创建 Provider 实例,配置 API 地址,密钥,模型名.
//  2. Complete:非流式调用--发 HTTP POST 请求,解析 JSON 响应,返回完整回复和 token 用量.
//  3. Stream:流式调用--发 HTTP POST 请求(stream=true),返回 sseStreamReader 逐块读取.
//  4. sseStreamReader:实现 StreamReader 接口,逐行解析 SSE 数据帧,提取增量文本和最终 token 用量.
//
// OpenAI 兼容协议:大多数模型厂商(DeepSeek,月之暗面,智谱等)都兼容这套 API 格式,
// 换模型只需改 base_url + api_key + model 三个配置,不用改代码.
package llm

import (
	// bufio 给 SSE 逐行扫描用.SSE 协议是按行发的,每行一个事件,用 bufio.Scanner 一行一行读最方便
	"bufio"
	// bytes 把请求体 JSON 转成 io.Reader.HTTP 请求的 body 要求是 io.Reader 类型,JSON 编码出来是 []byte,用 bytes.NewReader 包一下就能传给 http.NewRequest
	"bytes"
	// context 传给 HTTP 请求,支持超时取消.比如调用方给 60 秒超时,HTTP 请求到 60 秒还没返回就自动取消
	"context"
	// encoding/json 编解码 JSON.请求体要转成 JSON 发出去,响应体要从 JSON 解析出来
	"encoding/json"
	// fmt 格式化 error 信息,把底层错误包一层加上上下文
	"fmt"
	// io 读取 HTTP 响应体.非流式调用要读完整响应体;出错时还要把响应体排干
	"io"
	// net/http 发 HTTP 请求.构造请求,设 Header,发请求,拿响应都靠它
	"net/http"
	// strings 前缀匹配 SSE 的 data 行.SSE 每行格式是 "data: {JSON}",用 strings.HasPrefix 判断是不是 data 行
	"strings"
)

// NewOpenAICompat 用 Config 创建一个 Provider 实例.
// 传进来一个 Config(里面有 base_url,api_key,model),返回一个 *OpenAICompat.
// http.Client 用默认配置--默认超时是 0 表示不超时,靠 context 的超时来控制.
func NewOpenAICompat(cfg Config) *OpenAICompat {
	return &OpenAICompat{
		config: cfg,            // 存配置,后面发请求要用 base_url 和 api_key
		client: &http.Client{}, // 新建一个 HTTP 客户端,复用它发请求(底层会复用 TCP 连接)
	}
}

// Complete 实现非流式对话.
// 调一次 API,等模型把完整回复生成好,一次性返回.
// 参数 ctx 控制超时,req 是对话请求(消息列表 + 模型名).
// 返回 ChatResponse(完整回复文本 + token 用量 + 模型名)和错误.
func (p *OpenAICompat) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	// 第一步:拼 URL.base_url 是配置好的 API 地址(如 https://api.deepseek.com/v1),
	// 再加上 /chat/completions 就是完整的对话接口地址
	url := p.config.BaseURL + "/chat/completions"

	// 第二步:把请求结构体 req 转成 JSON 字节切片.
	// json.Marshal 是 Go 标准库里"把 Go 结构体转成 JSON 字符串"的函数.
	// 比如把 ChatRequest{Model:"deepseek-chat", Messages:[...]} 转成 {"model":"deepseek-chat","messages":[...]}
	body, err := json.Marshal(req)
	if err != nil {
		// 转失败了直接返回错误,加了 "marshal request" 前缀方便定位是哪一步出的问题
		return ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	// 第三步:构造 HTTP 请求对象.
	// http.NewRequestWithContext 比 http.NewRequest 多一个 context 参数,
	// 这样 HTTP 请求会跟着 context 一起超时或取消.
	// 参数:context,方法 "POST",URL,body(bytes.NewReader 把 []byte 包成 io.Reader)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("new request: %w", err)
	}

	// 设 HTTP 请求头.OpenAI 兼容协议要求:
	// Content-Type: application/json --告诉服务器我发的是 JSON
	httpReq.Header.Set("Content-Type", "application/json")
	// Authorization: Bearer sk-xxx --API 密钥,"Bearer " 是固定的前缀格式
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)

	// 第四步:发请求.client.Do 发出 HTTP 请求并等响应回来.
	// resp 是 HTTP 响应对象,里面有状态码,响应头,响应体
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("http request: %w", err)
	}
	// defer 在函数返回时关闭响应体,防止连接泄漏.
	// HTTP 响应体必须关闭,不然底层的 TCP 连接没法还给连接池复用
	defer resp.Body.Close()

	// 第五步:检查 HTTP 状态码.200 才是成功,其他都是出错.
	// 响应体里可能有密钥等敏感信息,不拼进 error 也不打日志,避免泄漏.
	if resp.StatusCode != http.StatusOK {
		// io.LimitReader 限制最多读 MaxErrorBodyDrainBytes(4096 字节),
		// io.Copy + io.Discard 就是"读出来扔掉",目的是把响应体排干,
		// 让底层 TCP 连接能还给连接池复用(不排干连接就没法复用)
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, MaxErrorBodyDrainBytes))
		return ChatResponse{}, fmt.Errorf("api error: status %d", resp.StatusCode)
	}

	// 第六步:解析 JSON 响应体.
	// json.NewDecoder 创建一个 JSON 解码器,从 resp.Body 里读数据.
	// .Decode(&apiResp) 把 JSON 解析到 apiResp 结构体里.
	// 比 json.Unmarshal 的好处:直接从 io.Reader 读,不用先把 body 读到 []byte
	var apiResp APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return ChatResponse{}, fmt.Errorf("decode response: %w", err)
	}

	// 第七步:检查有没有 choices.如果 API 返回了空 choices,说明出问题了
	if len(apiResp.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("no choices in response")
	}

	// 组装最终返回的 ChatResponse.
	// Choices[0].Message.Content 就是模型生成的完整回复文本
	// Usage 是 token 消耗统计
	// Model 是 API 实际使用的模型名(有时候 API 会返回和请求不同的模型名)
	return ChatResponse{
		Content: apiResp.Choices[0].Message.Content,
		Usage:   apiResp.Usage,
		Model:   apiResp.Model,
	}, nil
}

// Stream 实现流式对话.
// 请求带 stream:true,响应是 SSE 格式(每行 data: {JSON},末尾 data: [DONE]).
// 调用的人拿到 StreamReader 后循环调 Next() 逐块读取.
//
// SSE(Server-Sent Events)协议:服务器推流用的 HTTP 协议.
// 模型一边生成一边推,每个 token 或一小段文本作为一个 data 块发过来.
// 好处是用户不用等模型整段生成完才看到文字,可以实时看到打字效果.
func (p *OpenAICompat) Stream(ctx context.Context, req ChatRequest) (StreamReader, error) {
	// 第一步:标记流式请求
	// req.Stream=true 告诉 API 我要流式响应(SSE 格式),不是一次性返回
	req.Stream = true
	// StreamOptions.IncludeUsage=true 告诉 API 在流结束时([DONE] 前)返回 token 用量统计
	req.StreamOptions = &StreamOptions{IncludeUsage: true}

	// 第二步:把请求结构体转成 JSON 字节切片
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// 第三步:构造 HTTP 请求
	url := p.config.BaseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	// Content-Type: application/json --发的是 JSON
	httpReq.Header.Set("Content-Type", "application/json")
	// Authorization: Bearer sk-xxx --API 密钥
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	// Accept: text/event-stream --告诉服务器我要的是 SSE 流,不是普通 JSON 响应
	httpReq.Header.Set("Accept", "text/event-stream")

	// 第四步:发请求.
	// 注意:这里不关闭 body,留给 StreamReader 逐行读.
	// 因为 SSE 流是持续推送的,resp.Body 要在后面 Next() 里一直读
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}

	// 第五步:检查状态码.流式请求如果出错(密钥不对,额度用完等),
	// API 通常返回 4xx/5xx,不会有 SSE 流,这里提前拦截
	if resp.StatusCode != http.StatusOK {
		// 排干响应体让连接能复用
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, MaxErrorBodyDrainBytes))
		// 出错了要关闭 body,不然连接泄漏
		resp.Body.Close()
		return nil, fmt.Errorf("api error: status %d", resp.StatusCode)
	}

	// 第六步:返回 sseStreamReader,调用的人负责读完后调 Close 关闭 body
	return &sseStreamReader{
		scanner:  newSSEScanner(resp.Body), // 逐行扫描器,从 resp.Body 读 SSE 行
		body:     resp.Body,                // 存 body 引用,Close 时用
		finished: false,                   // 还没读完
	}, nil
}

// newSSEScanner 创建用于读取 SSE 响应的逐行扫描器.
// 最大单行限制提升到 1 MiB,避免较大增量块触发 Scanner 默认上限.
//
// bufio.Scanner 默认的 buffer 最大是 64KB,超过就报错.
// 但有些模型一次推一大块文本(比如输出长代码),可能超过 64KB,所以手动把上限提到 1MiB.
func newSSEScanner(r io.Reader) *bufio.Scanner {
	// bufio.NewScanner 从 io.Reader 创建一个逐行扫描器
	scanner := bufio.NewScanner(r)
	// 重新设置 buffer:初始 64KB,最大 1MiB(1024*1024).
	// 初始是 64*1024=64KB,超过 64KB 后会自动扩容,最多扩到 1MiB
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	return scanner
}

// Next 读取下一块增量文本.
// chunk 非空 = 有内容,done=true = 流结束,err 非 nil = 出错.
//
// 调用的人写个 for 循环反复调 Next():
//
//	for {
//		chunk, done, err := reader.Next()
//		if err != nil { ... }
//		if done { break }
//		// 处理 chunk
//	}
func (s *sseStreamReader) Next() (chunk string, done bool, err error) {
	// 如果之前已经读完了(收到过 [DONE] 或扫描器到底),直接返回 done=true
	if s.finished {
		return "", true, nil
	}

	// scanner.Scan() 逐行读.返回 true = 读到一行,false = 读完了或出错
	for s.scanner.Scan() {
		// scanner.Text() 返回当前行的文本(不含换行符)
		line := s.scanner.Text()

		// SSE 格式:以 "data: " 开头的行才是数据行
		// SSE 协议里还有空行(分隔事件),event 行,comment 行等,这些都要跳过
		if !strings.HasPrefix(line, "data: ") {
			continue // 跳过空行,注释行,event 行等
		}

		// 去掉 "data: " 前缀,拿到后面的 JSON 内容
		// strings.TrimPrefix 如果前缀匹配就删掉前缀返回剩余部分
		data := strings.TrimPrefix(line, "data: ")

		// SSE 流的结束标记 [DONE].
		// OpenAI 兼容协议规定:最后一个 data 块是 "data: [DONE]",表示流结束了
		if data == "[DONE]" {
			s.finished = true // 标记已读完,下次调 Next 直接返回 done=true
			return "", true, nil
		}

		// 把 data 行的 JSON 解析成 StreamResponse 结构体.
		// json.Unmarshal 把 JSON 字符串转成 Go 结构体.
		// data 是 string,json.Unmarshal 要 []byte,所以用 []byte(data) 转一下
		var streamResp StreamResponse
		if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
			// 跳过无法解析的块(某些实现会发心跳注释,比如 ": keep-alive")
			continue
		}

		// 如果这一块带了 token 用量统计(include_usage=true 时,最后一个块会带),存起来
		// Usage 是指针类型 *Usage,nil 表示这一块没有用量信息
		if streamResp.Usage != nil {
			s.usage = *streamResp.Usage
		}

		// 流式响应的 choices 可能为空(比如只有 usage 没有内容的尾部块)
		if len(streamResp.Choices) == 0 {
			continue
		}

		// Choices[0].Delta.Content 是这一块的增量文本
		content := streamResp.Choices[0].Delta.Content
		if content == "" {
			// 首块通常只有 role 没有 content(比如 {"delta":{"role":"assistant"}}),跳过
			continue
		}

		// 返回这一块的增量文本,done=false 表示流还没结束
		return content, false, nil
	}

	// scanner.Scan() 返回 false,说明读到末尾了.先检查是不是出错导致的中断
	// scanner.Err() 返回 nil 表示正常读完,非 nil 表示读取过程中出错了
	if err := s.scanner.Err(); err != nil {
		return "", true, fmt.Errorf("read stream: %w", err)
	}

	// 到这里说明正常读完但没收到 [DONE](有些 API 不按规矩发 [DONE])
	// 也算正常结束,标记 finished 并返回 done=true
	s.finished = true
	return "", true, nil
}

// Usage 返回流中最后解析到的 token 用量.
// 上游通常在 [DONE] 前的独立数据块中返回,因此应在读取完成后调用.
func (s *sseStreamReader) Usage() Usage {
	return s.usage
}

// Close 关闭底层 HTTP 响应体,必须调用.
// 流读完后不关 body 会导致 TCP 连接泄漏,连接池里会攒一堆打不开的连接.
func (s *sseStreamReader) Close() error {
	return s.body.Close()
}
