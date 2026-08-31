package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

type fracIndexRecordingTx struct {
	database.Tx
	queries      []string
	args         [][]interface{}
	rowsAffected int64
}

func (tx *fracIndexRecordingTx) Exec(query string, args ...interface{}) (sql.Result, error) {
	tx.queries = append(tx.queries, query)
	tx.args = append(tx.args, args)
	return driver.RowsAffected(tx.rowsAffected), nil
}

func createFracIndexTestDB(t *testing.T) database.Database {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	return tdb.DB
}

func createFracIndexTestWorkspace(t testing.TB, db database.Database) int {
	t.Helper()
	// Bool placeholders (not integer 1/0) and RETURNING id (not LastInsertId)
	// so the helper works on both SQLite and Postgres — Postgres rejects
	// integer literals for boolean columns and has no LastInsertId.
	var id int
	err := db.QueryRow(`
		INSERT INTO workspaces (name, key, description, active, is_personal, created_at, updated_at)
		VALUES ('Frac Test', 'FRAC', 'Test', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`, true, false).Scan(&id)
	if err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}
	return id
}

func insertItemWithFracIndex(t testing.TB, db database.Database, workspaceID, number int, fracIndex string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, is_task, frac_index, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`, workspaceID, number, "Item", true, fracIndex).Scan(&id)
	if err != nil {
		t.Fatalf("failed to insert item: %v", err)
	}
	return id
}

func itemIDsByFracIndex(t *testing.T, db database.Database, excludeID int) []int {
	t.Helper()
	rows, err := db.Query(`SELECT id FROM items WHERE frac_index IS NOT NULL AND id <> ? ORDER BY frac_index`, excludeID)
	if err != nil {
		t.Fatalf("query frac_index order: %v", err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan ordered id: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate ordered ids: %v", err)
	}
	return ids
}

func assertNoDuplicateFracIndexes(t *testing.T, db database.Database) {
	t.Helper()
	var duplicateGroups int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM (
			SELECT frac_index FROM items
			WHERE frac_index IS NOT NULL
			GROUP BY frac_index
			HAVING COUNT(*) > 1
		) d
	`).Scan(&duplicateGroups); err != nil {
		t.Fatalf("count duplicate frac_index groups: %v", err)
	}
	if duplicateGroups != 0 {
		t.Fatalf("found %d duplicate frac_index groups", duplicateGroups)
	}
}

func previewMoveFracIndex(t *testing.T, db database.Database, movingID int, prevID, nextID *int) string {
	t.Helper()
	key, err := database.WithTxResult(db, func(tx database.Tx) (string, error) {
		prev, err := readFracIndexForUpdate(tx, prevID, db.GetDriverName())
		if err != nil {
			return "", err
		}
		next, err := readFracIndexForUpdate(tx, nextID, db.GetDriverName())
		if err != nil {
			return "", err
		}
		return chooseMoveFracIndex(tx, movingID, prev, next, db.GetDriverName())
	})
	if err != nil {
		t.Fatalf("preview move frac_index: %v", err)
	}
	return key
}

// generateAndInsertAtEnd mirrors the production flow: a single tx reads the
// current MAX(frac_index), KeyBetween-generates the next key, INSERTs the
// item, commits. Returns the generated key. Test helper for exercising the
// real atomic primitive rather than poking the generator in isolation.
func generateAndInsertAtEnd(t *testing.T, db database.Database, workspaceID, number int) string {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	key, err := GenerateFracIndexForNewItem(tx)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO items (workspace_id, workspace_item_number, title, is_task, frac_index, created_at, updated_at)
		VALUES (?, ?, 'I', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, workspaceID, number, true, key); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return key
}

func TestKeyBetween_Bounds(t *testing.T) {
	tests := []struct {
		name    string
		a, b    string
		wantErr bool
	}{
		{name: "both empty returns zero", a: "", b: ""},
		{name: "empty left generates below b", a: "", b: "a1"},
		{name: "empty right generates above a", a: "a1", b: ""},
		{name: "between adjacent generates midpoint", a: "a1", b: "a2"},
		{name: "a == b is invalid", a: "a1", b: "a1", wantErr: true},
		{name: "a > b is invalid", a: "a2", b: "a1", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := KeyBetween(tc.a, tc.b)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.a != "" && !(got > tc.a) {
				t.Errorf("expected %q > %q", got, tc.a)
			}
			if tc.b != "" && !(got < tc.b) {
				t.Errorf("expected %q < %q", got, tc.b)
			}
		})
	}
}

func TestKeyBetween_AdjacentKeysGrowLength(t *testing.T) {
	// Repeatedly inserting between the same two bounds should keep producing
	// strictly ordered keys (length may grow but order must hold).
	a, b := "a1", "a2"
	prev := a
	for i := 0; i < 20; i++ {
		got, err := KeyBetween(prev, b)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if !(got > prev && got < b) {
			t.Fatalf("iter %d: got %q not strictly between %q and %q", i, got, prev, b)
		}
		prev = got
	}
}

// TestGenerateFracIndexForNewItem_MonotonicAndUnique exercises the production
// flow (read MAX inside tx → KeyBetween → INSERT → commit) sequentially and
// verifies every key is unique and strictly increasing. With the cache gone,
// this is the basic correctness sanity check.
func TestGenerateFracIndexForNewItem_MonotonicAndUnique(t *testing.T) {
	db := createFracIndexTestDB(t)
	workspaceID := createFracIndexTestWorkspace(t, db)

	const n = 25
	keys := make([]string, 0, n)
	for i := 0; i < n; i++ {
		k := generateAndInsertAtEnd(t, db, workspaceID, i+1)
		keys = append(keys, k)
	}

	seen := make(map[string]bool, n)
	for i, k := range keys {
		if seen[k] {
			t.Errorf("duplicate key %q at index %d", k, i)
		}
		seen[k] = true
		if i > 0 && !(k > keys[i-1]) {
			t.Errorf("keys not monotonically increasing at %d: %q after %q", i, k, keys[i-1])
		}
	}
}

// TestGenerateFracIndexForNewItem_ConcurrentNoDuplicates is the regression
// test for the previous lock-free cache race: two goroutines could read the
// same cached value and return the same KeyBetween output. With the cache
// removed, the MAX read happens inside each tx; SQLite serializes writers
// via its global writer lock so each tx sees the prior tx's committed MAX.
func TestGenerateFracIndexForNewItem_ConcurrentNoDuplicates(t *testing.T) {
	db := createFracIndexTestDB(t)
	workspaceID := createFracIndexTestWorkspace(t, db)

	const workers = 8
	const perWorker = 10
	const total = workers * perWorker

	var mu sync.Mutex
	numberCounter := 0
	keys := make(chan string, total)
	errs := make(chan error, total)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				mu.Lock()
				numberCounter++
				num := numberCounter
				mu.Unlock()

				tx, err := db.Begin()
				if err != nil {
					errs <- err
					return
				}
				k, err := GenerateFracIndexForNewItem(tx)
				if err != nil {
					_ = tx.Rollback()
					errs <- err
					return
				}
				if _, err := tx.Exec(`
					INSERT INTO items (workspace_id, workspace_item_number, title, is_task, frac_index, created_at, updated_at)
					VALUES (?, ?, 'I', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
				`, workspaceID, num, true, k); err != nil {
					_ = tx.Rollback()
					errs <- err
					return
				}
				if err := tx.Commit(); err != nil {
					errs <- err
					return
				}
				keys <- k
			}
		}()
	}
	wg.Wait()
	close(keys)
	close(errs)

	for err := range errs {
		t.Fatalf("worker error: %v", err)
	}

	seen := make(map[string]int, total)
	for k := range keys {
		seen[k]++
	}
	if len(seen) != total {
		dupes := 0
		for _, c := range seen {
			if c > 1 {
				dupes++
			}
		}
		t.Fatalf("expected %d unique keys, got %d (duplicate groups: %d)", total, len(seen), dupes)
	}
}

// TestMoveItemBetween_PlacesItemBetweenNeighbors verifies the reorder
// primitive: given prev and next neighbor IDs, the target item's frac_index
// ends up strictly between theirs, in one atomic tx.
func TestMoveItemBetween_PlacesItemBetweenNeighbors(t *testing.T) {
	db := createFracIndexTestDB(t)
	workspaceID := createFracIndexTestWorkspace(t, db)

	prevID := int(insertItemWithFracIndex(t, db, workspaceID, 1, "a2"))
	movingID := int(insertItemWithFracIndex(t, db, workspaceID, 2, "z9"))
	nextID := int(insertItemWithFracIndex(t, db, workspaceID, 3, "a5"))

	newKey, err := MoveItemBetween(db, movingID, &prevID, &nextID)
	if err != nil {
		t.Fatalf("MoveItemBetween: %v", err)
	}
	if !(newKey > "a2" && newKey < "a5") {
		t.Fatalf("expected %q strictly between \"a2\" and \"a5\"", newKey)
	}

	// Verify it was actually persisted.
	var persisted string
	if err := db.QueryRow("SELECT frac_index FROM items WHERE id = ?", movingID).Scan(&persisted); err != nil {
		t.Fatalf("read persisted: %v", err)
	}
	if persisted != newKey {
		t.Fatalf("returned key %q != persisted %q", newKey, persisted)
	}
}

// TestMoveItemBetween_EndOfList confirms passing nextID=nil generates a key
// after the prev neighbor (move to end).
func TestMoveItemBetween_EndOfList(t *testing.T) {
	db := createFracIndexTestDB(t)
	workspaceID := createFracIndexTestWorkspace(t, db)

	prevID := int(insertItemWithFracIndex(t, db, workspaceID, 1, "a2"))
	movingID := int(insertItemWithFracIndex(t, db, workspaceID, 2, "z9"))

	newKey, err := MoveItemBetween(db, movingID, &prevID, nil)
	if err != nil {
		t.Fatalf("MoveItemBetween: %v", err)
	}
	if !(newKey > "a2") {
		t.Fatalf("expected %q > \"a2\" for end-of-list move", newKey)
	}
}

// TestMoveItemBetween_StartOfList confirms passing prevID=nil generates a key
// before the next neighbor (move to start).
func TestMoveItemBetween_StartOfList(t *testing.T) {
	db := createFracIndexTestDB(t)
	workspaceID := createFracIndexTestWorkspace(t, db)

	nextID := int(insertItemWithFracIndex(t, db, workspaceID, 1, "a5"))
	movingID := int(insertItemWithFracIndex(t, db, workspaceID, 2, "z9"))

	newKey, err := MoveItemBetween(db, movingID, nil, &nextID)
	if err != nil {
		t.Fatalf("MoveItemBetween: %v", err)
	}
	if !(newKey < "a5") {
		t.Fatalf("expected %q < \"a5\" for start-of-list move", newKey)
	}
}

// Board/backlog reorders pass neighbors from a filtered subset (status column,
// iteration section, etc.), while frac_index remains globally unique. These
// regression tests cover deterministic collisions where the naive midpoint is
// occupied by an item outside the filtered subset.
func TestMoveItemBetween_FilteredSubsetEndAvoidsGlobalSuccessorCollision(t *testing.T) {
	db := createFracIndexTestDB(t)
	workspaceID := createFracIndexTestWorkspace(t, db)

	prevID := int(insertItemWithFracIndex(t, db, workspaceID, 1, "a0"))
	_ = insertItemWithFracIndex(t, db, workspaceID, 2, "a1") // Occupies KeyBetween("a0", "").
	movingID := int(insertItemWithFracIndex(t, db, workspaceID, 3, "z9"))

	newKey, err := MoveItemBetween(db, movingID, &prevID, nil)
	if err != nil {
		t.Fatalf("MoveItemBetween: %v", err)
	}
	if newKey == "a1" {
		t.Fatalf("move reused occupied global successor %q", newKey)
	}
	if newKey <= "a0" {
		t.Fatalf("new key %q must sort after filtered prev a0", newKey)
	}
}

func TestMoveItemBetween_FilteredSubsetBetweenAvoidsGlobalMidpointCollision(t *testing.T) {
	db := createFracIndexTestDB(t)
	workspaceID := createFracIndexTestWorkspace(t, db)

	prevID := int(insertItemWithFracIndex(t, db, workspaceID, 1, "a0"))
	_ = insertItemWithFracIndex(t, db, workspaceID, 2, "a1") // Occupies KeyBetween("a0", "a2").
	nextID := int(insertItemWithFracIndex(t, db, workspaceID, 3, "a2"))
	movingID := int(insertItemWithFracIndex(t, db, workspaceID, 4, "z9"))

	newKey, err := MoveItemBetween(db, movingID, &prevID, &nextID)
	if err != nil {
		t.Fatalf("MoveItemBetween: %v", err)
	}
	if newKey == "a1" {
		t.Fatalf("move reused occupied global midpoint %q", newKey)
	}
	if !(newKey > "a0" && newKey < "a2") {
		t.Fatalf("new key %q must be strictly between filtered neighbors a0 and a2", newKey)
	}
}

func TestMoveItemBetween_FilteredSubsetEmptyUsesGlobalAppend(t *testing.T) {
	db := createFracIndexTestDB(t)
	workspaceID := createFracIndexTestWorkspace(t, db)

	_ = insertItemWithFracIndex(t, db, workspaceID, 1, "a0") // Occupies KeyBetween("", "").
	movingID := int(insertItemWithFracIndex(t, db, workspaceID, 2, "z9"))

	newKey, err := MoveItemBetween(db, movingID, nil, nil)
	if err != nil {
		t.Fatalf("MoveItemBetween: %v", err)
	}
	if newKey == "a0" {
		t.Fatalf("move reused occupied initial key %q", newKey)
	}
}

func TestGenerateEvenlySpacedFracKeys_BalancesDenseGap(t *testing.T) {
	keys, err := generateEvenlySpacedFracKeys("a0", "a1", 1000)
	if err != nil {
		t.Fatalf("generateEvenlySpacedFracKeys: %v", err)
	}
	maxLen := 0
	for i, key := range keys {
		if len(key) > maxLen {
			maxLen = len(key)
		}
		if !(key > "a0" && key < "a1") {
			t.Fatalf("key %d=%q is outside gap a0..a1", i, key)
		}
		if i > 0 && keys[i-1] >= key {
			t.Fatalf("keys not strictly increasing at %d: %q >= %q", i, keys[i-1], key)
		}
	}
	if maxLen > 8 {
		t.Fatalf("balanced 1000-key gap should stay short, max len=%d", maxLen)
	}
}

func TestMoveItemBetween_LongHotGapTriggersLocalRebalance(t *testing.T) {
	db := createFracIndexTestDB(t)
	workspaceID := createFracIndexTestWorkspace(t, db)

	prevID := int(insertItemWithFracIndex(t, db, workspaceID, 1, "a0"))

	// Create a pathological hot gap immediately after prev by repeatedly
	// inserting between prev and the current first successor. The immediate
	// successor becomes long enough that MoveItemBetween must rebalance a local
	// window instead of writing another very long midpoint.
	upper := "a1"
	for i := 0; i < 900; i++ {
		key, err := KeyBetween("a0", upper)
		if err != nil {
			t.Fatalf("generate dense key %d: %v", i, err)
		}
		insertItemWithFracIndex(t, db, workspaceID, i+2, key)
		upper = key
	}
	movingID := int(insertItemWithFracIndex(t, db, workspaceID, 2000, "z9"))

	beforeOrder := itemIDsByFracIndex(t, db, movingID)
	naiveKey := previewMoveFracIndex(t, db, movingID, &prevID, nil)
	if len(naiveKey) <= fracIndexRebalanceLengthThreshold {
		t.Fatalf("test setup did not create a long hot-gap key: len=%d key=%q", len(naiveKey), naiveKey)
	}

	newKey, err := MoveItemBetween(db, movingID, &prevID, nil)
	if err != nil {
		t.Fatalf("MoveItemBetween: %v", err)
	}
	if len(newKey) > fracIndexRebalanceLengthThreshold {
		t.Fatalf("local rebalance should keep moved key below threshold: len=%d key=%q", len(newKey), newKey)
	}
	assertNoDuplicateFracIndexes(t, db)

	// Local rebalance may rewrite neighboring keys, but it must preserve the
	// global relative order of every non-moving item. Filtered board/list orders
	// are subsequences of this global order, so they remain stable too.
	afterOrder := itemIDsByFracIndex(t, db, movingID)
	if !slices.Equal(beforeOrder, afterOrder) {
		t.Fatalf("local rebalance changed non-moving global order")
	}
}

func TestMoveItemBetween_CanonicalLongHotGapTriggersLocalRebalance(t *testing.T) {
	db := createFracIndexTestDB(t)
	workspaceID := createFracIndexTestWorkspace(t, db)

	prevID := int(insertItemWithFracIndex(t, db, workspaceID, 1, "0|a0"))
	upper := "a1"
	for i := 0; i < 900; i++ {
		fraction, err := KeyBetween("a0", upper)
		if err != nil {
			t.Fatalf("generate dense canonical key %d: %v", i, err)
		}
		insertItemWithFracIndex(t, db, workspaceID, i+2, "0|"+fraction)
		upper = fraction
	}
	movingID := int(insertItemWithFracIndex(t, db, workspaceID, 2000, "0|z9"))

	beforeOrder := itemIDsByFracIndex(t, db, movingID)
	naiveKey := previewMoveFracIndex(t, db, movingID, &prevID, nil)
	if len(naiveKey) <= fracIndexRebalanceLengthThreshold {
		t.Fatalf("test setup did not create a long canonical hot-gap key: len=%d key=%q", len(naiveKey), naiveKey)
	}

	newKey, err := MoveItemBetween(db, movingID, &prevID, nil)
	if err != nil {
		t.Fatalf("MoveItemBetween: %v", err)
	}
	if len(newKey) > fracIndexRebalanceLengthThreshold {
		t.Fatalf("canonical local rebalance should keep moved key below threshold: len=%d key=%q", len(newKey), newKey)
	}
	if parsed, err := ParseGlobalRank(newKey); err != nil || parsed.Bucket != GlobalRankBucket0 {
		t.Fatalf("canonical local rebalance returned %q: parsed=%+v err=%v", newKey, parsed, err)
	}
	assertNoDuplicateFracIndexes(t, db)
	afterOrder := itemIDsByFracIndex(t, db, movingID)
	if !slices.Equal(beforeOrder, afterOrder) {
		t.Fatal("canonical local rebalance changed non-moving global order")
	}
	state, err := LoadGlobalRankState(db)
	if err != nil {
		t.Fatalf("load global rank state: %v", err)
	}
	if state.Phase != GlobalRankPhaseMigrating || state.TargetBucket == nil || *state.TargetBucket != GlobalRankBucket1 {
		t.Fatalf("hot-gap fallback state = %+v, want migration scheduled to bucket one", state)
	}

	worker := NewGlobalRankMigrationWorker(db, "hot-gap-normalizer", DefaultGlobalRankMigrationBatchSize, time.Minute)
	runGlobalRankWorkerToCompletion(t, worker)
	finalOrder := itemIDsByFracIndex(t, db, movingID)
	if !slices.Equal(afterOrder, finalOrder) {
		t.Fatal("global hot-gap normalization changed non-moving global order")
	}
	var maxRankLength int
	if err := db.QueryRow("SELECT COALESCE(MAX(LENGTH(frac_index)), 0) FROM items").Scan(&maxRankLength); err != nil {
		t.Fatalf("read normalized maximum rank length: %v", err)
	}
	if maxRankLength >= fracIndexRebalanceLengthThreshold {
		t.Fatalf("global hot-gap normalization left maximum rank length %d", maxRankLength)
	}
}

func TestHotGapSchedulesMigrationAfterFailedStateReset(t *testing.T) {
	db := createFracIndexTestDB(t)
	workspaceID := createFracIndexTestWorkspace(t, db)
	insertItemWithFracIndex(t, db, workspaceID, 1, "0|a1")
	if _, err := db.Exec(`
		UPDATE global_rank_state
		SET target_bucket = 1,
		    phase = 'failed',
		    direction = 'high_to_low',
		    frontier = '0|a1',
		    lease_owner = 'failed-worker',
		    lease_expires_at = CURRENT_TIMESTAMP,
		    migrated_count = 1,
		    total_count = 1,
		    last_error = 'invalid active-bucket rank'
		WHERE id = 1`); err != nil {
		t.Fatalf("set failed migration state: %v", err)
	}

	if _, err := ControlGlobalRankMigration(context.Background(), db, GlobalRankMigrationReset); err != nil {
		t.Fatalf("reset failed migration: %v", err)
	}
	requestGlobalRankMigrationAfterHotGap(db, 1)

	state, err := LoadGlobalRankState(db)
	if err != nil {
		t.Fatalf("load scheduled migration: %v", err)
	}
	if state.Phase != GlobalRankPhaseMigrating || state.TargetBucket == nil || *state.TargetBucket != GlobalRankBucket1 {
		t.Fatalf("hot-gap state = %+v, want migration rescheduled to bucket 1", state)
	}
	if state.LastError != nil {
		t.Fatalf("rescheduled migration retained failure reason %q", *state.LastError)
	}
}

func TestLocalRebalanceKeepsFracIndexesNonNullDuringTransaction(t *testing.T) {
	db := createFracIndexTestDB(t)
	workspaceID := createFracIndexTestWorkspace(t, db)
	insertItemWithFracIndex(t, db, workspaceID, 1, "a0")
	insertItemWithFracIndex(t, db, workspaceID, 2, "a1")
	movingID := int(insertItemWithFracIndex(t, db, workspaceID, 3, "z9"))

	err := database.WithTx(db, func(tx database.Tx) error {
		if err := rebalanceLocalFracIndexWindow(tx, movingID, "a0", "", db.GetDriverName()); err != nil {
			return err
		}
		var nullCount int
		if err := tx.QueryRow("SELECT COUNT(*) FROM items WHERE frac_index IS NULL").Scan(&nullCount); err != nil {
			return err
		}
		if nullCount != 0 {
			return fmt.Errorf("local rebalance exposed %d NULL frac_index values", nullCount)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("local rebalance: %v", err)
	}
}

func BenchmarkCanonicalLocalHotGapRebalance128(b *testing.B) {
	db := newItemListTestDB(b, "canonical-local-hot-gap-rebalance-benchmark")
	workspaceID := createFracIndexTestWorkspace(b, db)
	fractions, err := generateEvenlySpacedFracKeys("", "", fracIndexLocalRebalanceWindowSize)
	if err != nil {
		b.Fatalf("generate benchmark ranks: %v", err)
	}
	for index, fraction := range fractions {
		insertItemWithFracIndex(b, db, workspaceID, index+1, "0|"+fraction)
	}
	movingID := int(insertItemWithFracIndex(b, db, workspaceID, 1000, "0|z9"))
	prev := "0|" + fractions[len(fractions)/2-1]

	b.ReportAllocs()
	b.ReportMetric(fracIndexLocalRebalanceWindowSize, "window-rows")
	b.ResetTimer()
	for range b.N {
		tx, err := db.Begin()
		if err != nil {
			b.Fatalf("begin benchmark transaction: %v", err)
		}
		if err := rebalanceLocalGlobalRankWindow(tx, movingID, prev, "", GlobalRankBucket0, db.GetDriverName()); err != nil {
			_ = tx.Rollback()
			b.Fatalf("rebalance benchmark window: %v", err)
		}
		if err := tx.Rollback(); err != nil {
			b.Fatalf("rollback benchmark transaction: %v", err)
		}
	}
}

func TestUpdateFracIndexesBatchesFullLocalWindowIntoOneStatement(t *testing.T) {
	updates := make([]fracIndexUpdate, fracIndexLocalRebalanceWindowSize+1)
	for index := range updates {
		updates[index] = fracIndexUpdate{id: int64(index + 1), key: fmt.Sprintf("~rebalance-%d", index+1)}
	}
	tx := &fracIndexRecordingTx{rowsAffected: int64(len(updates))}

	if err := updateFracIndexes(tx, updates); err != nil {
		t.Fatalf("batch frac_index update: %v", err)
	}
	if len(tx.queries) != 1 {
		t.Fatalf("executed %d statements, want one set update", len(tx.queries))
	}
	if cases := strings.Count(tx.queries[0], " WHEN ? THEN ?"); cases != len(updates) {
		t.Fatalf("set update has %d CASE branches, want %d", cases, len(updates))
	}
	if got, want := len(tx.args[0]), len(updates)*3; got != want {
		t.Fatalf("set update has %d arguments, want %d", got, want)
	}
}

func TestUpdateFracIndexesRejectsPartialBatch(t *testing.T) {
	updates := []fracIndexUpdate{{id: 1, key: "0|a1"}, {id: 2, key: "0|a2"}}
	tx := &fracIndexRecordingTx{rowsAffected: 1}

	err := updateFracIndexes(tx, updates)
	if err == nil || !strings.Contains(err.Error(), "affected 1 rows, want 2") {
		t.Fatalf("partial batch error = %v, want exact affected-row guard", err)
	}
}

func TestUniqueFracIndexConstraintRejectsDuplicates(t *testing.T) {
	db := createFracIndexTestDB(t)
	workspaceID := createFracIndexTestWorkspace(t, db)

	insertItemWithFracIndex(t, db, workspaceID, 1, "a2")
	_, err := db.Exec(`
		INSERT INTO items (workspace_id, workspace_item_number, title, is_task, frac_index, created_at, updated_at)
		VALUES (?, ?, 'Dup', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, workspaceID, 2, true, "a2")
	if err == nil {
		t.Fatal("expected UNIQUE constraint violation for duplicate frac_index, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("expected UNIQUE error, got: %v", err)
	}
}

// TestIsFracIndexUniqueViolation_DetectsRealError verifies the helper
// recognizes a real INSERT collision on the partial unique index and ignores
// unrelated errors. This is the precise discriminator the retry loops in
// CreateItem and MoveItemBetween use to decide whether to retry.
func TestIsFracIndexUniqueViolation_DetectsRealError(t *testing.T) {
	db := createFracIndexTestDB(t)
	workspaceID := createFracIndexTestWorkspace(t, db)

	insertItemWithFracIndex(t, db, workspaceID, 1, "a2")
	_, err := db.Exec(`
		INSERT INTO items (workspace_id, workspace_item_number, title, is_task, frac_index, created_at, updated_at)
		VALUES (?, ?, 'Dup', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, workspaceID, 2, true, "a2")
	if !IsFracIndexUniqueViolation(err) {
		t.Fatalf("expected IsFracIndexUniqueViolation=true for duplicate frac_index insert, got %v", err)
	}
	if IsWorkspaceItemNumberUniqueViolation(err) {
		t.Fatalf("expected frac_index collision not to be classified as a workspace item-number collision: %v", err)
	}

	_, itemNumberErr := db.Exec(`
		INSERT INTO items (workspace_id, workspace_item_number, title, is_task, frac_index, created_at, updated_at)
		VALUES (?, ?, 'Duplicate number', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, workspaceID, 1, true, "a3")
	if !IsWorkspaceItemNumberUniqueViolation(itemNumberErr) {
		t.Fatalf("expected workspace item-number collision to be classified: %v", itemNumberErr)
	}
	if IsFracIndexUniqueViolation(itemNumberErr) {
		t.Fatalf("expected workspace item-number collision not to be classified as a frac_index collision: %v", itemNumberErr)
	}

	// A non-uniqueness error must not trigger the retry path.
	_, err2 := db.Exec("SELECT * FROM nope_no_such_table")
	if IsFracIndexUniqueViolation(err2) {
		t.Fatalf("expected IsFracIndexUniqueViolation=false for unrelated error, got true: %v", err2)
	}
	// nil is not a violation.
	if IsFracIndexUniqueViolation(nil) {
		t.Fatal("expected IsFracIndexUniqueViolation(nil)=false")
	}
}

func TestFracIndexRetryableTransactionErrorClassifiesPostgresConcurrencyAborts(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "deadlock", err: &pq.Error{Code: "40P01"}, want: true},
		{name: "serialization failure", err: &pq.Error{Code: "40001"}, want: true},
		{name: "unique violation", err: &pq.Error{Code: "23505"}, want: false},
		{name: "unrelated", err: errors.New("connection failed"), want: false},
		{name: "nil", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isFracIndexRetryableTransactionError(tt.err); got != tt.want {
				t.Fatalf("isFracIndexRetryableTransactionError() = %t, want %t", got, tt.want)
			}
		})
	}
}
