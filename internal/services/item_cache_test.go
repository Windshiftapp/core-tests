//go:build test

package services

import (
	"testing"
	"time"

	"windshift/internal/cacheutil"
)

func TestItemCacheRegistersTypedEffectiveProjectCache(t *testing.T) {
	service := NewItemCacheService(nil, ItemCacheConfig{
		HierarchyTTL:    time.Hour,
		MaxCacheSize:    12,
		WarmupBatchSize: 1,
		EnablePreWarm:   false,
	})
	t.Cleanup(func() { _ = service.Close() })

	found := false
	for _, snapshot := range cacheutil.Snapshots() {
		switch snapshot.Name {
		case "item_effective_project":
			found = true
		case "item_hierarchy", "item_projects":
			t.Fatalf("stale cache registered in diagnostics: %+v", snapshot)
		}
	}
	if !found {
		t.Fatal("item_effective_project cache missing from diagnostics")
	}

	stats := service.GetStats()
	for _, deadKey := range []string{"project_hits", "project_misses", "project_hit_rate", "project_cache_size"} {
		if _, ok := stats[deadKey]; ok {
			t.Fatalf("dead project cache statistic %q remains: %+v", deadKey, stats)
		}
	}
}

func TestItemCacheMaxEntries(t *testing.T) {
	if got := itemCacheMaxEntries(ItemCacheConfig{MaxEntries: 42, MaxCacheSize: 16}); got != 42 {
		t.Fatalf("explicit MaxEntries = %d, want 42", got)
	}
	wantDerived := 16 * 1024 * 1024 / itemEffectiveProjectEstimateBytes
	if got := itemCacheMaxEntries(ItemCacheConfig{MaxCacheSize: 16}); got != wantDerived {
		t.Fatalf("derived MaxEntries = %d, want %d", got, wantDerived)
	}
	if got := itemCacheMaxEntries(ItemCacheConfig{}); got != 0 {
		t.Fatalf("unconfigured MaxEntries = %d, want 0 (unbounded)", got)
	}
}
