package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRefresher_ParsesOpenRouterPricing verifies per-token rates are parsed
// from the catalog's string pricing, and that a model with no pricing block
// leaves Pricing nil (cost unknown, not free).
func TestRefresher_ParsesOpenRouterPricing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"openai/gpt-4o","name":"GPT-4o","context_length":128000,"pricing":{"prompt":"0.000003","completion":"0.000015","input_cache_read":"0.0000003","input_cache_write":"0.00000375","image":"0.001","request":"0"}},
			{"id":"local/no-price","name":"No Price","context_length":8000}
		]}`))
	}))
	defer srv.Close()

	db := newRefresherTestDB(t)
	cache := NewModelCache(db)
	refresher := newModelRefresherWithClient(cache, http.DefaultClient)
	provider := ProviderInfo{Type: "openrouter", BaseURL: srv.URL, ModelsEndpoint: "/models"}

	models, err := refresher.Refresh(context.Background(), provider, "", "")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	byID := indexByID(models)
	p := byID["openai/gpt-4o"].Pricing
	if p == nil {
		t.Fatal("gpt-4o should carry pricing")
	}
	if p.PromptUSD != 0.000003 || p.CompletionUSD != 0.000015 || p.CacheReadUSD != 0.0000003 || p.CacheWriteUSD != 0.00000375 || p.ImageUSD != 0.001 {
		t.Errorf("unexpected rates: %+v", p)
	}
	if byID["local/no-price"].Pricing != nil {
		t.Error("a model with no pricing block should leave Pricing nil")
	}
}

func TestPricing_CostUSD(t *testing.T) {
	p := &Pricing{PromptUSD: 0.000003, CompletionUSD: 0.000015, CacheReadUSD: 0.0000003, CacheWriteUSD: 0.00000375, ImageUSD: 0.001, RequestUSD: 0.0001}
	usage := Usage{PromptTokens: 1000, CompletionTokens: 200, CacheReadTokens: 400, CacheWriteTokens: 100, ReasoningTokens: 50}
	got := p.CostUSD(usage, 2)
	want := 1000*0.000003 + 200*0.000015 + 400*0.0000003 + 100*0.00000375 + 2*0.001 + 0.0001
	if got-want > 1e-12 || want-got > 1e-12 {
		t.Errorf("CostUSD = %v, want %v", got, want)
	}
	// nil pricing is zero-cost (caller treats unknown separately).
	var np *Pricing
	if np.CostUSD(Usage{PromptTokens: 100, CompletionTokens: 100}, 1) != 0 {
		t.Error("nil pricing should cost 0")
	}
}

func TestPricing_HasCompleteCacheRates(t *testing.T) {
	if (&Pricing{PromptUSD: 1, CacheReadUSD: 0.1}).HasCompleteCacheRates() {
		t.Fatal("cache placement must remain disabled when the cache-write rate is missing")
	}
	if !(&Pricing{PromptUSD: 1, CacheReadUSD: 0.1, CacheWriteUSD: 1.25}).HasCompleteCacheRates() {
		t.Fatal("complete base/read/write rates should enable cache placement")
	}
}
