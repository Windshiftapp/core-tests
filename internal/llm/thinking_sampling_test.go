//go:build test

package llm

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"windshift/internal/utils"
)

// WI-1798: Anthropic rejects temperature/top_p alongside thinking. Fantasy
// clears them only on its explicit-budget thinking path; the effort path, and
// an operator thinking override forwarded via ExtraBody, leave them set. The
// adapter must drop sampling parameters whenever thinking is enabled.

func completeAnthropicCapturingBody(t *testing.T, providerConfig string, req CompletionRequest) map[string]json.RawMessage {
	t.Helper()
	var body map[string]json.RawMessage
	server := reasoningAnthropicServer(t, &body)
	defer server.Close()

	client := NewProviderClient(ConnectionConfig{
		ProviderType:   ProviderType("anthropic"),
		BaseURL:        server.URL,
		Model:          "claude-sonnet-5",
		ProviderConfig: providerConfig,
		Timeout:        time.Second,
	})
	if _, err := client.Complete(context.Background(), req); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	return body
}

func anthropicRequestWithTemperature() CompletionRequest {
	return CompletionRequest{
		Messages:    []Message{{Role: "user", Content: "hello"}},
		Temperature: 0.2,
	}
}

func TestAnthropicConfiguredEffortDropsSamplingParameters(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	body := completeAnthropicCapturingBody(t, `{"reasoning_level":"high","top_p":0.9}`, anthropicRequestWithTemperature())
	if _, ok := body["temperature"]; ok {
		t.Fatalf("temperature leaked alongside effort thinking: %s", mustMarshalJSON(t, body))
	}
	if _, ok := body["top_p"]; ok {
		t.Fatalf("top_p leaked alongside effort thinking: %s", mustMarshalJSON(t, body))
	}
	var outputConfig map[string]json.RawMessage
	if err := json.Unmarshal(body["output_config"], &outputConfig); err != nil {
		t.Fatalf("output_config missing: %v (body=%s)", err, mustMarshalJSON(t, body))
	}
	if got := string(outputConfig["effort"]); got != `"high"` {
		t.Fatalf("output_config.effort = %s, want \"high\"", got)
	}
}

func TestAnthropicCodingAgentEffortDropsSamplingParameters(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	req := anthropicRequestWithTemperature()
	req.CodingAgent = true
	body := completeAnthropicCapturingBody(t, "", req)
	if _, ok := body["temperature"]; ok {
		t.Fatalf("temperature leaked alongside coding-agent effort: %s", mustMarshalJSON(t, body))
	}
}

func TestAnthropicThinkingOverrideDropsSamplingParameters(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	body := completeAnthropicCapturingBody(t, `{"thinking":{"type":"enabled","budget_tokens":2048}}`, anthropicRequestWithTemperature())
	if _, ok := body["temperature"]; ok {
		t.Fatalf("temperature leaked alongside operator thinking: %s", mustMarshalJSON(t, body))
	}
	var thinking map[string]json.RawMessage
	if err := json.Unmarshal(body["thinking"], &thinking); err != nil {
		t.Fatalf("thinking missing: %v (body=%s)", err, mustMarshalJSON(t, body))
	}
	if got := string(thinking["budget_tokens"]); got != "2048" {
		t.Fatalf("thinking.budget_tokens = %s, want 2048", got)
	}
}

func TestAnthropicThinkingDisabledPreservesTemperature(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	body := completeAnthropicCapturingBody(t, "", anthropicRequestWithTemperature())
	if got := string(body["temperature"]); got != "0.2" {
		t.Fatalf("temperature = %s, want 0.2 when thinking is disabled (body=%s)", got, mustMarshalJSON(t, body))
	}
}

func TestAnthropicDisabledThinkingOverridePreservesTemperature(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	body := completeAnthropicCapturingBody(t, `{"thinking":{"type":"disabled"}}`, anthropicRequestWithTemperature())
	if got := string(body["temperature"]); got != "0.2" {
		t.Fatalf("temperature = %s, want 0.2 when the thinking override is disabled (body=%s)", got, mustMarshalJSON(t, body))
	}
}
