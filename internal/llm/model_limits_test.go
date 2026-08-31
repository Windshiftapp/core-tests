package llm

import (
	"testing"
	"time"
)

func TestResolveModelLimitsAcrossDifferentWindows(t *testing.T) {
	LoadDefaultProviders()
	m := &ConnectionManager{}
	for _, tc := range []struct {
		provider                ProviderType
		model                   string
		wantContext, wantOutput int
	}{
		{provider: "openai", model: "gpt-4o", wantContext: 128000, wantOutput: 16384},
		{provider: "openrouter", model: "google/gemini-2.5-pro", wantContext: 1048576, wantOutput: 65536},
	} {
		contextWindow, maxOutput := m.resolveModelLimits(tc.provider, tc.model)
		if contextWindow != tc.wantContext || maxOutput != tc.wantOutput {
			t.Fatalf("resolveModelLimits(%q, %q) = (%d, %d), want (%d, %d)", tc.provider, tc.model, contextWindow, maxOutput, tc.wantContext, tc.wantOutput)
		}
	}
}

func TestResolveModelLimitsTreatsLegacyCachedMaxTokensAsContext(t *testing.T) {
	LoadDefaultProviders()
	db := newRefresherTestDB(t)
	cache := NewModelCache(db)
	if err := cache.SaveSuccess("openai", []ModelInfo{{ID: "gpt-4o", MaxTokens: 128000}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	m := &ConnectionManager{modelCache: cache}
	contextWindow, maxOutput := m.resolveModelLimits("openai", "gpt-4o")
	if contextWindow != 128000 || maxOutput != 16384 {
		t.Fatalf("legacy limits = (%d, %d)", contextWindow, maxOutput)
	}
}
