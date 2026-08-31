package services

import (
	"path/filepath"
	"testing"

	"windshift/internal/database"
)

func newItemLinkBatchTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "item-link-batch.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return db
}

func insertItemLinkBatchRow(t *testing.T, db database.Database, query string, args ...any) int {
	t.Helper()
	result, err := db.ExecWrite(query, args...)
	if err != nil {
		t.Fatalf("insert fixture row: %v\nquery: %s", err, query)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	return int(id)
}

// insertBatchItem creates an item through the production CreateItem path so
// fixture rows carry a canonical rank, workflow defaults, and history.
func insertBatchItem(t *testing.T, db database.Database, workspaceID int, title string) int {
	t.Helper()
	id, err := CreateItem(db, ItemCreationParams{WorkspaceID: workspaceID, Title: title})
	if err != nil {
		t.Fatalf("create fixture item %q: %v", title, err)
	}
	return int(id)
}

func TestListLinksForItemsDefaultExcludesIncomingCustomFieldLinks(t *testing.T) {
	db := newItemLinkBatchTestDB(t)
	workspaceID := insertItemLinkBatchRow(t, db,
		`INSERT INTO workspaces (name, key, description, active) VALUES ('Link batch', 'LB', '', true)`,
	)
	standardSourceID := insertBatchItem(t, db, workspaceID, "Standard source")
	customSourceID := insertBatchItem(t, db, workspaceID, "Custom source")
	targetID := insertBatchItem(t, db, workspaceID, "Target")
	customFieldID := insertItemLinkBatchRow(t, db,
		`INSERT INTO custom_field_definitions (name, field_type) VALUES ('Linked requirement', 'linking')`,
	)

	var linkTypeID int
	if err := db.QueryRow(`SELECT id FROM link_types ORDER BY id LIMIT 1`).Scan(&linkTypeID); err != nil {
		t.Fatalf("load seeded link type: %v", err)
	}
	standardLinkID := insertItemLinkBatchRow(t, db, `
		INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id)
		VALUES (?, 'item', ?, 'item', ?)
	`, linkTypeID, standardSourceID, targetID)
	customLinkID := insertItemLinkBatchRow(t, db, `
		INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id, custom_field_id)
		VALUES (?, 'item', ?, 'item', ?, ?)
	`, linkTypeID, customSourceID, targetID, customFieldID)

	permissions := &countingWorkspacePermissions{
		allowed: map[int]bool{workspaceID: true},
		calls:   map[int]int{},
	}
	service := NewItemLinkService(db).WithPermissionService(permissions)

	defaultLinks, err := service.ListLinksForItemsWithChecks(1, []int{targetID})
	if err != nil {
		t.Fatalf("ListLinksForItemsWithChecks: %v", err)
	}
	if got := defaultLinks[targetID].Incoming; len(got) != 1 || got[0].ID != standardLinkID {
		t.Fatalf("default incoming links = %+v, want only standard link %d", got, standardLinkID)
	}

	allLinks, err := service.ListLinksForItemsWithCustomFieldsAndChecks(1, []int{targetID})
	if err != nil {
		t.Fatalf("ListLinksForItemsWithCustomFieldsAndChecks: %v", err)
	}
	gotIDs := map[int]bool{}
	for _, link := range allLinks[targetID].Incoming {
		gotIDs[link.ID] = true
	}
	if len(gotIDs) != 2 || !gotIDs[standardLinkID] || !gotIDs[customLinkID] {
		t.Fatalf("custom-field incoming link ids = %#v, want %d and %d", gotIDs, standardLinkID, customLinkID)
	}
}
