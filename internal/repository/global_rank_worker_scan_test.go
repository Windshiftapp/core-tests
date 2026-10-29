//go:build test

package repository

import (
	"context"
	"database/sql"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// countingItemsScanDatabase counts aggregate COUNT(*) scans over items issued
// inside worker transactions so tests can assert batches do not re-scan the
// table. Only transaction-scoped queries are counted; verification queries on
// the raw database bypass the wrapper.
type countingItemsScanDatabase struct {
	database.Database
	itemsCountQueries atomic.Int64
}

func (db *countingItemsScanDatabase) BeginTx(ctx context.Context, opts *sql.TxOptions) (database.Tx, error) {
	tx, err := db.Database.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &countingItemsScanTx{Tx: tx, counter: &db.itemsCountQueries}, nil
}

type countingItemsScanTx struct {
	database.Tx
	counter *atomic.Int64
}

func (tx *countingItemsScanTx) countIfItemsScan(query string) {
	if strings.HasPrefix(strings.TrimSpace(strings.ToUpper(query)), "SELECT COUNT(*) FROM ITEMS") {
		tx.counter.Add(1)
	}
}

func (tx *countingItemsScanTx) QueryRow(query string, args ...any) *sql.Row {
	tx.countIfItemsScan(query)
	return tx.Tx.QueryRow(query, args...)
}

func (tx *countingItemsScanTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	tx.countIfItemsScan(query)
	return tx.Tx.QueryRowContext(ctx, query, args...)
}

func (tx *countingItemsScanTx) Query(query string, args ...any) (*sql.Rows, error) {
	tx.countIfItemsScan(query)
	return tx.Tx.Query(query, args...)
}

func (tx *countingItemsScanTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	tx.countIfItemsScan(query)
	return tx.Tx.QueryContext(ctx, query, args...)
}

// A multi-batch migration must derive its progress incrementally from the
// migration-start snapshot instead of re-running whole-table COUNT scans over
// items in every batch.
func TestGlobalRankMigrationWorkerDoesNotRescanItemsPerBatch(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	workspaceID := createFracIndexTestWorkspace(t, tdb.DB)
	for number, rank := range []string{"0|a1", "0|a2", "0|a3", "0|a4", "0|a5", "0|a6"} {
		insertItemWithFracIndex(t, tdb.DB, workspaceID, number+1, rank)
	}

	counting := &countingItemsScanDatabase{Database: tdb.DB}
	worker := NewGlobalRankMigrationWorker(counting, "count-worker", 2, time.Minute)

	wantRemaining := []int64{4, 2, 0}
	var totalMigrated int64
	for batch := 0; batch < 10; batch++ {
		result, err := worker.Run(context.Background())
		if err != nil {
			t.Fatalf("migration batch %d: %v", batch+1, err)
		}
		if batch < len(wantRemaining) {
			if result.Migrated != 2 {
				t.Fatalf("batch %d migrated %d rows, want 2", batch+1, result.Migrated)
			}
			if result.Remaining != wantRemaining[batch] {
				t.Fatalf("batch %d remaining = %d, want %d", batch+1, result.Remaining, wantRemaining[batch])
			}
		}
		totalMigrated += int64(result.Migrated)
		if result.Completed {
			break
		}
	}

	if totalMigrated != 6 {
		t.Fatalf("migration migrated %d rows, want all 6", totalMigrated)
	}
	if got := counting.itemsCountQueries.Load(); got != 1 {
		t.Fatalf("migration issued %d COUNT scans over items, want 1 (the migration-start snapshot)", got)
	}

	var state GlobalRankState
	if err := tdb.DB.QueryRow(`
		SELECT active_bucket, target_bucket, phase, direction, frontier,
		       lease_owner, lease_expires_at, migrated_count, total_count, last_error
		FROM global_rank_state WHERE id = ?`, globalRankStateRowID).Scan(
		&state.ActiveBucket, &state.TargetBucket, &state.Phase, &state.Direction,
		&state.Frontier, &state.LeaseOwner, &state.LeaseExpiresAt,
		&state.MigratedCount, &state.TotalCount, &state.LastError); err != nil {
		t.Fatalf("load final state: %v", err)
	}
	if state.Phase != GlobalRankPhaseStable || state.ActiveBucket != GlobalRankBucket1 || state.TargetBucket != nil {
		t.Fatalf("final state = %+v, want stable in bucket 1", state)
	}
	assertOrderedWorkspaceItemNumbers(t, tdb.DB, []int{1, 2, 3, 4, 5, 6})
	assertNoDuplicateFracIndexes(t, tdb.DB)

	var remainingInBucket0 int64
	if err := tdb.DB.QueryRow(
		`SELECT COUNT(*) FROM items WHERE frac_index >= ? AND frac_index < ?`, "0|", "1|",
	).Scan(&remainingInBucket0); err != nil {
		t.Fatalf("count unmigrated rows: %v", err)
	}
	if remainingInBucket0 != 0 {
		t.Fatalf("%d rows remain in bucket 0 after completion", remainingInBucket0)
	}
}
