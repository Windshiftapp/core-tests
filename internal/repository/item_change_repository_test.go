//go:build test

package repository

import (
	"runtime"
	"testing"
	"time"

	"windshift/internal/testutils"
)

func TestStableCurrentWatermarkWaitsForEarlierPostgresWriter(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()
	if db.GetDriverName() != "postgres" {
		t.Skip("PostgreSQL sequence commit ordering regression")
	}

	writer, err := db.Begin()
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}
	defer func() { _ = writer.Rollback() }()
	var changeID int64
	if err := writer.QueryRow(
		"INSERT INTO item_change_log (item_id, workspace_id, change_type) VALUES (?, ?, ?) RETURNING id",
		7,
		3,
		"upsert",
	).Scan(&changeID); err != nil {
		t.Fatalf("insert uncommitted change: %v", err)
	}

	type result struct {
		watermark int64
		err       error
	}
	resultCh := make(chan result, 1)
	go func() {
		watermark, watermarkErr := NewItemChangeRepository(db).StableCurrentWatermark([]int{3}, 3)
		resultCh <- result{watermark: watermark, err: watermarkErr}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err := db.QueryRow(`
			SELECT EXISTS (
				SELECT 1
				FROM pg_locks l
				JOIN pg_class c ON c.oid = l.relation
				WHERE c.relname = 'item_change_log'
				  AND l.mode = 'ShareLock'
				  AND NOT l.granted
			)
		`).Scan(&waiting)
		if err != nil {
			t.Fatalf("inspect watermark lock: %v", err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stable watermark did not wait for the uncommitted writer")
		}
		runtime.Gosched()
	}

	if err := writer.Commit(); err != nil {
		t.Fatalf("commit writer: %v", err)
	}
	select {
	case got := <-resultCh:
		if got.err != nil {
			t.Fatalf("stable watermark: %v", got.err)
		}
		if got.watermark != changeID {
			t.Fatalf("watermark = %d, want committed change id %d", got.watermark, changeID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stable watermark remained blocked after writer committed")
	}
}
