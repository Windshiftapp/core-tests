//go:build test

package repository_test

import (
	"testing"
	"time"

	"windshift/internal/repository"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

// seedRemapItems inserts two items with statusID in the seeded workspace.
func seedRemapItems(t *testing.T, tdb *testutils.TestDB, statusID int) (int, int) {
	t.Helper()
	data := tdb.SeedTestData(t)
	f := factory.NewTestFactory(tdb.GetDatabase())
	first, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: data.WorkspaceID,
		Title:       "Remap target one",
		StatusID:    &statusID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("insert first item: %v", err)
	}
	second, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: data.WorkspaceID,
		Title:       "Remap target two",
		StatusID:    &statusID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("insert second item: %v", err)
	}
	return first, second
}

func countRemapHistoryRows(t *testing.T, tdb *testutils.TestDB, itemID int, fieldName string, oldID, newID int) int {
	t.Helper()
	var count int
	if err := tdb.QueryRow(`
		SELECT COUNT(*) FROM item_history
		WHERE item_id = ? AND field_name = ?
		  AND old_value = ? AND new_value = ?
	`, itemID, fieldName, oldID, newID).Scan(&count); err != nil {
		t.Fatalf("count remap history rows for item %d: %v", itemID, err)
	}
	return count
}

func TestRemapFieldForWorkspacesTxAsUser_RecordsHistoryForEachItem(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	first, second := seedRemapItems(t, tdb, data.StatusID)

	var targetStatus int
	var categoryID int
	if err := tdb.QueryRow(`SELECT category_id FROM statuses WHERE id = ?`, data.StatusID).Scan(&categoryID); err != nil {
		t.Fatalf("read seeded status category: %v", err)
	}
	if err := tdb.QueryRow(`
		INSERT INTO statuses (name, category_id) VALUES (?, ?) RETURNING id
	`, "Remapped"+time.Now().Format("150405.000000000"), categoryID).Scan(&targetStatus); err != nil {
		t.Fatalf("insert target status: %v", err)
	}

	tx, err := tdb.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := repository.NewItemRepository(tdb.GetDatabase()).RemapFieldForWorkspacesTxAsUser(
		tx, "status_id", &data.StatusID, targetStatus, nil, []int{data.WorkspaceID}, time.Now(), data.UserID,
	)
	if err != nil {
		t.Fatalf("remap: %v", err)
	}
	if rows != 2 {
		t.Fatalf("remapped rows = %d, want 2", rows)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	for _, itemID := range []int{first, second} {
		if got := countRemapHistoryRows(t, tdb, itemID, "status_id", data.StatusID, targetStatus); got != 1 {
			t.Fatalf("item %d history rows = %d, want 1", itemID, got)
		}
		var userID int
		if err := tdb.QueryRow(`
			SELECT user_id FROM item_history
			WHERE item_id = ? AND field_name = 'status_id'
			  AND old_value = ? AND new_value = ?
		`, itemID, data.StatusID, targetStatus).Scan(&userID); err != nil {
			t.Fatalf("read remap history actor for item %d: %v", itemID, err)
		}
		if userID != data.UserID {
			t.Fatalf("item %d remap actor = %d, want %d", itemID, userID, data.UserID)
		}
	}
}

func TestRemapFieldForWorkspacesTx_RecordsSystemAttributedHistory(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	itemID, _ := seedRemapItems(t, tdb, data.StatusID)

	var targetPriority int
	if err := tdb.QueryRow(`
		INSERT INTO priorities (name) VALUES (?) RETURNING id
	`, "Remapped"+time.Now().Format("150405.000000000")).Scan(&targetPriority); err != nil {
		t.Fatalf("insert target priority: %v", err)
	}

	tx, err := tdb.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := repository.NewItemRepository(tdb.GetDatabase()).RemapFieldForWorkspacesTx(
		tx, "priority_id", &data.PriorityID, targetPriority, nil, []int{data.WorkspaceID}, time.Now(),
	)
	if err != nil {
		t.Fatalf("remap: %v", err)
	}
	if rows != 2 {
		t.Fatalf("remapped rows = %d, want 2", rows)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if got := countRemapHistoryRows(t, tdb, itemID, "priority_id", data.PriorityID, targetPriority); got != 1 {
		t.Fatalf("item %d history rows = %d, want 1", itemID, got)
	}
	var userID *int
	var actorKind string
	if err := tdb.QueryRow(`
		SELECT user_id, actor_kind FROM item_history
		WHERE item_id = ? AND field_name = 'priority_id'
		  AND old_value = ? AND new_value = ?
	`, itemID, data.PriorityID, targetPriority).Scan(&userID, &actorKind); err != nil {
		t.Fatalf("read remap history actor: %v", err)
	}
	if userID != nil {
		t.Fatalf("remap actor user_id = %d, want NULL for the system variant", *userID)
	}
	if actorKind != "system" {
		t.Fatalf("actor_kind = %q, want system", actorKind)
	}
}
