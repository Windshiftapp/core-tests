//go:build test

package llm

import (
	"slices"
	"strings"
	"testing"
)

// WI-1963: the support floor is Claude 4.7+, which all use adaptive thinking.
// The adapter always emits adaptive thinking for Anthropic, so a pre-4.7 model
// in the default catalog would be sent a request it rejects. Pin the catalog so
// those ids cannot be reintroduced silently.

func TestDefaultCatalogOffersOnlyCurrentClaudeModels(t *testing.T) {
	LoadDefaultProviders()

	anthropic := GetProvider(ProviderType("anthropic"))
	if anthropic == nil {
		t.Fatal("anthropic provider missing from the default catalog")
	}
	got := make([]string, 0, len(anthropic.Models))
	for _, m := range anthropic.Models {
		got = append(got, m.ID)
	}
	slices.Sort(got)
	want := []string{"claude-fable-5", "claude-opus-5", "claude-sonnet-5"}
	if !slices.Equal(got, want) {
		t.Fatalf("anthropic catalog models = %v, want the current set %v", got, want)
	}

	// Anthropic models offered through a proxy must also be current.
	var proxied []string
	for _, p := range GetProviders() {
		for _, m := range p.Models {
			if strings.HasPrefix(m.ID, "anthropic/") {
				proxied = append(proxied, m.ID)
			}
		}
	}
	slices.Sort(proxied)
	wantProxied := []string{"anthropic/claude-opus-5", "anthropic/claude-sonnet-5"}
	if !slices.Equal(proxied, wantProxied) {
		t.Fatalf("proxied Anthropic catalog models = %v, want %v", proxied, wantProxied)
	}
}
