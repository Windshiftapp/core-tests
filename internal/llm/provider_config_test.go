package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestProviderConfigResponsesReasoning(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want *ResponsesReasoningConfig
	}{
		{
			name: "normalizes configured effort and summary",
			raw:  `{"reasoning":{"effort":" high ","summary":" detailed ","budget_tokens":2048}}`,
			want: &ResponsesReasoningConfig{Effort: "high", Summary: "detailed", BudgetTokens: 2048},
		},
		{name: "ignores missing reasoning", raw: `{"api_contract":"responses"}`},
		{name: "ignores non-object reasoning", raw: `{"reasoning":"high"}`},
		{name: "ignores malformed config", raw: `{"reasoning":`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProviderConfigResponsesReasoning(tt.raw); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ProviderConfigResponsesReasoning(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestValidateProviderConfigReasoning(t *testing.T) {
	if err := ValidateProviderConfig(`{"reasoning":{"effort":"high","summary":"detailed"}}`); err != nil {
		t.Fatalf("ValidateProviderConfig() rejected valid reasoning: %v", err)
	}
	if err := ValidateProviderConfig(`{"reasoning":"high"}`); err == nil {
		t.Fatal("ValidateProviderConfig() accepted non-object reasoning")
	}
	if err := ValidateProviderConfig(`{"reasoning":{"budget_tokens":-1}}`); err == nil {
		t.Fatal("ValidateProviderConfig() accepted negative reasoning budget")
	}
	if err := ValidateProviderConfig(`{"reasoning":{"budget_tokens":"large"}}`); err == nil {
		t.Fatal("ValidateProviderConfig() accepted non-numeric reasoning budget")
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
