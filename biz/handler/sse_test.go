package handler

import (
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
)

// TestPrepareSSE 验证 prepareSSE 是否设置了标准 SSE 响应头。
func TestPrepareSSE(t *testing.T) {
	// 创建一个不需要启动 HTTP 服务的 Hertz 请求上下文。
	c := app.NewContext(0)

	prepareSSE(c)

	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "Content-Type",
			got:  string(c.Response.Header.ContentType()),
			want: "text/event-stream",
		},
		{
			name: "Cache-Control",
			got:  string(c.Response.Header.Peek("Cache-Control")),
			want: "no-cache",
		},
		{
			name: "Connection",
			got:  string(c.Response.Header.Peek("Connection")),
			want: "keep-alive",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf(
					"%s 响应头错误：得到 %q，期望 %q",
					test.name,
					test.got,
					test.want,
				)
			}
		})
	}
}

// TestWriteSSE 验证 Go 数据能否被编码成标准 SSE 消息。
func TestWriteSSE(t *testing.T) {
	c := app.NewContext(0)

	err := writeSSE(
		c,
		"message",
		map[string]string{
			"content": "第一行\n第二行",
		},
	)
	if err != nil {
		t.Fatalf("写入 SSE 消息失败：%v", err)
	}

	got := string(c.Response.Body())
	want := "event: message\n" +
		"data: {\"content\":\"第一行\\n第二行\"}\n\n"

	if got != want {
		t.Fatalf(
			"SSE 消息格式错误：\n得到：\n%q\n期望：\n%q",
			got,
			want,
		)
	}

	// JSON 中的换行必须被编码为 \n，
	// 不能把一条 SSE 消息意外拆成多行。
	if strings.Contains(got, "第一行\n第二行") {
		t.Fatal("消息内容中的真实换行没有被 JSON 转义")
	}
}

// TestWriteSSEDifferentEvent 验证 writeSSE 不只支持 message 事件。
func TestWriteSSEDifferentEvent(t *testing.T) {
	c := app.NewContext(0)

	err := writeSSE(
		c,
		"usage",
		map[string]int64{
			"prompt_tokens":     10,
			"completion_tokens": 20,
			"total_tokens":      30,
		},
	)
	if err != nil {
		t.Fatalf("写入 usage 事件失败：%v", err)
	}

	got := string(c.Response.Body())

	if !strings.HasPrefix(got, "event: usage\n") {
		t.Fatalf("事件类型错误：%q", got)
	}

	if !strings.Contains(got, `"total_tokens":30`) {
		t.Fatalf("响应中没有 total_tokens：%q", got)
	}

	if !strings.HasSuffix(got, "\n\n") {
		t.Fatalf("SSE 事件没有使用两个换行结尾：%q", got)
	}
}
