//go:build test

package services_test

import (
	"fmt"
	"testing"

	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

func TestItemCRUDService_GetBacklogItemsAppliesSubFilter(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()
	f := factory.NewTestFactory(db)

	assigneeID, workspaceID, err := f.CreateUserAndWorkspace()
	if err != nil {
		t.Fatalf("create user/workspace: %v", err)
	}

	var itemTypeID, statusID int
	if err := db.QueryRow("SELECT id FROM item_types LIMIT 1").Scan(&itemTypeID); err != nil {
		t.Fatalf("get item type: %v", err)
	}
	if err := db.QueryRow(`
		SELECT s.id
		FROM statuses s
		JOIN status_categories sc ON s.category_id = sc.id
		WHERE COALESCE(sc.is_completed, FALSE) = FALSE
		LIMIT 1
	`).Scan(&statusID); err != nil {
		t.Fatalf("get backlog status: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO board_configurations (workspace_id, backlog_status_ids) VALUES (?, ?)",
		workspaceID,
		fmt.Sprintf("[%d]", statusID),
	); err != nil {
		t.Fatalf("create backlog config: %v", err)
	}

	assignedID, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: workspaceID,
		Title:       "Assigned backlog item",
		StatusID:    &statusID,
		ItemTypeID:  &itemTypeID,
		AssigneeID:  &assigneeID,
		CreatorID:   &assigneeID,
	})
	if err != nil {
		t.Fatalf("create assigned item: %v", err)
	}
	if _, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: workspaceID,
		Title:       "Unassigned backlog item",
		StatusID:    &statusID,
		ItemTypeID:  &itemTypeID,
		CreatorID:   &assigneeID,
	}); err != nil {
		t.Fatalf("create unassigned item: %v", err)
	}

	items, total, err := services.NewItemCRUDService(db).GetBacklogItems(services.BacklogParams{
		WorkspaceID:  workspaceID,
		SubQLQuery:   fmt.Sprintf("assignee = %d", assigneeID),
		WorkspaceIDs: []int{workspaceID},
		UserID:       assigneeID,
		Pagination:   services.PaginationParams{Limit: 50},
	})
	if err != nil {
		t.Fatalf("get backlog: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("filtered backlog returned total=%d len=%d, want exactly one", total, len(items))
	}
	if items[0].ID != assignedID {
		t.Fatalf("filtered backlog returned item %d, want assigned item %d", items[0].ID, assignedID)
	}
	if items[0].AssigneeID == nil || *items[0].AssigneeID != assigneeID {
		t.Fatalf("filtered backlog returned item with assignee %v, want %d", items[0].AssigneeID, assigneeID)
	}
}
