package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/utils"
)

func TestDefaultAnthropicProviderCompletesThroughMessagesAPI(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	var request map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Fatalf("request = %s %s, want POST /v1/messages", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "anthropic-test-key" {
			t.Fatalf("x-api-key = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got == "" {
			t.Fatal("anthropic-version header is missing")
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_01\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-5\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":12,\"cache_creation_input_tokens\":3,\"cache_read_input_tokens\":4,\"output_tokens\":0}}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"The release is ready.\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
		_, _ = w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer server.Close()

	client := NewProviderClient(ConnectionConfig{
		ProviderType: ProviderType("anthropic"),
		Model:        "claude-sonnet-5",
		APIKey:       "anthropic-test-key",
		BaseURL:      server.URL,
		Timeout:      time.Second,
	})
	response, err := client.Complete(context.Background(), CompletionRequest{
		Messages:  []Message{{Role: "system", Content: "Answer briefly."}, {Role: "user", Content: "Is the release ready?"}},
		MaxTokens: 128,
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got := response.Choices[0].Message.Content; got != "The release is ready." {
		t.Fatalf("content = %q", got)
	}
	if response.Usage != (Usage{PromptTokens: 12, CompletionTokens: 5, TotalTokens: 24, CacheReadTokens: 4, CacheWriteTokens: 3}) {
		t.Fatalf("usage = %+v", response.Usage)
	}
	if got := string(request["model"]); got != `"claude-sonnet-5"` {
		t.Fatalf("model = %s", got)
	}
	if got := string(request["max_tokens"]); got != "128" {
		t.Fatalf("max_tokens = %s", got)
	}
	if _, ok := request["input"]; ok {
		t.Fatalf("Anthropic request used Responses input field: %s", request["input"])
	}
	if len(request["system"]) == 0 || len(request["messages"]) == 0 {
		t.Fatalf("system/messages were not mapped: %s", mustMarshalJSON(t, request))
	}
}
