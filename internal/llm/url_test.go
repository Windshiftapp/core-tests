package llm

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/utils"
)

func TestJoinProviderPathAvoidsDuplicateOpenAIVersion(t *testing.T) {
	tests := []struct {
		name string
		base string
		path string
		want string
	}{
		{
			name: "root plus openai chat path",
			base: "http://localhost:11434",
			path: "/v1/chat/completions",
			want: "http://localhost:11434/v1/chat/completions",
		},
		{
			name: "versioned base plus openai chat path",
			base: "http://localhost:11434/v1",
			path: "/v1/chat/completions",
			want: "http://localhost:11434/v1/chat/completions",
		},
		{
			name: "versioned base with trailing slash",
			base: "http://localhost:11434/v1/",
			path: "v1/models",
			want: "http://localhost:11434/v1/models",
		},
		{
			name: "non v1 provider path is preserved",
			base: "https://api.z.ai/api/paas/v4",
			path: "/chat/completions",
			want: "https://api.z.ai/api/paas/v4/chat/completions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinProviderPath(tt.base, tt.path); got != tt.want {
				t.Fatalf("joinProviderPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProviderModelsURLForVersionedCustomBase(t *testing.T) {
	provider := ProviderInfo{ModelsEndpoint: "/v1/models", BaseURL: "https://api.openai.com"}
	got := provider.ModelsURLForBase("http://localhost:1234/v1")
	want := "http://localhost:1234/v1/models"
	if got != want {
		t.Fatalf("ModelsURLForBase() = %q, want %q", got, want)
	}
}

func TestDefaultOpenRouterHasSeedModels(t *testing.T) {
	LoadDefaultProviders()
	provider := GetProvider(ProviderType("openrouter"))
	if provider == nil {
		t.Fatal("OpenRouter provider not registered")
	}
	if len(provider.Models) == 0 {
		t.Fatal("OpenRouter should include seed models before the admin refreshes the live catalog")
	}
}

func TestDefaultProvidersExposeDirectAnthropic(t *testing.T) {
	LoadDefaultProviders()
	provider := GetProvider(ProviderType("anthropic"))
	if provider == nil {
		t.Fatal("direct Anthropic provider is not registered")
	}
	if provider.APIFormat != APIContractAnthropic {
		t.Fatalf("APIFormat = %q, want %q", provider.APIFormat, APIContractAnthropic)
	}
	if got := provider.ModelsURL(); got != "https://api.anthropic.com/v1/models" {
		t.Fatalf("ModelsURL() = %q, want Anthropic models endpoint", got)
	}
	if provider.AuthScheme() != "anthropic" || provider.ResponseFormat() != "anthropic" {
		t.Fatalf("catalog contract = auth %q response %q, want anthropic/anthropic", provider.AuthScheme(), provider.ResponseFormat())
	}
	if len(provider.Models) != 4 {
		t.Fatalf("models = %d, want 4 current Anthropic models", len(provider.Models))
	}
	for _, model := range provider.Models {
		if model.MaxTokens <= 0 || model.ContextWindow <= 0 || !model.SupportsVision {
			t.Fatalf("model %q has incomplete limits/capabilities: %+v", model.ID, model)
		}
		if model.Pricing == nil || model.Pricing.PromptUSD <= 0 || model.Pricing.CompletionUSD <= 0 {
			t.Fatalf("model %q has incomplete pricing: %+v", model.ID, model.Pricing)
		}
	}
}

func TestAdminConfiguredHTTPClientBlocksLocalhostWhenExplicitlyDisallowed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	utils.SetAllowLocalConnections(false)
	defer utils.SetAllowLocalConnections(true)

	client := newAdminConfiguredHTTPClient(time.Second)
	resp, err := client.Get(server.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected LLM HTTP client to block loopback/private endpoints when explicitly disallowed")
	}
}

func TestAdminConfiguredHTTPClientAllowsLocalhostByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	utils.SetAllowLocalConnections(true)
	defer utils.SetAllowLocalConnections(true)

	client := newAdminConfiguredHTTPClient(time.Second)
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("expected LLM HTTP client to allow loopback by default: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
