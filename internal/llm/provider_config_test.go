package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"windshift/internal/utils"
)

func TestMergeProviderConfigJSONAddsFieldsWithoutOverwritingRequest(t *testing.T) {
	got, err := MergeProviderConfigJSON(
		[]byte(`{"model":"connection-model","messages":[{"role":"user","content":"hi"}]}`),
		`{"model":"ignored","provider":{"order":["anthropic"],"allow_fallbacks":false},"temperature":0.2}`,
	)
	if err != nil {
		t.Fatalf("MergeProviderConfigJSON() error = %v", err)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("merged body is not JSON: %v", err)
	}
	if string(body["model"]) != `"connection-model"` {
		t.Fatalf("model = %s, want connection model to win", body["model"])
	}
	if string(body["provider"]) != `{"order":["anthropic"],"allow_fallbacks":false}` {
		t.Fatalf("provider = %s", body["provider"])
	}
	if string(body["temperature"]) != `0.2` {
		t.Fatalf("temperature = %s", body["temperature"])
	}
}

func TestValidateProviderConfigRejectsNonObject(t *testing.T) {
	if err := ValidateProviderConfig(`["anthropic"]`); err == nil {
		t.Fatal("expected array provider_config to be rejected")
	}
	if err := ValidateProviderConfig(`{"provider":{"sort":"latency"}}`); err != nil {
		t.Fatalf("expected object provider_config to pass: %v", err)
	}
}

func TestValidateProviderConfigAPIContract(t *testing.T) {
	for _, contract := range []string{"auto", "responses", "chat_completions"} {
		if err := ValidateProviderConfig(`{"api_contract":"` + contract + `"}`); err != nil {
			t.Errorf("ValidateProviderConfig(%q) error = %v", contract, err)
		}
	}
	if err := ValidateProviderConfig(`{"api_contract":"completions"}`); err == nil {
		t.Fatal("ValidateProviderConfig() accepted unsupported api_contract")
	}
	if err := ValidateProviderConfig(`{"api_contract":5}`); err == nil {
		t.Fatal("ValidateProviderConfig() accepted non-string api_contract")
	}
}

func TestProviderConfigAPIContract(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{raw: "", want: APIContractAuto},
		{raw: `{"api_contract":" responses "}`, want: APIContractResponses},
		{raw: `{"api_contract":"CHAT_COMPLETIONS"}`, want: APIContractChatCompletions},
		{raw: `{"api_contract":"invalid"}`, want: APIContractAuto},
	}
	for _, tt := range tests {
		if got := ProviderConfigAPIContract(tt.raw); got != tt.want {
			t.Errorf("ProviderConfigAPIContract(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestProviderConfigReasoningLevel(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "returns configured level", raw: `{"reasoning_level":"high"}`, want: ReasoningLevelHigh},
		{name: "trims and lowercases", raw: `{"reasoning_level":"  Medium "}`, want: ReasoningLevelMedium},
		{name: "default means unset", raw: `{"reasoning_level":"default"}`, want: ""},
		{name: "ignores missing level", raw: `{"api_contract":"responses"}`},
		{name: "ignores unknown level", raw: `{"reasoning_level":"extreme"}`},
		{name: "ignores non-string level", raw: `{"reasoning_level":3}`},
		{name: "ignores malformed config", raw: `{"reasoning_level":`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProviderConfigReasoningLevel(tt.raw); got != tt.want {
				t.Fatalf("ProviderConfigReasoningLevel(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestProviderConfigHasReasoningOverride(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "no override", raw: `{"reasoning_level":"high"}`, want: false},
		{name: "reasoning object", raw: `{"reasoning":{"effort":"high"}}`, want: true},
		{name: "reasoning_effort", raw: `{"reasoning_effort":"low"}`, want: true},
		{name: "thinking", raw: `{"thinking":{"type":"enabled","budget_tokens":1024}}`, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProviderConfigHasReasoningOverride(tt.raw); got != tt.want {
				t.Fatalf("ProviderConfigHasReasoningOverride(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestValidateProviderConfigReasoningLevel(t *testing.T) {
	for _, level := range []string{"default", "low", "medium", "high", " HIGH "} {
		if err := ValidateProviderConfig(`{"reasoning_level":"` + level + `"}`); err != nil {
			t.Errorf("ValidateProviderConfig() rejected level %q: %v", level, err)
		}
	}
	if err := ValidateProviderConfig(`{"reasoning_level":"extreme"}`); err == nil {
		t.Fatal("ValidateProviderConfig() accepted unsupported reasoning_level")
	}
	if err := ValidateProviderConfig(`{"reasoning_level":3}`); err == nil {
		t.Fatal("ValidateProviderConfig() accepted non-string reasoning_level")
	}
	// Provider-native reasoning fields stay opaque: any shape passes through.
	if err := ValidateProviderConfig(`{"reasoning":"high"}`); err != nil {
		t.Fatalf("ValidateProviderConfig() rejected raw reasoning: %v", err)
	}
	if err := ValidateProviderConfig(`{"thinking":{"budget_tokens":-1}}`); err != nil {
		t.Fatalf("ValidateProviderConfig() rejected raw thinking: %v", err)
	}
}

func TestOpenAIClientIncludesProviderConfig(t *testing.T) {
	var body map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CompletionResponse{
			Choices: []Choice{{Message: Message{Role: "assistant", Content: "ok"}}},
		})
	}))
	defer server.Close()

	utils.SetAllowLocalConnections(true)
	defer utils.SetAllowLocalConnections(true)

	client := NewProviderClient(ConnectionConfig{
		ProviderType:   ProviderType("openrouter"),
		BaseURL:        server.URL,
		Model:          "openrouter-model",
		ProviderConfig: `{"provider":{"only":["anthropic"],"allow_fallbacks":false},"model":"ignored"}`,
		Timeout:        time.Second,
	})
	if _, err := client.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if string(body["model"]) != `"openrouter-model"` {
		t.Fatalf("model = %s, want configured model to win", body["model"])
	}
	var provider map[string]json.RawMessage
	if err := json.Unmarshal(body["provider"], &provider); err != nil {
		t.Fatalf("provider is not JSON object: %v", err)
	}
	if string(provider["only"]) != `["anthropic"]` {
		t.Fatalf("provider.only = %s", provider["only"])
	}
	if string(provider["allow_fallbacks"]) != `false` {
		t.Fatalf("provider.allow_fallbacks = %s", provider["allow_fallbacks"])
	}
}

// MergeProviderConfig adds provider_config fields to an in-memory request
// body. Existing generated request fields win, so config cannot replace the
// prompt, model, tools, or other fields already set by the caller.
func MergeProviderConfig(body map[string]any, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return fmt.Errorf("provider_config must be valid JSON: %w", err)
	}
	if cfg == nil {
		return fmt.Errorf("provider_config must be a JSON object")
	}
	for k, v := range cfg {
		if reservedProviderConfigKeys[k] {
			continue // windshift-private key, never forwarded to the provider
		}
		if _, exists := body[k]; exists {
			continue
		}
		var decoded any
		if err := json.Unmarshal(v, &decoded); err != nil {
			return fmt.Errorf("provider_config.%s must be valid JSON: %w", k, err)
		}
		body[k] = decoded
	}
	return nil
}

// MergeProviderConfigJSON adds provider_config fields to a raw JSON request
// body. It is used by the coding-agent proxy path, where the runner owns the
// OpenAI-compatible request body and the broker only injects connection config.
func MergeProviderConfigJSON(body []byte, raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return body, nil
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("request body must be a JSON object: %w", err)
	}
	if request == nil {
		return nil, fmt.Errorf("request body must be a JSON object")
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("provider_config must be valid JSON: %w", err)
	}
	if cfg == nil {
		return nil, fmt.Errorf("provider_config must be a JSON object")
	}
	for k, v := range cfg {
		if reservedProviderConfigKeys[k] {
			continue // windshift-private key, never forwarded to the provider
		}
		if _, exists := request[k]; exists {
			continue
		}
		request[k] = v
	}
	merged, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal provider-configured request: %w", err)
	}
	return merged, nil
}
