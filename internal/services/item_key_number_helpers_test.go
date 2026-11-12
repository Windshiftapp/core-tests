//go:build test

package services

import (
	"testing"

	"windshift/internal/database"
)

func createKeyNumberItem(t *testing.T, db database.Database, workspaceID int, title string, parentID *int) int {
	t.Helper()
	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: workspaceID,
		Title:       title,
		ParentID:    parentID,
	})
	if err != nil {
		t.Fatalf("create item %q: %v", title, err)
	}
	return int(itemID)
}

func itemNumberByID(t *testing.T, db database.Database, itemID int) int {
	t.Helper()
	var number int
	if err := db.QueryRow(`SELECT workspace_item_number FROM items WHERE id = ?`, itemID).Scan(&number); err != nil {
		t.Fatalf("load item number for item %d: %v", itemID, err)
	}
	return number
}

func keyReservationCount(t *testing.T, db database.Database, workspaceID, number int) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM item_key_reservations
		WHERE workspace_id = ? AND workspace_item_number = ?
	`, workspaceID, number).Scan(&count); err != nil {
		t.Fatalf("count key reservations for %d-%d: %v", workspaceID, number, err)
	}
	return count
}
