//go:build test

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

// The admin connection test must exercise the parameters in-product chat sends
// — sampling temperature and a tool definition — so a provider that rejects a
// chat request fails the test instead of passing a bare one-token ping.
func TestConnectionProbeSendsChatParameters(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	var body map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewProviderClient(ConnectionConfig{
		ProviderType: ProviderType("local"),
		Model:        "opaque-litellm-model",
		BaseURL:      server.URL,
		Timeout:      time.Second,
	})
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}

	if got := string(body["temperature"]); got != "0.1" {
		t.Fatalf("probe temperature = %s, want 0.1 (body=%s)", got, mustMarshalJSON(t, body))
	}
	var tools []map[string]any
	if err := json.Unmarshal(body["tools"], &tools); err != nil {
		t.Fatalf("probe tools missing: %v (body=%s)", err, mustMarshalJSON(t, body))
	}
	if len(tools) != 1 {
		t.Fatalf("probe tools = %d, want 1 (body=%s)", len(tools), mustMarshalJSON(t, body))
	}
	function, _ := tools[0]["function"].(map[string]any)
	if function["name"] != "windshift_connection_probe" {
		t.Fatalf("probe tool name = %v (body=%s)", function["name"], mustMarshalJSON(t, body))
	}
}

// A connection with a configured reasoning level must have the probe exercise
// that option, including the adapter's rule that thinking drops temperature.
func TestConnectionProbeCarriesConfiguredReasoning(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	var body map[string]json.RawMessage
	server := reasoningAnthropicServer(t, &body)
	defer server.Close()

	client := NewProviderClient(ConnectionConfig{
		ProviderType:   ProviderType("anthropic"),
		BaseURL:        server.URL,
		Model:          "claude-sonnet-5",
		ProviderConfig: `{"reasoning_level":"high"}`,
		Timeout:        time.Second,
	})
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}

	var outputConfig map[string]json.RawMessage
	if err := json.Unmarshal(body["output_config"], &outputConfig); err != nil {
		t.Fatalf("output_config missing: %v (body=%s)", err, mustMarshalJSON(t, body))
	}
	if got := string(outputConfig["effort"]); got != `"high"` {
		t.Fatalf("output_config.effort = %s, want \"high\" (body=%s)", got, mustMarshalJSON(t, body))
	}
	if _, ok := body["temperature"]; ok {
		t.Fatalf("probe leaked temperature alongside configured thinking: %s", mustMarshalJSON(t, body))
	}
	if _, ok := body["tools"]; !ok {
		t.Fatalf("probe omitted tools: %s", mustMarshalJSON(t, body))
	}
}
