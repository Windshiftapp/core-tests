//go:build test

package repository

import (
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

func TestNextWorkspaceItemNumberSkipsMovedKeyReservation(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	if _, err := tdb.ExecWrite(`
		INSERT INTO item_key_reservations (
			workspace_id, workspace_item_number, moved_item_id,
			destination_workspace_id, destination_workspace_item_number,
			moved_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, data.WorkspaceID, 50, nil, data.WorkspaceID, 51, data.UserID, time.Now()); err != nil {
		t.Fatalf("reserve moved key: %v", err)
	}

	tx, err := tdb.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	next, err := NewItemRepository(tdb.GetDatabase()).GetNextWorkspaceItemNumber(tx, data.WorkspaceID)
	if err != nil {
		t.Fatalf("get next item number: %v", err)
	}
	if next != 51 {
		t.Fatalf("next item number = %d, want 51 so moved key 50 cannot be reused", next)
	}
}

// Re-deleting or racing two reservations for the same key must not error; the
// reservation is already retired, so the second insert is a no-op.
func TestReserveDeletedItemNumbersIsIdempotent(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	repo := NewItemRepository(tdb.GetDatabase())
	item := &models.Item{ID: 42, WorkspaceID: data.WorkspaceID, WorkspaceItemNumber: 7}

	if err := database.WithTx(tdb.GetDatabase(), func(tx database.Tx) error {
		if err := repo.ReserveDeletedItemNumbers(tx, []*models.Item{item}, 0); err != nil {
			return err
		}
		return repo.ReserveDeletedItemNumbers(tx, []*models.Item{item}, 0)
	}); err != nil {
		t.Fatalf("reserve deleted item numbers twice: %v", err)
	}

	var count int
	if err := tdb.QueryRow(`
		SELECT COUNT(*) FROM item_key_reservations
		WHERE workspace_id = ? AND workspace_item_number = 7
	`, data.WorkspaceID).Scan(&count); err != nil {
		t.Fatalf("count reservations: %v", err)
	}
	if count != 1 {
		t.Fatalf("reservation rows = %d, want 1", count)
	}
}
