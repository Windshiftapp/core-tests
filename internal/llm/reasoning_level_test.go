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

// reasoningJSONServer captures one request body and answers with a plain
// completion, which is enough for the Chat Completions and Responses contracts.
func reasoningJSONServer(t *testing.T, body *map[string]json.RawMessage) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/responses" {
			_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","created_at":1,"status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(CompletionResponse{
			Choices: []Choice{{Message: Message{Role: "assistant", Content: "ok"}}},
		})
	}))
}

// reasoningAnthropicServer answers the Messages API with a minimal SSE stream.
func reasoningAnthropicServer(t *testing.T, body *map[string]json.RawMessage) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_01\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-5\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
		_, _ = w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
}

func completeWithReasoningConfig(t *testing.T, ptype ProviderType, model, providerConfig string) map[string]json.RawMessage {
	t.Helper()
	var body map[string]json.RawMessage
	var server *httptest.Server
	if ptype == ProviderType("anthropic") {
		server = reasoningAnthropicServer(t, &body)
	} else {
		server = reasoningJSONServer(t, &body)
	}
	defer server.Close()

	client := NewProviderClient(ConnectionConfig{
		ProviderType:   ptype,
		BaseURL:        server.URL,
		Model:          model,
		ProviderConfig: providerConfig,
		Timeout:        time.Second,
	})
	if _, err := client.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	return body
}

func TestChatCompletionsReasoningLevelSendsReasoningEffort(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	body := completeWithReasoningConfig(t, ProviderType("openrouter"), "openrouter-model", `{"reasoning_level":"high"}`)
	if got := string(body["reasoning_effort"]); got != `"high"` {
		t.Fatalf("reasoning_effort = %s, want \"high\" (body=%s)", got, mustMarshalJSON(t, body))
	}
	if _, ok := body["reasoning"]; ok {
		t.Fatalf("unexpected reasoning object: %s", mustMarshalJSON(t, body))
	}
}

func TestChatCompletionsReasoningLevelDefaultSendsNothing(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	body := completeWithReasoningConfig(t, ProviderType("openrouter"), "openrouter-model", `{"reasoning_level":"default"}`)
	if _, ok := body["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort was sent for default: %s", mustMarshalJSON(t, body))
	}
	if _, ok := body["reasoning"]; ok {
		t.Fatalf("reasoning was sent for default: %s", mustMarshalJSON(t, body))
	}
}

func TestChatCompletionsReasoningObjectOverrideWins(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	body := completeWithReasoningConfig(t, ProviderType("openrouter"), "openrouter-model", `{"reasoning_level":"high","reasoning":{"enabled":false}}`)
	var reasoning map[string]json.RawMessage
	if err := json.Unmarshal(body["reasoning"], &reasoning); err != nil {
		t.Fatalf("reasoning is not an object: %v (body=%s)", err, mustMarshalJSON(t, body))
	}
	if got := string(reasoning["enabled"]); got != "false" {
		t.Fatalf("reasoning.enabled = %s, want false", got)
	}
	if _, ok := body["reasoning_effort"]; ok {
		t.Fatalf("normalized reasoning_effort leaked alongside the override: %s", mustMarshalJSON(t, body))
	}
}

func TestChatCompletionsReasoningEffortOverrideWins(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	body := completeWithReasoningConfig(t, ProviderType("openrouter"), "openrouter-model", `{"reasoning_level":"high","reasoning_effort":"low"}`)
	if got := string(body["reasoning_effort"]); got != `"low"` {
		t.Fatalf("reasoning_effort = %s, want \"low\" (body=%s)", got, mustMarshalJSON(t, body))
	}
}

func TestResponsesReasoningLevelMapsToReasoningEffort(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	body := completeWithReasoningConfig(t, ProviderType("openai"), "gpt-5.1", `{"api_contract":"responses","reasoning_level":"high"}`)
	var reasoning map[string]json.RawMessage
	if err := json.Unmarshal(body["reasoning"], &reasoning); err != nil {
		t.Fatalf("reasoning is not an object: %v (body=%s)", err, mustMarshalJSON(t, body))
	}
	if got := string(reasoning["effort"]); got != `"high"` {
		t.Fatalf("reasoning.effort = %s, want \"high\"", got)
	}
}

func TestResponsesReasoningOverrideWins(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	body := completeWithReasoningConfig(t, ProviderType("openai"), "gpt-5.1", `{"api_contract":"responses","reasoning_level":"high","reasoning":{"effort":"low","summary":"detailed"}}`)
	var reasoning map[string]json.RawMessage
	if err := json.Unmarshal(body["reasoning"], &reasoning); err != nil {
		t.Fatalf("reasoning is not an object: %v (body=%s)", err, mustMarshalJSON(t, body))
	}
	if got := string(reasoning["effort"]); got != `"low"` {
		t.Fatalf("reasoning.effort = %s, want the operator override \"low\"", got)
	}
	if got := string(reasoning["summary"]); got != `"detailed"` {
		t.Fatalf("reasoning.summary = %s, want \"detailed\"", got)
	}
}

func TestAnthropicReasoningLevelMapsToEffort(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	body := completeWithReasoningConfig(t, ProviderType("anthropic"), "claude-sonnet-5", `{"reasoning_level":"high"}`)
	t.Logf("anthropic body: %s", mustMarshalJSON(t, body))
	var outputConfig map[string]json.RawMessage
	if err := json.Unmarshal(body["output_config"], &outputConfig); err != nil {
		t.Fatalf("output_config is not an object: %v (body=%s)", err, mustMarshalJSON(t, body))
	}
	if got := string(outputConfig["effort"]); got != `"high"` {
		t.Fatalf("output_config.effort = %s, want \"high\"", got)
	}
}

func TestAnthropicThinkingOverrideIsForwardedVerbatim(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	body := completeWithReasoningConfig(t, ProviderType("anthropic"), "claude-sonnet-5", `{"reasoning_level":"high","thinking":{"type":"enabled","budget_tokens":2048}}`)
	t.Logf("anthropic body: %s", mustMarshalJSON(t, body))
	var thinking map[string]json.RawMessage
	if err := json.Unmarshal(body["thinking"], &thinking); err != nil {
		t.Fatalf("thinking is not an object: %v (body=%s)", err, mustMarshalJSON(t, body))
	}
	if got := string(thinking["budget_tokens"]); got != "2048" {
		t.Fatalf("thinking.budget_tokens = %s, want 2048", got)
	}
	if _, ok := body["output_config"]; ok {
		t.Fatalf("normalized output_config leaked alongside the thinking override: %s", mustMarshalJSON(t, body))
	}
}
