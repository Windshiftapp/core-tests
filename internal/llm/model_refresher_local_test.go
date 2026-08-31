package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelRefresherFetchesLocalOpenAICompatibleModels(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[{"id":"llama3.1:8b","name":"Llama 3.1 8B","context_length":8192}]}`)
	}))
	defer server.Close()

	provider := ProviderInfo{
		Type:                 ProviderType("local"),
		ModelsEndpoint:       "/v1/models",
		ModelsAuthScheme:     "bearer",
		ModelsResponseFormat: "openai",
	}
	refresher := newModelRefresherWithClient(nil, server.Client())
	models, err := refresher.fetch(context.Background(), provider, provider.ModelsURLForBase(server.URL+"/v1"), "")
	if err != nil {
		t.Fatalf("fetch() error = %v", err)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("request path = %q, want /v1/models", gotPath)
	}
	if len(models) != 1 || models[0].ID != "llama3.1:8b" || models[0].ContextWindow != 8192 || models[0].MaxTokens != 0 {
		t.Fatalf("models = %#v", models)
	}
}
