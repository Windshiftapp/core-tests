package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/cacheutil"
	"windshift/internal/config"
)

func TestDiagnosticsCacheMemoryReportsBudgetAndRegisteredCaches(t *testing.T) {
	cache, err := cacheutil.New("diagnostics_test", cacheutil.BigCacheOptions{
		TTL: time.Minute, MaxCacheMB: 2, Shards: 1, MaxEntrySize: 1024, InitialCapacityMB: 1,
	})
	if err != nil {
		t.Fatalf("create cache: %v", err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	if err := cache.Set("key", []byte("value")); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	if _, err := cache.Get("key"); err != nil {
		t.Fatalf("read cache: %v", err)
	}

	budget, err := config.ResolveMemoryBudget(512)
	if err != nil {
		t.Fatalf("resolve budget: %v", err)
	}
	h := &DiagnosticsHandler{memoryBudget: budget}
	recorder := httptest.NewRecorder()
	h.GetCacheMemory(recorder, httptest.NewRequest("GET", "/api/admin/diagnostics/cache-memory", nil))
	if recorder.Code != 200 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		Budget              config.MemoryBudget `json:"budget"`
		AllocatedCacheBytes int64               `json:"allocated_cache_bytes"`
		MaximumCacheBytes   int64               `json:"maximum_cache_bytes"`
		ProcessRSSBytes     uint64              `json:"process_rss_bytes"`
		NextGCBytes         uint64              `json:"next_gc_bytes"`
		GCCount             uint32              `json:"gc_count"`
		GCCPUFraction       float64             `json:"gc_cpu_fraction"`
		GCPauseTotalNS      uint64              `json:"gc_pause_total_ns"`
		GCPauseMaxNS        uint64              `json:"gc_pause_max_ns"`
		CgroupMemory        struct {
			Available bool `json:"available"`
		} `json:"cgroup_memory"`
		Caches []cacheutil.Snapshot `json:"caches"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Budget.ProcessLimitMB != 512 || response.AllocatedCacheBytes <= 0 || response.MaximumCacheBytes <= 0 {
		t.Fatalf("incomplete diagnostics response: %+v", response)
	}
	if response.NextGCBytes == 0 || response.GCPauseMaxNS > response.GCPauseTotalNS {
		t.Fatalf("invalid GC diagnostics: %+v", response)
	}
	found := false
	for _, snapshot := range response.Caches {
		if snapshot.Name == "diagnostics_test" {
			found = snapshot.Entries == 1 && snapshot.Hits == 1
		}
	}
	if !found {
		t.Fatalf("diagnostics_test cache missing or incorrect: %+v", response.Caches)
	}
}
