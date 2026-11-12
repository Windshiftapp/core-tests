//go:build test

package services_test

import (
	"fmt"
	"testing"

	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

// A collection backlog must honor the collection's own board configuration.
// The frontend sends only collection_id for these requests, so the scope has to
// be resolved from the collection rather than from a workspace parameter.
func TestItemCRUDService_GetBacklogItemsUsesCollectionScopedStatuses(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()
	f := factory.NewTestFactory(db)

	creatorID, workspaceID, err := f.CreateUserAndWorkspace()
	if err != nil {
		t.Fatalf("create user/workspace: %v", err)
	}

	var itemTypeID, openStatusID, doneStatusID int
	if err := db.QueryRow("SELECT id FROM item_types LIMIT 1").Scan(&itemTypeID); err != nil {
		t.Fatalf("get item type: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM statuses WHERE name = 'Open'`).Scan(&openStatusID); err != nil {
		t.Fatalf("get Open status: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM statuses WHERE name = 'Done'`).Scan(&doneStatusID); err != nil {
		t.Fatalf("get Done status: %v", err)
	}

	collectionResult, err := db.ExecWrite(
		`INSERT INTO collections (name, ql_query, workspace_id, created_by) VALUES (?, '', ?, ?)`,
		"Backlog scope",
		workspaceID,
		creatorID,
	)
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	collectionID64, err := collectionResult.LastInsertId()
	if err != nil {
		t.Fatalf("collection LastInsertId: %v", err)
	}
	collectionID := int(collectionID64)

	if _, err := db.ExecWrite(
		"INSERT INTO board_configurations (collection_id, backlog_status_ids) VALUES (?, ?)",
		collectionID,
		fmt.Sprintf("[%d, %d]", openStatusID, doneStatusID),
	); err != nil {
		t.Fatalf("create collection backlog config: %v", err)
	}

	openItemID, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: workspaceID,
		Title:       "Open backlog item",
		StatusID:    &openStatusID,
		ItemTypeID:  &itemTypeID,
		CreatorID:   &creatorID,
	})
	if err != nil {
		t.Fatalf("create open item: %v", err)
	}
	doneItemID, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: workspaceID,
		Title:       "Done backlog item",
		StatusID:    &doneStatusID,
		ItemTypeID:  &itemTypeID,
		CreatorID:   &creatorID,
	})
	if err != nil {
		t.Fatalf("create done item: %v", err)
	}

	items, total, err := services.NewItemCRUDService(db).GetBacklogItems(services.BacklogParams{
		CollectionID: collectionID,
		WorkspaceIDs: []int{workspaceID},
		UserID:       creatorID,
		Pagination:   services.PaginationParams{Limit: 50},
	})
	if err != nil {
		t.Fatalf("get backlog: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("collection backlog returned total=%d len=%d, want 2/2", total, len(items))
	}
	returned := map[int]bool{}
	for _, item := range items {
		returned[item.ID] = true
	}
	if !returned[openItemID] || !returned[doneItemID] {
		t.Fatalf("collection backlog returned ids %v, want open=%d and done=%d", returned, openItemID, doneItemID)
	}
}

// A collection without its own board configuration uses the global
// non-completed statuses, matching the defaults the board configuration editor
// shows for that scope. A workspace-scoped configuration must not leak into it.
func TestItemCRUDService_GetBacklogItemsFallsBackForUnconfiguredCollection(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()
	f := factory.NewTestFactory(db)

	creatorID, workspaceID, err := f.CreateUserAndWorkspace()
	if err != nil {
		t.Fatalf("create user/workspace: %v", err)
	}

	var itemTypeID, openStatusID, doneStatusID int
	if err := db.QueryRow("SELECT id FROM item_types LIMIT 1").Scan(&itemTypeID); err != nil {
		t.Fatalf("get item type: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM statuses WHERE name = 'Open'`).Scan(&openStatusID); err != nil {
		t.Fatalf("get Open status: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM statuses WHERE name = 'Done'`).Scan(&doneStatusID); err != nil {
		t.Fatalf("get Done status: %v", err)
	}

	// Workspace default includes Done; the collection has no configuration.
	if _, err := db.ExecWrite(
		"INSERT INTO board_configurations (workspace_id, backlog_status_ids) VALUES (?, ?)",
		workspaceID,
		fmt.Sprintf("[%d, %d]", openStatusID, doneStatusID),
	); err != nil {
		t.Fatalf("create workspace backlog config: %v", err)
	}

	collectionResult, err := db.ExecWrite(
		`INSERT INTO collections (name, ql_query, workspace_id, created_by) VALUES (?, '', ?, ?)`,
		"Unconfigured backlog scope",
		workspaceID,
		creatorID,
	)
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	collectionID64, err := collectionResult.LastInsertId()
	if err != nil {
		t.Fatalf("collection LastInsertId: %v", err)
	}
	collectionID := int(collectionID64)

	openItemID, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: workspaceID,
		Title:       "Open backlog item",
		StatusID:    &openStatusID,
		ItemTypeID:  &itemTypeID,
		CreatorID:   &creatorID,
	})
	if err != nil {
		t.Fatalf("create open item: %v", err)
	}
	if _, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: workspaceID,
		Title:       "Done backlog item",
		StatusID:    &doneStatusID,
		ItemTypeID:  &itemTypeID,
		CreatorID:   &creatorID,
	}); err != nil {
		t.Fatalf("create done item: %v", err)
	}

	items, total, err := services.NewItemCRUDService(db).GetBacklogItems(services.BacklogParams{
		CollectionID: collectionID,
		WorkspaceIDs: []int{workspaceID},
		UserID:       creatorID,
		Pagination:   services.PaginationParams{Limit: 50},
	})
	if err != nil {
		t.Fatalf("get backlog: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].ID != openItemID {
		t.Fatalf("unconfigured collection backlog returned total=%d items=%v, want only open item %d", total, items, openItemID)
	}
}
