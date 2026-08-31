//go:build test

package services

import (
	"testing"
	"time"

	"windshift/internal/cacheutil"
)

func TestItemCacheUsesEntireAllocationWithoutWorkspaceProjectCache(t *testing.T) {
	service, err := NewItemCacheService(nil, ItemCacheConfig{
		HierarchyTTL:    time.Hour,
		MaxCacheSize:    12,
		WarmupBatchSize: 1,
		EnablePreWarm:   false,
	})
	if err != nil {
		t.Fatalf("NewItemCacheService() error = %v", err)
	}
	t.Cleanup(func() { _ = service.hierarchyCache.Close() })

	wantMaximum := int64(12 * 1024 * 1024)
	foundHierarchy := false
	for _, snapshot := range cacheutil.Snapshots() {
		switch snapshot.Name {
		case "item_hierarchy":
			foundHierarchy = true
			if snapshot.MaximumCapacityBytes != wantMaximum {
				t.Fatalf("item_hierarchy maximum = %d, want %d", snapshot.MaximumCapacityBytes, wantMaximum)
			}
		case "item_projects":
			t.Fatalf("unused item_projects cache is still registered: %+v", snapshot)
		}
	}
	if !foundHierarchy {
		t.Fatal("item_hierarchy cache missing from diagnostics")
	}

	stats := service.GetStats()
	for _, deadKey := range []string{"project_hits", "project_misses", "project_hit_rate", "project_cache_size"} {
		if _, ok := stats[deadKey]; ok {
			t.Fatalf("dead project cache statistic %q remains: %+v", deadKey, stats)
		}
	}
}
