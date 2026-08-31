package cacheutil

import (
	"bytes"
	"testing"
	"time"
)

func TestCacheStartsBelowMaximumAndEvictsAtLimit(t *testing.T) {
	cache, err := New("cacheutil_regression", BigCacheOptions{
		TTL:               time.Hour,
		MaxCacheMB:        1,
		Shards:            1,
		MaxEntrySize:      32 * 1024,
		InitialCapacityMB: 1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = cache.Close() })

	if got := cache.Capacity(); got > 1024*1024 {
		t.Fatalf("initial capacity = %d, exceeds 1 MiB maximum", got)
	}
	payload := bytes.Repeat([]byte("x"), 24*1024)
	for i := 0; i < 100; i++ {
		if err := cache.Set(string(rune(i+1)), payload); err != nil {
			t.Fatalf("Set(%d) error = %v", i, err)
		}
	}

	var snapshot *Snapshot
	snapshots := Snapshots()
	for i := range snapshots {
		candidate := snapshots[i]
		if candidate.Name == "cacheutil_regression" {
			snapshot = &candidate
			break
		}
	}
	if snapshot == nil {
		t.Fatal("registered cache snapshot not found")
	}
	if snapshot.NoSpaceEvictions == 0 {
		t.Fatalf("NoSpaceEvictions = 0 after exceeding cache limit: %+v", snapshot)
	}
	if snapshot.AllocatedCapacityBytes > snapshot.MaximumCapacityBytes {
		t.Fatalf("allocated capacity exceeds maximum: %+v", snapshot)
	}
}

func TestLargeMaximumDoesNotPreallocateMaximum(t *testing.T) {
	cache, err := New("cacheutil_initial_capacity", BigCacheOptions{
		TTL:               time.Hour,
		MaxCacheMB:        128,
		Shards:            16,
		MaxEntrySize:      64 * 1024,
		InitialCapacityMB: 4,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = cache.Close() })

	if got := cache.Capacity(); got >= 32*1024*1024 {
		t.Fatalf("initial capacity = %d, want less than 32 MiB for a 128 MiB maximum", got)
	}
}
