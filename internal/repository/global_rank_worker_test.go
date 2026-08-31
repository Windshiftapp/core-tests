//go:build test

package repository

import (
	"context"
	"database/sql"
	"runtime"
	"strings"
	"testing"
	"time"

	"windshift/internal/testutils"
)

func TestGlobalRankMigrationWorkerResumesFrontierAndHonorsLease(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	workspaceID := createFracIndexTestWorkspace(t, tdb.DB)
	for number, rank := range []string{"0|a1", "0|a2", "0|a3", "0|a4", "0|a5"} {
		insertItemWithFracIndex(t, tdb.DB, workspaceID, number+1, rank)
	}

	base := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	ownerA := NewGlobalRankMigrationWorker(tdb.DB, "balancer-a", 2, 10*time.Second)
	ownerA.now = func() time.Time { return base }
	first, err := ownerA.Run(context.Background())
	if err != nil {
		t.Fatalf("first migration batch: %v", err)
	}
	if first.Migrated != 2 || first.Completed {
		t.Fatalf("first batch = %+v, want two rows and incomplete", first)
	}
	if first.State.Phase != GlobalRankPhaseMigrating || first.State.Frontier == nil || *first.State.Frontier != "0|a4" {
		t.Fatalf("first state = %+v, want migrating frontier 0|a4", first.State)
	}
	assertOrderedWorkspaceItemNumbers(t, tdb.DB, []int{1, 2, 3, 4, 5})

	ownerB := NewGlobalRankMigrationWorker(tdb.DB, "balancer-b", 2, 10*time.Second)
	ownerB.now = func() time.Time { return base.Add(time.Second) }
	blocked, err := ownerB.Run(context.Background())
	if err != nil {
		t.Fatalf("live lease observation: %v", err)
	}
	if blocked.LeaseAcquired || blocked.Migrated != 0 {
		t.Fatalf("live lease result = %+v, want no acquisition or migration", blocked)
	}
	assertOrderedWorkspaceItemNumbers(t, tdb.DB, []int{1, 2, 3, 4, 5})

	ownerB.now = func() time.Time { return base.Add(11 * time.Second) }
	second, err := ownerB.Run(context.Background())
	if err != nil {
		t.Fatalf("resumed migration batch: %v", err)
	}
	if second.Migrated != 2 || second.Completed {
		t.Fatalf("second batch = %+v, want two rows and incomplete", second)
	}
	third, err := ownerB.Run(context.Background())
	if err != nil {
		t.Fatalf("final migration batch: %v", err)
	}
	if third.Migrated != 1 || !third.Completed || third.State.Phase != GlobalRankPhaseStable || third.State.ActiveBucket != GlobalRankBucket1 {
		t.Fatalf("final batch = %+v, want completed stable bucket 1", third)
	}
	assertOrderedWorkspaceItemNumbers(t, tdb.DB, []int{1, 2, 3, 4, 5})
	assertNoDuplicateFracIndexes(t, tdb.DB)
}

func TestGlobalRankMigrationWorkerRebalancesLongFractionalPayloads(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	workspaceID := createFracIndexTestWorkspace(t, tdb.DB)

	longPrefix := "a0" + strings.Repeat("0", fracIndexRebalanceLengthThreshold)
	originalRanks := []string{
		"0|" + longPrefix + "1",
		"0|" + longPrefix + "2",
		"0|" + longPrefix + "3",
	}
	for number, rank := range originalRanks {
		insertItemWithFracIndex(t, tdb.DB, workspaceID, number+1, rank)
	}

	worker := NewGlobalRankMigrationWorker(tdb.DB, "normalizing-worker", 2, time.Minute)
	first, err := worker.Run(context.Background())
	if err != nil {
		t.Fatalf("first normalizing batch: %v", err)
	}
	if first.Completed || first.Migrated != 2 {
		t.Fatalf("first normalizing batch = %+v, want two rows and active migration", first)
	}
	integrity, err := NewFracIndexRepository(tdb.DB).GetGlobalRankIntegrity(first.State, time.Now().UTC())
	if err != nil {
		t.Fatalf("check normalizing migration integrity: %v", err)
	}
	if !integrity.Healthy || integrity.FrontierViolationCount != 0 {
		t.Fatalf("normalizing migration integrity = %+v, want healthy frontier", integrity)
	}
	runGlobalRankWorkerToCompletion(t, worker)

	rows, err := tdb.DB.Query("SELECT workspace_item_number, frac_index FROM items ORDER BY frac_index")
	if err != nil {
		t.Fatalf("read normalized ranks: %v", err)
	}
	defer rows.Close()
	for index := 0; rows.Next(); index++ {
		var number int
		var rank string
		if err := rows.Scan(&number, &rank); err != nil {
			t.Fatalf("scan normalized rank %d: %v", index, err)
		}
		if number != index+1 {
			t.Fatalf("normalized order at %d = item %d, want item %d", index, number, index+1)
		}
		parsed, err := ParseGlobalRank(rank)
		if err != nil {
			t.Fatalf("parse normalized rank %q: %v", rank, err)
		}
		if parsed.Bucket != GlobalRankBucket1 {
			t.Fatalf("normalized rank %q uses bucket %d, want bucket 1", rank, parsed.Bucket)
		}
		if len(rank) >= fracIndexRebalanceLengthThreshold {
			t.Fatalf("normalized rank remained pathological: len=%d rank=%q", len(rank), rank)
		}
		if rank == originalRanks[index] {
			t.Fatalf("migration only changed the bucket for rank %q", rank)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate normalized ranks: %v", err)
	}
}

func TestGlobalRankMigrationBatchSpacingStaysShortAtOneHundredThousandRows(t *testing.T) {
	for _, direction := range []GlobalRankDirection{GlobalRankDirectionHighToLow, GlobalRankDirectionLowToHigh} {
		t.Run(string(direction), func(t *testing.T) {
			left, right := "", ""
			maxLength := 0
			for migrated := 0; migrated < 100000; migrated += DefaultGlobalRankMigrationBatchSize {
				batchSize := min(DefaultGlobalRankMigrationBatchSize, 100000-migrated)
				fractions, err := generateEvenlySpacedFracKeys(left, right, batchSize)
				if err != nil {
					t.Fatalf("generate batch at row %d: %v", migrated, err)
				}
				for _, fraction := range fractions {
					if err := validateOrderKey(fraction); err != nil {
						t.Fatalf("generated invalid fraction %q: %v", fraction, err)
					}
					maxLength = max(maxLength, len(fraction))
				}
				if direction == GlobalRankDirectionHighToLow {
					if right != "" && fractions[len(fractions)-1] >= right {
						t.Fatalf("batch at row %d did not stay below boundary %q", migrated, right)
					}
					right = fractions[0]
				} else {
					if left != "" && fractions[0] <= left {
						t.Fatalf("batch at row %d did not stay above boundary %q", migrated, left)
					}
					left = fractions[len(fractions)-1]
				}
			}
			if maxLength > 16 {
				t.Fatalf("100k-row normalized rank length = %d, want at most 16", maxLength)
			}
		})
	}
}

func TestGlobalRankMigrationWorkerUsesLowToHighForBucketTwo(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	workspaceID := createFracIndexTestWorkspace(t, tdb.DB)
	for number, rank := range []string{"2|a1", "2|a2", "2|a3"} {
		insertItemWithFracIndex(t, tdb.DB, workspaceID, number+1, rank)
	}
	if _, err := tdb.DB.Exec("UPDATE global_rank_state SET active_bucket = 2 WHERE id = 1"); err != nil {
		t.Fatalf("set bucket two active: %v", err)
	}

	worker := NewGlobalRankMigrationWorker(tdb.DB, "balancer-two", 10, time.Minute)
	worker.now = func() time.Time { return time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC) }
	result, err := worker.Run(context.Background())
	if err != nil {
		t.Fatalf("bucket two migration: %v", err)
	}
	if !result.Completed || result.State.ActiveBucket != GlobalRankBucket0 || result.State.Phase != GlobalRankPhaseStable {
		t.Fatalf("bucket two result = %+v, want completed stable bucket 0", result)
	}
	assertOrderedWorkspaceItemNumbers(t, tdb.DB, []int{1, 2, 3})
}

func TestGlobalRankMigrationWorkerLeavesPausedMigrationPaused(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	workspaceID := createFracIndexTestWorkspace(t, tdb.DB)
	insertItemWithFracIndex(t, tdb.DB, workspaceID, 1, "0|a1")

	if _, err := ControlGlobalRankMigration(context.Background(), tdb.DB, GlobalRankMigrationStart); err != nil {
		t.Fatalf("start migration: %v", err)
	}
	if _, err := ControlGlobalRankMigration(context.Background(), tdb.DB, GlobalRankMigrationPause); err != nil {
		t.Fatalf("pause migration: %v", err)
	}

	worker := NewGlobalRankMigrationWorker(tdb.DB, "paused-worker", 10, time.Minute)
	result, err := worker.Run(context.Background())
	if err != nil {
		t.Fatalf("observe paused migration: %v", err)
	}
	if result.LeaseAcquired || result.Migrated != 0 || result.State.Phase != GlobalRankPhasePaused {
		t.Fatalf("paused worker result = %+v, want an inert paused state", result)
	}
	state, err := LoadGlobalRankState(tdb.DB)
	if err != nil {
		t.Fatalf("reload paused state: %v", err)
	}
	if state.Phase != GlobalRankPhasePaused {
		t.Fatalf("worker resumed paused migration: %+v", state)
	}
}

func TestMoveItemBetweenCrossesHighToLowMigrationFrontier(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	workspaceID := createFracIndexTestWorkspace(t, tdb.DB)
	ids := make([]int, 0, 5)
	for number, rank := range []string{"0|a0", "0|a1", "0|a2", "0|a3", "0|a4"} {
		ids = append(ids, int(insertItemWithFracIndex(t, tdb.DB, workspaceID, number+1, rank)))
	}

	worker := NewGlobalRankMigrationWorker(tdb.DB, "frontier-high", 2, time.Minute)
	first, err := worker.Run(context.Background())
	if err != nil {
		t.Fatalf("first migration batch: %v", err)
	}
	if first.Completed || first.State.Frontier == nil || *first.State.Frontier != "0|a3" {
		t.Fatalf("first migration state = %+v, want frontier 0|a3", first.State)
	}

	movingID, prevID, nextID := ids[0], ids[2], ids[3]
	newKey, err := MoveItemBetween(tdb.DB, movingID, &prevID, &nextID)
	if err != nil {
		t.Fatalf("move across high-to-low frontier: %v", err)
	}
	parsed, err := ParseGlobalRank(newKey)
	if err != nil {
		t.Fatalf("parse moved rank: %v", err)
	}
	if parsed.Bucket != GlobalRankBucket0 || parsed.Fraction <= "a2" {
		t.Fatalf("moved rank = %q, want the high end of the active bucket", newKey)
	}
	assertOrderedItemIDs(t, tdb.DB, []int{ids[1], ids[2], movingID, ids[3], ids[4]})

	runGlobalRankWorkerToCompletion(t, worker)
	assertNoDuplicateFracIndexes(t, tdb.DB)
	assertOrderedItemIDs(t, tdb.DB, []int{ids[1], ids[2], movingID, ids[3], ids[4]})
}

func TestMoveItemBetweenCrossesLowToHighMigrationFrontier(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	workspaceID := createFracIndexTestWorkspace(t, tdb.DB)
	ids := make([]int, 0, 5)
	for number, rank := range []string{"2|a1", "2|a2", "2|a3", "2|a4", "2|a5"} {
		ids = append(ids, int(insertItemWithFracIndex(t, tdb.DB, workspaceID, number+1, rank)))
	}
	if _, err := tdb.DB.Exec("UPDATE global_rank_state SET active_bucket = 2 WHERE id = 1"); err != nil {
		t.Fatalf("set bucket two active: %v", err)
	}

	worker := NewGlobalRankMigrationWorker(tdb.DB, "frontier-low", 2, time.Minute)
	first, err := worker.Run(context.Background())
	if err != nil {
		t.Fatalf("first migration batch: %v", err)
	}
	if first.Completed || first.State.Frontier == nil || *first.State.Frontier != "2|a2" {
		t.Fatalf("first migration state = %+v, want frontier 2|a2", first.State)
	}

	movingID, prevID, nextID := ids[4], ids[1], ids[2]
	newKey, err := MoveItemBetween(tdb.DB, movingID, &prevID, &nextID)
	if err != nil {
		t.Fatalf("move across low-to-high frontier: %v", err)
	}
	parsed, err := ParseGlobalRank(newKey)
	if err != nil {
		t.Fatalf("parse moved rank: %v", err)
	}
	if parsed.Bucket != GlobalRankBucket2 || parsed.Fraction >= "a3" {
		t.Fatalf("moved rank = %q, want the low end of the active bucket", newKey)
	}
	assertOrderedItemIDs(t, tdb.DB, []int{ids[0], ids[1], movingID, ids[2], ids[3]})

	runGlobalRankWorkerToCompletion(t, worker)
	assertNoDuplicateFracIndexes(t, tdb.DB)
	assertOrderedItemIDs(t, tdb.DB, []int{ids[0], ids[1], movingID, ids[2], ids[3]})
}

func TestGlobalRankMigrationWorkerRecordsMalformedRankFailure(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	workspaceID := createFracIndexTestWorkspace(t, tdb.DB)
	id := insertItemWithFracIndex(t, tdb.DB, workspaceID, 1, "0|a1")
	if _, err := tdb.DB.Exec("UPDATE items SET frac_index = ? WHERE id = ?", "0|a", id); err != nil {
		t.Fatalf("corrupt rank: %v", err)
	}

	worker := NewGlobalRankMigrationWorker(tdb.DB, "balancer-failure", 10, time.Minute)
	result, err := worker.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid active-bucket rank") {
		t.Fatalf("malformed rank error = %v, want invalid-rank failure", err)
	}
	if result.State.Phase != GlobalRankPhaseFailed || result.State.LastError == nil {
		t.Fatalf("failure state = %+v, want failed phase and reason", result.State)
	}
	state, err := LoadGlobalRankState(tdb.DB)
	if err != nil {
		t.Fatalf("load failure state: %v", err)
	}
	if state.Phase != GlobalRankPhaseFailed || state.LeaseOwner != nil {
		t.Fatalf("persisted failure state = %+v, want failed without lease", state)
	}
}

func TestGlobalRankMigrationCompletionSerializesConcurrentAppend(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	if tdb.DB.GetDriverName() != "postgres" {
		t.Skip("PostgreSQL advisory-lock concurrency contract")
	}
	workspaceID := createFracIndexTestWorkspace(t, tdb.DB)
	insertItemWithFracIndex(t, tdb.DB, workspaceID, 1, "2|a1")
	if _, err := tdb.DB.Exec("UPDATE global_rank_state SET active_bucket = 2 WHERE id = 1"); err != nil {
		t.Fatalf("set bucket two active: %v", err)
	}

	workerAtCompletion := make(chan struct{})
	releaseWorker := make(chan struct{})
	defer func() {
		select {
		case <-releaseWorker:
		default:
			close(releaseWorker)
		}
	}()
	worker := NewGlobalRankMigrationWorker(tdb.DB, "completion-race", 128, time.Minute)
	worker.beforeCompletion = func() {
		close(workerAtCompletion)
		<-releaseWorker
	}
	workerResult := make(chan error, 1)
	go func() {
		_, err := worker.Run(context.Background())
		workerResult <- err
	}()
	<-workerAtCompletion

	appendAttempted := make(chan struct{})
	appendResult := make(chan error, 1)
	go func() {
		tx, err := tdb.DB.Begin()
		if err != nil {
			appendResult <- err
			return
		}
		defer func() { _ = tx.Rollback() }()
		close(appendAttempted)
		rank, err := GenerateFracIndexForNewItem(tx, tdb.DB.GetDriverName())
		if err == nil {
			_, err = tx.Exec(`
				INSERT INTO items (workspace_id, workspace_item_number, title, is_task, frac_index, created_at, updated_at)
				VALUES (?, 2, 'Concurrent append', TRUE, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, workspaceID, rank)
		}
		if err == nil {
			err = tx.Commit()
		}
		appendResult <- err
	}()
	<-appendAttempted
	waitForGlobalRankAdvisoryWaiter(t, tdb.DB)
	close(releaseWorker)

	if err := <-workerResult; err != nil {
		t.Fatalf("complete migration: %v", err)
	}
	if err := <-appendResult; err != nil {
		t.Fatalf("concurrent append: %v", err)
	}
	state, err := LoadGlobalRankState(tdb.DB)
	if err != nil {
		t.Fatalf("load completed state: %v", err)
	}
	if state.Phase != GlobalRankPhaseStable || state.ActiveBucket != GlobalRankBucket0 {
		t.Fatalf("completed state = %+v, want stable bucket 0", state)
	}
	var wrongBucketCount int
	if err := tdb.DB.QueryRow("SELECT COUNT(*) FROM items WHERE frac_index < '0|' OR frac_index >= '1|'").Scan(&wrongBucketCount); err != nil {
		t.Fatalf("count non-active ranks: %v", err)
	}
	if wrongBucketCount != 0 {
		t.Fatalf("stable bucket 0 contains %d ranks outside bucket 0", wrongBucketCount)
	}
}

func TestGlobalRankMigrationBatchQueryUsesFracIndexRangeIndex(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	target := GlobalRankBucket1
	direction := GlobalRankDirectionHighToLow
	frontier := "0|a5"
	state := GlobalRankState{
		ActiveBucket: GlobalRankBucket0,
		TargetBucket: &target,
		Phase:        GlobalRankPhaseMigrating,
		Direction:    &direction,
		Frontier:     &frontier,
	}
	query, args, err := globalRankMigrationRowsQuery(state, 128, tdb.DB.GetDriverName())
	if err != nil {
		t.Fatalf("build migration query: %v", err)
	}

	plan := ""
	if tdb.DB.GetDriverName() == "postgres" {
		tx, err := tdb.DB.Begin()
		if err != nil {
			t.Fatalf("begin explain transaction: %v", err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec("SET LOCAL enable_seqscan = off"); err != nil {
			t.Fatalf("disable sequential scan for deterministic plan assertion: %v", err)
		}
		rows, err := tx.Query("EXPLAIN "+query, args...)
		if err != nil {
			t.Fatalf("explain migration query: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatalf("scan PostgreSQL plan: %v", err)
			}
			plan += line + "\n"
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate PostgreSQL plan: %v", err)
		}
	} else {
		rows, err := tdb.DB.Query("EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatalf("explain migration query: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatalf("scan SQLite plan: %v", err)
			}
			plan += detail + "\n"
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate SQLite plan: %v", err)
		}
	}
	if !strings.Contains(plan, "idx_items_frac_index") {
		t.Fatalf("migration query plan does not use idx_items_frac_index:\n%s", plan)
	}
}

func waitForGlobalRankAdvisoryWaiter(t *testing.T, db interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := db.QueryRow(`
			SELECT COUNT(*)
			FROM pg_locks
			WHERE locktype = 'advisory'
			  AND classid = ?
			  AND objid = ?
			  AND NOT granted`, globalRankAdvisoryLockClass, globalRankStateRowID).Scan(&waiting); err != nil {
			t.Fatalf("inspect global rank advisory lock: %v", err)
		}
		if waiting > 0 {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("concurrent append never waited for the global rank migration lock")
}

func assertOrderedRanks(t *testing.T, db interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
}, want []string) {
	t.Helper()
	rows, err := db.Query("SELECT frac_index FROM items ORDER BY frac_index")
	if err != nil {
		t.Fatalf("read ordered ranks: %v", err)
	}
	defer rows.Close()
	for i, expected := range want {
		if !rows.Next() {
			t.Fatalf("ordered ranks ended at %d, want %q", i, expected)
		}
		var got string
		if err := rows.Scan(&got); err != nil {
			t.Fatalf("scan ordered rank %d: %v", i, err)
		}
		if got != expected {
			t.Errorf("ordered rank %d = %q, want %q", i, got, expected)
		}
	}
	if rows.Next() {
		t.Fatal("ordered ranks contain more rows than expected")
	}
}

func assertOrderedWorkspaceItemNumbers(t *testing.T, db interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
}, want []int) {
	t.Helper()
	rows, err := db.Query("SELECT workspace_item_number FROM items ORDER BY frac_index")
	if err != nil {
		t.Fatalf("read item order: %v", err)
	}
	defer rows.Close()
	for index, expected := range want {
		if !rows.Next() {
			t.Fatalf("item order ended at %d, want item number %d", index, expected)
		}
		var got int
		if err := rows.Scan(&got); err != nil {
			t.Fatalf("scan item order %d: %v", index, err)
		}
		if got != expected {
			t.Fatalf("item order at %d = %d, want %d", index, got, expected)
		}
	}
	if rows.Next() {
		t.Fatal("item order contains more rows than expected")
	}
}

func assertOrderedItemIDs(t *testing.T, db interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
}, want []int) {
	t.Helper()
	rows, err := db.Query("SELECT id FROM items ORDER BY frac_index")
	if err != nil {
		t.Fatalf("read item order: %v", err)
	}
	defer rows.Close()
	for index, expected := range want {
		if !rows.Next() {
			t.Fatalf("item order ended at %d, want item %d", index, expected)
		}
		var got int
		if err := rows.Scan(&got); err != nil {
			t.Fatalf("scan item order %d: %v", index, err)
		}
		if got != expected {
			t.Fatalf("item order at %d = %d, want %d", index, got, expected)
		}
	}
	if rows.Next() {
		t.Fatal("item order contains more rows than expected")
	}
}

func runGlobalRankWorkerToCompletion(t *testing.T, worker *GlobalRankMigrationWorker) {
	t.Helper()
	for batch := 0; batch < 10; batch++ {
		result, err := worker.Run(context.Background())
		if err != nil {
			t.Fatalf("migration batch %d: %v", batch+2, err)
		}
		if result.Completed {
			return
		}
	}
	t.Fatal("global rank worker did not complete within 10 batches")
}

func assertItemRankBetween(t *testing.T, db interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}, itemID int, lower, upper string) {
	t.Helper()
	var rank string
	if err := db.QueryRow("SELECT frac_index FROM items WHERE id = ?", itemID).Scan(&rank); err != nil {
		t.Fatalf("read item %d rank: %v", itemID, err)
	}
	if !(rank > lower && rank < upper) {
		t.Fatalf("item %d rank = %q, want between %q and %q", itemID, rank, lower, upper)
	}
}
