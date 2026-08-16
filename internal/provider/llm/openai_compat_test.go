package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOpenAICompatStreamUsage 验证流式请求会申请 usage，并能解析末尾独立 usage 块。
func TestOpenAICompatStreamUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !req.Stream || req.StreamOptions == nil || !req.StreamOptions.IncludeUsage {
			t.Errorf("stream usage option missing: %+v", req)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4,\"total_tokens\":14,\"prompt_cache_hit_tokens\":6,\"prompt_cache_miss_tokens\":4,\"completion_tokens_details\":{\"reasoning_tokens\":2}}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	provider := NewOpenAICompat(Config{BaseURL: server.URL, APIKey: "test", Model: "test-model"})
	reader, err := provider.Stream(context.Background(), ChatRequest{Model: "test-model"})
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer reader.Close()

	chunk, done, err := reader.Next()
	if err != nil || done || chunk != "你" {
		t.Fatalf("first chunk = %q, done=%v, err=%v", chunk, done, err)
	}
	chunk, done, err = reader.Next()
	if err != nil || !done || chunk != "" {
		t.Fatalf("stream end chunk = %q, done=%v, err=%v", chunk, done, err)
	}

	usage := reader.Usage()
	if usage.PromptTokens != 10 || usage.CompletionTokens != 4 || usage.TotalTokens != 14 ||
		usage.CacheHitTokens() != 6 || usage.CacheMissTokens() != 4 || usage.CompletionTokensDetails.ReasoningTokens != 2 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}
