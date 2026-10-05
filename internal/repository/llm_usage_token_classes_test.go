package repository

import (
	"context"
	"testing"

	"windshift/internal/testutils"
)

func TestLLMUsageRepositoryPersistsAndAggregatesTokenClasses(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	repo := NewLLMUsageRepository(tdb.GetDatabase())
	cost := 0.0123

	if err := repo.Insert(context.Background(), LLMUsageRecord{
		RunID: 41, Model: "model-a", PromptTokens: 100, CompletionTokens: 25,
		TotalTokens: 175, CacheReadTokens: 40, CacheWriteTokens: 10,
		ReasoningTokens: 7, CostUSD: &cost, CostSource: "computed",
	}); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	if err := repo.Insert(context.Background(), LLMUsageRecord{
		RunID: 41, Model: "model-a", PromptTokens: 20, CompletionTokens: 5,
		TotalTokens: 35, CacheReadTokens: 8, CacheWriteTokens: 2,
		ReasoningTokens: 3,
	}); err != nil {
		t.Fatalf("second Insert() error = %v", err)
	}

	totals, err := repo.TotalsForRun(context.Background(), 41)
	if err != nil {
		t.Fatalf("TotalsForRun() error = %v", err)
	}
	if totals.PromptTokens != 120 || totals.CompletionTokens != 30 || totals.TotalTokens != 210 ||
		totals.CacheReadTokens != 48 || totals.CacheWriteTokens != 12 || totals.ReasoningTokens != 10 || totals.Calls != 2 {
		t.Fatalf("totals = %+v", totals)
	}
	if totals.Model != "model-a" {
		t.Fatalf("model = %q, want model-a", totals.Model)
	}
	if totals.CostUSD == nil || *totals.CostUSD != cost {
		t.Fatalf("cost = %v, want %v", totals.CostUSD, cost)
	}
}

func TestLLMUsageRepositorySumsProviderCallsPerRow(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	repo := NewLLMUsageRepository(tdb.GetDatabase())

	// A chat turn stores one aggregated row covering several provider
	// round-trips; a broker row covers exactly one. TotalsForRun must add the
	// per-row counts, not count rows.
	if err := repo.Insert(context.Background(), LLMUsageRecord{
		RunID: 77, Calls: 6, Model: "model-a", TotalTokens: 900,
	}); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	if err := repo.Insert(context.Background(), LLMUsageRecord{
		RunID: 77, Model: "model-a", TotalTokens: 100,
	}); err != nil {
		t.Fatalf("second Insert() error = %v", err)
	}

	totals, err := repo.TotalsForRun(context.Background(), 77)
	if err != nil {
		t.Fatalf("TotalsForRun() error = %v", err)
	}
	if totals.Calls != 7 {
		t.Fatalf("calls = %d, want 7", totals.Calls)
	}
	if totals.TotalTokens != 1000 {
		t.Fatalf("total tokens = %d, want 1000", totals.TotalTokens)
	}
}
