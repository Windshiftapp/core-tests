//go:build test

package services

import (
	"testing"

	"windshift/internal/constants"
	"windshift/internal/itemevents"
	"windshift/internal/testutils"
)

// TestTodoistStoreUpdateTaskRecordsHistory asserts that Todoist-driven task
// updates — which bypass ItemUpdateService — still land in item history,
// attributed to the personal-workspace owner whose Todoist drove the change.
func TestTodoistStoreUpdateTaskRecordsHistory(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	// Personal tasks live in the owner's personal workspace; create it the
	// way production bootstraps one, then create the task through the
	// production creation service (same package) so numbering, bookkeeping,
	// and creation history match real behavior.
	var personalWorkspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, is_personal, owner_id)
		VALUES ('Todoist History Personal', 'TDHP', true, ?) RETURNING id
	`, data.UserID).Scan(&personalWorkspaceID); err != nil {
		t.Fatalf("insert personal workspace: %v", err)
	}
	creator := data.UserID
	openStatus := constants.StatusIDOpen
	itemID64, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
		WorkspaceID:      personalWorkspaceID,
		Title:            "Synced task",
		Description:      "Original description",
		StatusID:         &openStatus,
		CreatorID:        &creator,
		AssigneeID:       &creator,
		ValidatingUserID: creator,
		IsTask:           true,
		EventMetadata:    itemevents.User(creator, "application"),
	})
	if err != nil {
		t.Fatalf("insert task item: %v", err)
	}
	itemID := int(itemID64)

	store := &itemPersonalStore{db: tdb.GetDatabase()}
	err = store.UpdateTask(itemID, data.UserID, taskState{
		Title:       "Synced task",
		Description: "Updated by Todoist",
		Due:         "",
		Completed:   true,
	}, []string{"description", "completed"})
	if err != nil {
		t.Fatalf("update task: %v", err)
	}

	type row struct {
		fieldName string
		oldValue  string
		newValue  string
		userID    *int
	}
	var rows []row
	// Creation history rows also carry these field names with empty
	// old_value; only rows with a prior value are sync-driven updates.
	dbRows, err := tdb.Query(`
		SELECT field_name, COALESCE(old_value, ''), COALESCE(new_value, ''), user_id
		FROM item_history
		WHERE item_id = ? AND field_name IN ('description', 'status_id')
		  AND COALESCE(old_value, '') <> ''
		ORDER BY field_name
	`, itemID)
	if err != nil {
		t.Fatalf("read history rows: %v", err)
	}
	defer dbRows.Close()
	for dbRows.Next() {
		var r row
		if err := dbRows.Scan(&r.fieldName, &r.oldValue, &r.newValue, &r.userID); err != nil {
			t.Fatalf("scan history row: %v", err)
		}
		rows = append(rows, r)
	}
	if err := dbRows.Err(); err != nil {
		t.Fatalf("iterate history rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("history rows = %d, want 2 (description and status_id)", len(rows))
	}
	byField := map[string]row{}
	for _, r := range rows {
		byField[r.fieldName] = r
	}
	desc := byField["description"]
	if desc.oldValue != "Original description" || desc.newValue != "Updated by Todoist" {
		t.Fatalf("description history = %q -> %q", desc.oldValue, desc.newValue)
	}
	if desc.userID == nil || *desc.userID != data.UserID {
		t.Fatalf("description actor = %v, want %d", desc.userID, data.UserID)
	}
	status := byField["status_id"]
	if status.oldValue == "" || status.newValue == "" || status.oldValue == status.newValue {
		t.Fatalf("status history = %q -> %q, want distinct status IDs", status.oldValue, status.newValue)
	}
	if status.userID == nil || *status.userID != data.UserID {
		t.Fatalf("status actor = %v, want %d", status.userID, data.UserID)
	}
}
