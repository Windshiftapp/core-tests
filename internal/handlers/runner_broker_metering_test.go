package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/llm"
)

// chatCompletionsUpstream serves one OpenAI-compatible chat completion whose
// usage block is supplied by the caller.
func chatCompletionsUpstream(t *testing.T, usage string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("upstream path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cmpl-1","object":"chat.completion","model":"vendor/model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":` + usage + `}`))
	}))
}

// costRow reads the single metered call recorded for the run.
func costRow(t *testing.T, fixture *llmBrokerFixture) (costUSD sql.NullFloat64, costSource string) {
	t.Helper()
	if err := fixture.db.QueryRow(
		`SELECT cost_usd, cost_source FROM llm_usage WHERE run_id = ?`, fixture.runID,
	).Scan(&costUSD, &costSource); err != nil {
		t.Fatalf("read llm_usage row: %v", err)
	}
	return costUSD, costSource
}

// TestNeutralInferenceEndpointRecordsProviderReportedCost covers the metering
// half of WI-920. Replacing the byte proxy with the neutral operation dropped
// the SSE usage tail, and with it the inline cost a reselling gateway reports.
// That number is authoritative — it already reflects the gateway's discounts
// and routing — so it must survive the component boundary.
func TestNeutralInferenceEndpointRecordsProviderReportedCost(t *testing.T) {
	server := chatCompletionsUpstream(t, `{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,"cost":0.004125}`)
	defer server.Close()
	fixture := newLLMBrokerFixtureFor(t, llm.CreateConnectionRequest{
		Name: "OpenRouter broker", ProviderType: "openrouter", Model: "vendor/model",
		APIKey: "provider-secret", BaseURL: server.URL, IsEnabled: true,
	})

	if got := fixture.request(t, fixture.token, `{"messages":[{"role":"user","content":"hi"}]}`); got.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", got.Code, got.Body.String())
	}
	costUSD, costSource := costRow(t, fixture)
	if costSource != "provider" {
		t.Fatalf("cost_source = %q, want %q", costSource, "provider")
	}
	if !costUSD.Valid || costUSD.Float64 != 0.004125 {
		t.Fatalf("cost_usd = %+v, want the provider-reported 0.004125", costUSD)
	}
}

// TestNeutralInferenceEndpointMarksPartiallyPricedUsageUnpriced covers the
// other metering half. Refusing to price a call whose cache class has no rate
// is deliberate — billing a cache write at the base input rate under-reports —
// but an empty cost_source made that indistinguishable from a model with no
// configured rates at all, so the gap was invisible.
func TestNeutralInferenceEndpointMarksPartiallyPricedUsageUnpriced(t *testing.T) {
	server := chatCompletionsUpstream(t, `{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,
		"prompt_tokens_details":{"cached_tokens":80}}`)
	defer server.Close()
	fixture := newLLMBrokerFixtureFor(t, llm.CreateConnectionRequest{
		Name: "OpenRouter broker", ProviderType: "openrouter", Model: "vendor/partial-rates",
		APIKey: "provider-secret", BaseURL: server.URL, IsEnabled: true,
	})
	// Base rates only: the catalog advertises no cache-read rate, while the
	// call reports cached input.
	cache := llm.NewModelCache(fixture.db.GetDatabase())
	if err := cache.SaveSuccess("openrouter", []llm.ModelInfo{{
		ID:      "vendor/partial-rates",
		Pricing: &llm.Pricing{PromptUSD: 0.000001, CompletionUSD: 0.000002},
	}}, time.Now()); err != nil {
		t.Fatalf("seed model cache: %v", err)
	}
	fixture.manager.SetModelCache(cache)

	if got := fixture.request(t, fixture.token, `{"messages":[{"role":"user","content":"hi"}]}`); got.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", got.Code, got.Body.String())
	}
	costUSD, costSource := costRow(t, fixture)
	if costSource != "unpriced" {
		t.Fatalf("cost_source = %q, want %q", costSource, "unpriced")
	}
	if costUSD.Valid {
		t.Fatalf("cost_usd = %v, want NULL: an unrated cache class must not be billed at the base rate", costUSD.Float64)
	}
	// The tokens themselves still have to be metered.
	totals, err := fixture.usage.TotalsForRun(context.Background(), fixture.runID)
	if err != nil {
		t.Fatalf("usage totals: %v", err)
	}
	if totals.PromptTokens != 40 || totals.CacheReadTokens != 80 || totals.CompletionTokens != 30 {
		t.Fatalf("typed usage totals = %+v", totals)
	}
}
