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

func TestProviderClientUsesModernCompletionTokenParameter(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	tests := []struct {
		name     string
		provider ProviderType
		path     string
	}{
		{
			name:     "OpenAI through custom LiteLLM base URL",
			provider: ProviderType("openai"),
			path:     "/v1/chat/completions",
		},
		{
			name:     "OpenRouter accepts the modern parameter",
			provider: ProviderType("openrouter"),
			path:     "/chat/completions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body map[string]json.RawMessage
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.path {
					t.Fatalf("request path = %q, want %q", r.URL.Path, tt.path)
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(CompletionResponse{
					Choices: []Choice{{Message: Message{Role: "assistant", Content: "ok"}}},
				})
			}))
			defer server.Close()

			client := NewProviderClient(ConnectionConfig{
				ProviderType: tt.provider,
				Model:        "test-model",
				BaseURL:      server.URL,
				Timeout:      time.Second,
			})
			_, err := client.Complete(context.Background(), CompletionRequest{
				Messages:  []Message{{Role: "user", Content: "hello"}},
				MaxTokens: 321,
			})
			if err != nil {
				t.Fatalf("Complete() error = %v", err)
			}

			if got := string(body["max_completion_tokens"]); got != "321" {
				t.Fatalf("max_completion_tokens = %s, want 321; body = %s", got, mustMarshalJSON(t, body))
			}
			if _, exists := body["max_tokens"]; exists {
				t.Fatalf("request must not include deprecated max_tokens; body = %s", mustMarshalJSON(t, body))
			}
		})
	}
}

func TestProviderClientDoesNotRetryUnrelatedBadRequest(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"temperature is out of range"}}`))
	}))
	defer server.Close()

	client := NewProviderClient(ConnectionConfig{
		ProviderType: ProviderType("local"),
		Model:        "opaque-model-alias",
		BaseURL:      server.URL,
		Timeout:      time.Second,
	})
	_, err := client.Complete(context.Background(), CompletionRequest{
		Messages:  []Message{{Role: "user", Content: "hello"}},
		MaxTokens: 321,
	})
	if !errors.Is(err, ErrAPIError) {
		t.Fatalf("Complete() error = %v, want ErrAPIError", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want no retry for an unrelated bad request", requests)
	}
}

func TestProviderClientFallsBackAndRemembersLegacyCompletionTokenParameter(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	var bodies []map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		bodies = append(bodies, body)
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported parameter: 'max_completion_tokens'. Use 'max_tokens' instead."}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CompletionResponse{
			Choices: []Choice{{Message: Message{Role: "assistant", Content: "ok"}}},
		})
	}))
	defer server.Close()

	client := NewProviderClient(ConnectionConfig{
		ProviderType: ProviderType("local"),
		Model:        "opaque-model-alias",
		BaseURL:      server.URL,
		Timeout:      time.Second,
	})
	request := CompletionRequest{
		Messages:  []Message{{Role: "user", Content: "hello"}},
		MaxTokens: 321,
	}
	if _, err := client.Complete(context.Background(), request); err != nil {
		t.Fatalf("first Complete() error = %v", err)
	}
	if _, err := client.Complete(context.Background(), request); err != nil {
		t.Fatalf("second Complete() error = %v", err)
	}

	if len(bodies) != 3 {
		t.Fatalf("requests = %d, want initial request, fallback, and remembered legacy request", len(bodies))
	}
	if _, exists := bodies[0]["max_completion_tokens"]; !exists {
		t.Fatalf("initial request must use max_completion_tokens; body = %s", mustMarshalJSON(t, bodies[0]))
	}
	for i, body := range bodies[1:] {
		if got := string(body["max_tokens"]); got != "321" {
			t.Fatalf("legacy request %d max_tokens = %s, want 321; body = %s", i+1, got, mustMarshalJSON(t, body))
		}
		if _, exists := body["max_completion_tokens"]; exists {
			t.Fatalf("legacy request %d must not include max_completion_tokens; body = %s", i+1, mustMarshalJSON(t, body))
		}
	}
}

func mustMarshalJSON(t *testing.T, value interface{}) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return data
}
