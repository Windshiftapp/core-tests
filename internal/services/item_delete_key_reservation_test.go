//go:build test

package services

import (
	"testing"

	"windshift/internal/testutils"
)

func TestDeleteSingleRetiresHighestItemNumber(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	firstID := createKeyNumberItem(t, db, data.WorkspaceID, "First", nil)
	secondID := createKeyNumberItem(t, db, data.WorkspaceID, "Second", nil)
	if got := itemNumberByID(t, db, secondID); got != 2 {
		t.Fatalf("second item number = %d, want 2", got)
	}

	if err := NewItemCRUDService(db).DeleteSingle(secondID); err != nil {
		t.Fatalf("delete highest item: %v", err)
	}
	if got := keyReservationCount(t, db, data.WorkspaceID, 2); got != 1 {
		t.Fatalf("reservation rows for 2 = %d, want 1", got)
	}

	nextID := createKeyNumberItem(t, db, data.WorkspaceID, "Third", nil)
	if got := itemNumberByID(t, db, nextID); got != 3 {
		t.Fatalf("next item number = %d, want 3 (retired key not reissued)", got)
	}
	if got := itemNumberByID(t, db, firstID); got != 1 {
		t.Fatalf("surviving item number = %d, want unchanged 1", got)
	}
}

func TestDeleteNonMaxItemDoesNotChangeNextNumber(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	firstID := createKeyNumberItem(t, db, data.WorkspaceID, "First", nil)
	createKeyNumberItem(t, db, data.WorkspaceID, "Second", nil)

	if err := NewItemCRUDService(db).DeleteSingle(firstID); err != nil {
		t.Fatalf("delete non-max item: %v", err)
	}
	if got := keyReservationCount(t, db, data.WorkspaceID, 1); got != 1 {
		t.Fatalf("reservation rows for 1 = %d, want 1", got)
	}

	nextID := createKeyNumberItem(t, db, data.WorkspaceID, "Third", nil)
	if got := itemNumberByID(t, db, nextID); got != 3 {
		t.Fatalf("next item number = %d, want 3 (interior gap stays unfilled)", got)
	}
}

func TestDeleteCascadeRetiresHighestDescendantNumber(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	var parentTypeID, childTypeID int
	if err := db.QueryRow(`
		SELECT id FROM item_types
		WHERE hierarchy_level >= 0
		ORDER BY hierarchy_level, id LIMIT 1
	`).Scan(&parentTypeID); err != nil {
		t.Fatalf("load parent item type: %v", err)
	}
	if err := db.QueryRow(`
		SELECT id FROM item_types
		WHERE hierarchy_level > (SELECT hierarchy_level FROM item_types WHERE id = ?)
		ORDER BY hierarchy_level, id LIMIT 1
	`, parentTypeID).Scan(&childTypeID); err != nil {
		t.Fatalf("load child item type: %v", err)
	}

	parentID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Cascade parent",
		ItemTypeID:  &parentTypeID,
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	parentID := int(parentID64)
	childID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Cascade child",
		ItemTypeID:  &childTypeID,
		ParentID:    &parentID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if got := itemNumberByID(t, db, int(childID64)); got != 2 {
		t.Fatalf("child item number = %d, want 2", got)
	}

	if _, err := NewItemCRUDService(db).Delete(parentID); err != nil {
		t.Fatalf("cascade delete: %v", err)
	}
	if got := keyReservationCount(t, db, data.WorkspaceID, 2); got != 1 {
		t.Fatalf("reservation rows for descendant number 2 = %d, want 1", got)
	}

	nextID := createKeyNumberItem(t, db, data.WorkspaceID, "After cascade", nil)
	if got := itemNumberByID(t, db, nextID); got != 3 {
		t.Fatalf("next item number = %d, want 3 (descendant key retired)", got)
	}
}

func TestBulkDeleteRetiresHighestItemNumber(t *testing.T) {
	env := newBulkTransitionDeleteEnv(t)
	actor := AuditActor{UserID: env.adminID, Username: "bulk-admin"}

	env.createItem(t, "Bulk first", env.openStatus)
	second := env.createItem(t, "Bulk second", env.openStatus)

	result, err := env.app.BulkDelete(actor, []int{second})
	if err != nil {
		t.Fatalf("bulk delete: %v", err)
	}
	if result.DeletedCount != 1 {
		t.Fatalf("bulk deleted count = %d, want 1", result.DeletedCount)
	}
	if got := keyReservationCount(t, env.db, env.workspace, 2); got != 1 {
		t.Fatalf("reservation rows for 2 = %d, want 1", got)
	}

	nextID := env.createItem(t, "Bulk third", env.openStatus)
	if got := itemNumberByID(t, env.db, nextID); got != 3 {
		t.Fatalf("next item number = %d, want 3 (bulk delete retired the key)", got)
	}
}
