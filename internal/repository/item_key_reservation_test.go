//go:build test

package repository

import (
	"testing"
	"time"

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
