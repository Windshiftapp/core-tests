package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/utils"
)

func TestOpenAICompatibleHealthAcceptsCompletionLimitResponse(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	var body map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Could not finish the message because max_tokens or model output limit was reached. Please try again with higher max_tokens."}}`))
	}))
	defer server.Close()

	client := NewProviderClient(ConnectionConfig{
		ProviderType: ProviderType("local"),
		Model:        "opaque-litellm-model",
		BaseURL:      server.URL,
		Timeout:      time.Second,
	})
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v, want token-limit response to prove connection health", err)
	}
	if got := string(body["max_completion_tokens"]); got != "1" {
		t.Fatalf("health probe max_completion_tokens = %s, want 1", got)
	}
}

func TestOpenAICompatibleHealthStillRejectsOtherAPIErrors(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid API key"}}`))
	}))
	defer server.Close()

	client := NewProviderClient(ConnectionConfig{
		ProviderType: ProviderType("local"),
		Model:        "opaque-litellm-model",
		BaseURL:      server.URL,
		Timeout:      time.Second,
	})
	err := client.Health(context.Background())
	if !errors.Is(err, ErrConnectionFailed) || !errors.Is(err, ErrAPIError) {
		t.Fatalf("Health() error = %v, want wrapped ErrConnectionFailed and ErrAPIError", err)
	}
}

func TestOpenAIResponsesHealthUsesOutputLimitAndAcceptsExplicitExhaustion(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	var body map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("request path = %q, want /v1/responses", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"max_output_tokens was reached"}}`))
	}))
	defer server.Close()

	client := NewProviderClient(ConnectionConfig{
		ProviderType:   ProviderType("openai"),
		Model:          "gpt-test",
		BaseURL:        server.URL,
		ProviderConfig: `{"api_contract":"responses"}`,
		Timeout:        time.Second,
	})
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v, want explicit output-limit response to prove connection health", err)
	}
	if got := string(body["max_output_tokens"]); got != "1" {
		t.Fatalf("health probe max_output_tokens = %s, want 1", got)
	}
	if _, exists := body["max_completion_tokens"]; exists {
		t.Fatalf("Responses health probe included max_completion_tokens; body = %s", mustMarshalJSON(t, body))
	}
}
