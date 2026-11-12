//go:build test

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

type blockingItemCountDB struct {
	database.Database
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (db *blockingItemCountDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	db.once.Do(func() { close(db.started) })
	<-db.release
	return db.Database.QueryRowContext(ctx, query, args...)
}

type observedDoneContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (ctx *observedDoneContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
}

type itemCountResult struct {
	count int
	err   error
}

func TestCachedItemListCountCanceledLeaderDoesNotFailFollower(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })

	db := &blockingItemCountDB{
		Database: tdb.GetDatabase(),
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-db.release:
		default:
			close(db.release)
		}
	})

	deadline, cancelDeadline := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancelDeadline()

	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	leaderResult := make(chan itemCountResult, 1)
	go func() {
		count, err := cachedItemListCount(leaderCtx, db, 101, `SELECT 7`)
		leaderResult <- itemCountResult{count: count, err: err}
	}()
	waitItemCountSignal(t, deadline, db.started, "leader query start")

	cancelLeader()
	leader := waitItemCountResult(t, deadline, leaderResult, "leader cancellation")
	if !errors.Is(leader.err, context.Canceled) {
		t.Fatalf("leader error = %v, want context.Canceled", leader.err)
	}

	followerCtx := &observedDoneContext{Context: t.Context(), observed: make(chan struct{})}
	followerResult := make(chan itemCountResult, 1)
	go func() {
		count, err := cachedItemListCount(followerCtx, db, 101, `SELECT 7`)
		followerResult <- itemCountResult{count: count, err: err}
	}()
	waitItemCountSignal(t, deadline, followerCtx.observed, "follower singleflight join")
	close(db.release)

	follower := waitItemCountResult(t, deadline, followerResult, "follower result")
	if follower.err != nil || follower.count != 7 {
		t.Fatalf("follower result = %d, %v; want 7, nil", follower.count, follower.err)
	}
}

func TestInvalidateItemListCountCacheScopesWorkspace(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	db := tdb.GetDatabase()

	if count := loadItemCount(t, db, 201, `SELECT 1`); count != 1 {
		t.Fatalf("workspace 201 initial count = %d, want 1", count)
	}
	if count := loadItemCount(t, db, 202, `SELECT 2`); count != 2 {
		t.Fatalf("workspace 202 initial count = %d, want 2", count)
	}

	InvalidateItemListCountCache(db, 201)

	if count := loadItemCount(t, db, 201, `SELECT 3`); count != 3 {
		t.Fatalf("invalidated workspace count = %d, want 3", count)
	}
	if count := loadItemCount(t, db, 202, `SELECT 4`); count != 2 {
		t.Fatalf("unrelated workspace count = %d, want cached 2", count)
	}
}

func TestInvalidateItemListCountCacheRejectsStaleInflightStore(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	db := &blockingItemCountDB{
		Database: tdb.GetDatabase(),
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-db.release:
		default:
			close(db.release)
		}
	})

	deadline, cancelDeadline := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancelDeadline()
	result := make(chan itemCountResult, 1)
	go func() {
		count, err := cachedItemListCount(t.Context(), db, 301, `SELECT 1`)
		result <- itemCountResult{count: count, err: err}
	}()
	waitItemCountSignal(t, deadline, db.started, "stale count query start")

	InvalidateItemListCountCache(db, 301)
	close(db.release)
	first := waitItemCountResult(t, deadline, result, "stale count query result")
	if first.err != nil || first.count != 1 {
		t.Fatalf("in-flight result = %d, %v; want 1, nil", first.count, first.err)
	}

	if count := loadItemCount(t, db, 301, `SELECT 2`); count != 2 {
		t.Fatalf("post-invalidation count = %d, want fresh 2", count)
	}
}

func TestInvalidateItemListCountCachePreservesUnrelatedInflightStore(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	db := &blockingItemCountDB{
		Database: tdb.GetDatabase(),
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-db.release:
		default:
			close(db.release)
		}
	})

	deadline, cancelDeadline := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancelDeadline()
	result := make(chan itemCountResult, 1)
	go func() {
		count, err := cachedItemListCount(t.Context(), db, 401, `SELECT 1`)
		result <- itemCountResult{count: count, err: err}
	}()
	waitItemCountSignal(t, deadline, db.started, "unrelated count query start")

	InvalidateItemListCountCache(db, 402)
	close(db.release)
	first := waitItemCountResult(t, deadline, result, "unrelated count query result")
	if first.err != nil || first.count != 1 {
		t.Fatalf("in-flight result = %d, %v; want 1, nil", first.count, first.err)
	}

	if count := loadItemCount(t, db, 401, `SELECT 2`); count != 1 {
		t.Fatalf("unrelated in-flight count = %d, want cached 1", count)
	}
}

func TestItemFieldUpdateDoesNotInvalidateWorkspaceCount(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	repo := NewItemRepository(db)

	itemID, err := repo.CreateWithRetry(t.Context(), &models.Item{
		WorkspaceID: data.WorkspaceID,
		Title:       "Count-neutral update",
	}, nil)
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if count := loadItemCount(t, db, data.WorkspaceID, `SELECT 1`); count != 1 {
		t.Fatalf("initial cached count = %d, want 1", count)
	}

	if err := database.WithTx(db, func(tx database.Tx) error {
		return repo.UpdateFields(tx, itemID, map[string]any{"title": "Updated title"})
	}); err != nil {
		t.Fatalf("update item title: %v", err)
	}
	if count := loadItemCount(t, db, data.WorkspaceID, `SELECT 2`); count != 1 {
		t.Fatalf("count after field update = %d, want cached 1", count)
	}
}

func TestItemListCountCacheSweepEvictsOverBoundEntries(t *testing.T) {
	isolateItemListCountCache(t)

	now := time.Now()
	itemListCountCache.Lock()
	for i := 0; i < itemListCountCacheMaxEntries+256; i++ {
		itemListCountCache.entries[fmt.Sprintf("workspace:%d", i)] = itemListCountCacheEntry{
			count:     1,
			expiresAt: now.Add(time.Minute),
		}
	}
	sweepItemListCountCacheLocked(now)
	got := len(itemListCountCache.entries)
	itemListCountCache.Unlock()

	if got > itemListCountCacheMaxEntries {
		t.Fatalf("entries after sweep = %d, want <= %d", got, itemListCountCacheMaxEntries)
	}
}

func TestItemListCountCacheSweepDropsEntriesExpiredBeyondGrace(t *testing.T) {
	isolateItemListCountCache(t)

	now := time.Now()
	itemListCountCache.Lock()
	itemListCountCache.entries["stale"] = itemListCountCacheEntry{
		count:     1,
		expiresAt: now.Add(-itemListCountCacheSweepGrace - time.Second),
	}
	itemListCountCache.entries["fresh"] = itemListCountCacheEntry{
		count:     2,
		expiresAt: now.Add(time.Minute),
	}
	sweepItemListCountCacheLocked(now)
	_, stale := itemListCountCache.entries["stale"]
	_, fresh := itemListCountCache.entries["fresh"]
	itemListCountCache.Unlock()

	if stale {
		t.Fatal("long-expired entry survived the sweep")
	}
	if !fresh {
		t.Fatal("fresh entry was dropped by the sweep")
	}
}

func TestInvalidateItemListCountCacheKeepsGenerationTombstone(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	db := tdb.GetDatabase()

	if count := loadItemCount(t, db, 501, `SELECT 1`); count != 1 {
		t.Fatalf("initial count = %d, want 1", count)
	}
	InvalidateItemListCountCache(db, 501)

	key := itemListCountCacheKey(db, 501)
	itemListCountCache.Lock()
	entry, ok := itemListCountCache.entries[key]
	itemListCountCache.Unlock()
	if !ok {
		t.Fatal("invalidation dropped the generation tombstone")
	}
	if entry.generation == 0 {
		t.Fatal("invalidation did not bump the generation")
	}
	if !entry.expiresAt.IsZero() {
		t.Fatalf("invalidated entry should be expired, got expiresAt=%v", entry.expiresAt)
	}
	if count := loadItemCount(t, db, 501, `SELECT 2`); count != 2 {
		t.Fatalf("post-invalidation count = %d, want fresh 2", count)
	}
}

// isolateItemListCountCache swaps in an empty cache map for the duration of a
// test and restores the previous map afterwards, so sweep tests do not disturb
// entries other tests rely on.
func isolateItemListCountCache(t *testing.T) {
	t.Helper()
	itemListCountCache.Lock()
	savedEntries := itemListCountCache.entries
	savedSweep := itemListCountCache.lastSweep
	itemListCountCache.entries = make(map[string]itemListCountCacheEntry)
	itemListCountCache.lastSweep = time.Time{}
	itemListCountCache.Unlock()
	t.Cleanup(func() {
		itemListCountCache.Lock()
		itemListCountCache.entries = savedEntries
		itemListCountCache.lastSweep = savedSweep
		itemListCountCache.Unlock()
	})
}

func waitItemCountSignal(t *testing.T, ctx context.Context, signal <-chan struct{}, operation string) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatalf("timed out waiting for %s: %v", operation, ctx.Err())
	}
}

func waitItemCountResult(t *testing.T, ctx context.Context, result <-chan itemCountResult, operation string) itemCountResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-ctx.Done():
		t.Fatalf("timed out waiting for %s: %v", operation, ctx.Err())
		return itemCountResult{}
	}
}

func loadItemCount(t *testing.T, db database.Database, workspaceID int, query string) int {
	t.Helper()
	count, err := cachedItemListCount(t.Context(), db, workspaceID, query)
	if err != nil {
		t.Fatalf("load item count: %v", err)
	}
	return count
}
