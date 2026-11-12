package cacheutil

import (
	"testing"
	"time"
)

type typedCachePayload struct {
	values map[string]bool
}

func TestTypedCacheGetSetDeleteReset(t *testing.T) {
	cache := NewTyped[*typedCachePayload]("typed_basic", TypedCacheOptions{TTL: time.Hour, Shards: 4})
	t.Cleanup(func() { _ = cache.Close() })

	if _, ok := cache.Get(1); ok {
		t.Fatal("Get on empty cache returned a value")
	}
	value := &typedCachePayload{values: map[string]bool{"a": true}}
	cache.Set(1, value)

	got, ok := cache.Get(1)
	if !ok || got != value {
		t.Fatalf("Get = (%p, %t), want the stored pointer %p", got, ok, value)
	}
	if cache.Len() != 1 {
		t.Fatalf("Len = %d, want 1", cache.Len())
	}

	if !cache.Delete(1) {
		t.Fatal("Delete reported missing an existing key")
	}
	if cache.Delete(1) {
		t.Fatal("Delete reported an already-deleted key")
	}
	if cache.Len() != 0 {
		t.Fatalf("Len after delete = %d, want 0", cache.Len())
	}

	cache.Set(2, value)
	cache.Set(3, value)
	cache.Reset()
	if cache.Len() != 0 {
		t.Fatalf("Len after reset = %d, want 0", cache.Len())
	}
	if _, ok := cache.Get(2); ok {
		t.Fatal("Get returned a value after reset")
	}
}

func TestTypedCacheExpiredEntryIsAMiss(t *testing.T) {
	// A negative TTL stores an already-expired entry, so the read path can be
	// exercised without waiting on a clock.
	cache := NewTyped[*typedCachePayload]("typed_expiry", TypedCacheOptions{TTL: -time.Second, Shards: 1})
	t.Cleanup(func() { _ = cache.Close() })

	cache.Set(1, &typedCachePayload{})
	if _, ok := cache.Get(1); ok {
		t.Fatal("Get returned an expired entry")
	}
	if cache.Len() != 0 {
		t.Fatalf("Len = %d, want expired entry reclaimed on read", cache.Len())
	}
}

func TestTypedCacheSweepReclaimsExpiredEntries(t *testing.T) {
	cache := NewTyped[*typedCachePayload]("typed_sweep", TypedCacheOptions{
		TTL:           time.Minute,
		Shards:        2,
		SweepInterval: time.Hour,
	})
	t.Cleanup(func() { _ = cache.Close() })

	cache.Set(1, &typedCachePayload{})
	cache.Set(2, &typedCachePayload{})
	cache.sweep(time.Now().Add(2 * time.Minute))

	if cache.Len() != 0 {
		t.Fatalf("Len after sweep = %d, want 0", cache.Len())
	}
}

func TestTypedCacheBoundsEntriesAndEvictsSoonestExpiring(t *testing.T) {
	cache := NewTyped[*typedCachePayload]("typed_bounded", TypedCacheOptions{
		TTL:        time.Hour,
		MaxEntries: 2,
		Shards:     1,
	})
	t.Cleanup(func() { _ = cache.Close() })

	first := &typedCachePayload{}
	cache.Set(1, first)
	cache.Set(2, &typedCachePayload{})
	cache.Set(3, &typedCachePayload{})

	if cache.Len() != 2 {
		t.Fatalf("Len = %d, want the 2-entry bound", cache.Len())
	}
	if _, ok := cache.Get(1); ok {
		t.Fatal("soonest-expiring entry survived the count bound")
	}
	if _, ok := cache.Get(3); !ok {
		t.Fatal("newest entry was evicted instead of the soonest-expiring entry")
	}

	snapshot := cache.cacheSnapshot("typed_bounded")
	if snapshot.NoSpaceEvictions != 1 {
		t.Fatalf("NoSpaceEvictions = %d, want 1", snapshot.NoSpaceEvictions)
	}
}

func TestTypedCacheSnapshotReportsTrafficAndEntries(t *testing.T) {
	cache := NewTyped[*typedCachePayload]("typed_snapshot", TypedCacheOptions{TTL: time.Hour, Shards: 2})
	t.Cleanup(func() { _ = cache.Close() })

	cache.Set(7, &typedCachePayload{})
	if _, ok := cache.Get(7); !ok {
		t.Fatal("Get missed a stored entry")
	}
	if _, ok := cache.Get(8); ok {
		t.Fatal("Get hit a missing entry")
	}

	snapshot := cache.cacheSnapshot("typed_snapshot")
	if snapshot.Name != "typed_snapshot" || snapshot.Entries != 1 || snapshot.Hits != 1 || snapshot.Misses != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if snapshot.AllocatedCapacityBytes != 0 || snapshot.MaximumCapacityBytes != 0 {
		t.Fatalf("typed cache should not report byte capacity: %+v", snapshot)
	}

	// The cache must be discoverable through the shared diagnostics registry.
	found := false
	for _, registered := range Snapshots() {
		if registered.Name == "typed_snapshot" {
			found = registered.Entries == 1 && registered.Hits == 1 && registered.Misses == 1
		}
	}
	if !found {
		t.Fatal("typed cache was not registered for process diagnostics")
	}
}
