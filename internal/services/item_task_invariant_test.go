//go:build test

package services

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/constants"
	"windshift/internal/testutils"
	"windshift/internal/validation"
)

func TestCreateItemEnforcesPersonalTaskInvariant(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	seed := tdb.SeedTestData(t)

	otherUserID := taskInvariantInsertID(t, tdb, `
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('other-task-owner@example.test', 'other-task-owner', 'Other', 'Owner', true)
		RETURNING id
	`)
	ownWorkspaceID := taskInvariantInsertID(t, tdb, `
		INSERT INTO workspaces (name, key, active, is_personal, owner_id)
		VALUES ('Own tasks', 'OWN-TASKS', true, true, ?)
		RETURNING id
	`, seed.UserID)
	otherWorkspaceID := taskInvariantInsertID(t, tdb, `
		INSERT INTO workspaces (name, key, active, is_personal, owner_id)
		VALUES ('Other tasks', 'OTHER-TASKS', true, true, ?)
		RETURNING id
	`, otherUserID)
	invalidStatusID := taskInvariantQueryID(t, tdb, `
		SELECT id FROM statuses WHERE id NOT IN (?, ?) ORDER BY id LIMIT 1
	`, constants.StatusIDOpen, constants.StatusIDDone)
	defaultedID, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
		WorkspaceID:      ownWorkspaceID,
		Title:            "Task invariant: default status",
		IsTask:           true,
		CreatorID:        &seed.UserID,
		ValidatingUserID: seed.UserID,
	})
	if err != nil {
		t.Fatalf("create task with default status: %v", err)
	}
	var defaultedStatusID int
	if err := tdb.QueryRow(`SELECT status_id FROM items WHERE id = ?`, defaultedID).Scan(&defaultedStatusID); err != nil {
		t.Fatalf("load default task status: %v", err)
	}
	if defaultedStatusID != constants.StatusIDOpen {
		t.Fatalf("default task status = %d, want Open (%d)", defaultedStatusID, constants.StatusIDOpen)
	}

	tests := []struct {
		name        string
		workspaceID int
		statusID    int
		actorUserID int
		wantError   bool
	}{
		{name: "own personal workspace open", workspaceID: ownWorkspaceID, statusID: constants.StatusIDOpen, actorUserID: seed.UserID},
		{name: "own personal workspace done", workspaceID: ownWorkspaceID, statusID: constants.StatusIDDone, actorUserID: seed.UserID},
		{name: "shared workspace", workspaceID: seed.WorkspaceID, statusID: constants.StatusIDOpen, actorUserID: seed.UserID, wantError: true},
		{name: "another users personal workspace", workspaceID: otherWorkspaceID, statusID: constants.StatusIDOpen, actorUserID: seed.UserID, wantError: true},
		{name: "invalid personal task status", workspaceID: ownWorkspaceID, statusID: invalidStatusID, actorUserID: seed.UserID, wantError: true},
		{name: "missing actor", workspaceID: ownWorkspaceID, statusID: constants.StatusIDOpen, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title := "Task invariant: " + tt.name
			params := ItemCreationParams{
				WorkspaceID:      tt.workspaceID,
				Title:            title,
				StatusID:         &tt.statusID,
				IsTask:           true,
				ValidatingUserID: tt.actorUserID,
			}
			if tt.actorUserID > 0 {
				params.CreatorID = &tt.actorUserID
			}
			createdID, err := CreateItem(tdb.GetDatabase(), params)
			if !tt.wantError {
				if err != nil {
					t.Fatalf("CreateItem: %v", err)
				}
				var workspaceID, statusID int
				var isTask bool
				if err := tdb.QueryRow(`SELECT workspace_id, status_id, is_task FROM items WHERE id = ?`, createdID).Scan(&workspaceID, &statusID, &isTask); err != nil {
					t.Fatalf("load created task: %v", err)
				}
				if workspaceID != tt.workspaceID || statusID != tt.statusID || !isTask {
					t.Fatalf("created task = workspace:%d status:%d is_task:%v", workspaceID, statusID, isTask)
				}
				return
			}

			var validationErr *validation.ValidationError
			if !errors.As(err, &validationErr) || validationErr.Field != "is_task" {
				t.Fatalf("CreateItem error = %v, want is_task ValidationError", err)
			}
			var count int
			if err := tdb.QueryRow(`SELECT COUNT(*) FROM items WHERE title = ?`, title).Scan(&count); err != nil {
				t.Fatalf("count persisted items: %v", err)
			}
			if count != 0 {
				t.Fatalf("persisted invalid tasks = %d, want 0", count)
			}
		})
	}
}

func TestItemUpdatePersistsAndValidatesTaskFlag(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	seed := tdb.SeedTestData(t)
	ownWorkspaceID := taskInvariantInsertID(t, tdb, `
		INSERT INTO workspaces (name, key, active, is_personal, owner_id)
		VALUES ('Update tasks', 'UPDATE-TASKS', true, true, ?)
		RETURNING id
	`, seed.UserID)

	itemID, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
		WorkspaceID: ownWorkspaceID,
		Title:       "Become a task",
		StatusID:    taskInvariantIntPointer(constants.StatusIDOpen),
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if _, err := NewItemUpdateService(tdb.GetDatabase()).UpdateItem(UpdateItemRequest{
		ItemID: int(itemID), UpdateData: map[string]any{"is_task": true}, UserID: seed.UserID,
	}); err != nil {
		t.Fatalf("set is_task: %v", err)
	}
	var isTask bool
	if err := tdb.QueryRow(`SELECT is_task FROM items WHERE id = ?`, itemID).Scan(&isTask); err != nil {
		t.Fatalf("load updated item: %v", err)
	}
	if !isTask {
		t.Fatal("persisted is_task = false, want true")
	}

	sharedItemID, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
		WorkspaceID: seed.WorkspaceID,
		Title:       "Shared item",
	})
	if err != nil {
		t.Fatalf("create shared item: %v", err)
	}
	_, err = NewItemUpdateService(tdb.GetDatabase()).UpdateItem(UpdateItemRequest{
		ItemID: int(sharedItemID), UpdateData: map[string]any{"is_task": true}, UserID: seed.UserID,
	})
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) || validationErr.Field != "is_task" {
		t.Fatalf("shared update error = %v, want is_task ValidationError", err)
	}
}

func TestBulkUpdateRejectsTaskFlagOutsidePersonalWorkspace(t *testing.T) {
	fixture := seedBulkFixture(t, 2)
	_, err := NewItemUpdateService(fixture.db).BulkUpdateItems(context.Background(), BulkUpdateItemsRequest{
		ItemIDs:            fixture.incompleteIDs,
		Fields:             map[string]any{"is_task": true},
		UserID:             fixture.userID,
		AuthorizeWorkspace: func(int) (bool, error) { return true, nil },
	})
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) || validationErr.Field != "is_task" {
		t.Fatalf("bulk update error = %v, want is_task ValidationError", err)
	}
	var count int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM items WHERE id IN (?, ?) AND is_task = true`, fixture.incompleteIDs[0], fixture.incompleteIDs[1]).Scan(&count); err != nil {
		t.Fatalf("count task items: %v", err)
	}
	if count != 0 {
		t.Fatalf("bulk update persisted %d tasks, want 0", count)
	}
}

func TestWorkspaceMoveRejectsTaskOutsideOwnersPersonalWorkspace(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	ownWorkspaceID := taskInvariantInsertID(t, tdb, `
		INSERT INTO workspaces (name, key, active, is_personal, owner_id)
		VALUES ('Move task source', 'MOVE-TASK-SRC', true, true, ?)
		RETURNING id
	`, seed.UserID)
	destinationWorkspaceID := taskInvariantInsertID(t, tdb, `
		INSERT INTO workspaces (name, key, active, is_personal)
		VALUES ('Move task destination', 'MOVE-TASK-DST', true, false)
		RETURNING id
	`)
	configurationSetID := taskInvariantQueryID(t, tdb, `SELECT id FROM configuration_sets WHERE is_default = true ORDER BY id LIMIT 1`)
	itemTypeID := taskInvariantQueryID(t, tdb, `SELECT item_type_id FROM configuration_set_item_types WHERE configuration_set_id = ? ORDER BY id LIMIT 1`, configurationSetID)
	if _, err := tdb.Exec(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, destinationWorkspaceID, configurationSetID); err != nil {
		t.Fatalf("attach destination configuration: %v", err)
	}

	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID:      ownWorkspaceID,
		ItemTypeID:       &itemTypeID,
		Title:            "Do not move",
		StatusID:         taskInvariantIntPointer(constants.StatusIDOpen),
		IsTask:           true,
		CreatorID:        &seed.UserID,
		ValidatingUserID: seed.UserID,
	})
	if err != nil {
		t.Fatalf("create personal task: %v", err)
	}

	moveService := NewItemWorkspaceMoveService(db)
	moveInput := ItemWorkspaceMoveInput{
		DestinationWorkspaceID: destinationWorkspaceID,
		TargetItemTypeID:       itemTypeID,
		TargetStatusID:         constants.StatusIDOpen,
	}
	_, err = moveService.Preview(int(itemID), seed.UserID, moveInput)
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) || validationErr.Field != "is_task" {
		t.Fatalf("preview error = %v, want is_task ValidationError", err)
	}

	_, err = moveService.Move(int(itemID), seed.UserID, moveInput)
	validationErr = nil
	if !errors.As(err, &validationErr) || validationErr.Field != "is_task" {
		t.Fatalf("move error = %v, want is_task ValidationError", err)
	}
	var workspaceID int
	if err := tdb.QueryRow(`SELECT workspace_id FROM items WHERE id = ?`, itemID).Scan(&workspaceID); err != nil {
		t.Fatalf("load task workspace: %v", err)
	}
	if workspaceID != ownWorkspaceID {
		t.Fatalf("task workspace = %d, want %d", workspaceID, ownWorkspaceID)
	}
}

func TestItemCopyRevalidatesTaskInvariant(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	sharedItemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: seed.WorkspaceID,
		Title:       "Corrupt shared task source",
		StatusID:    taskInvariantIntPointer(constants.StatusIDOpen),
	})
	if err != nil {
		t.Fatalf("create source item: %v", err)
	}
	if _, err := tdb.Exec(`UPDATE items SET is_task = true WHERE id = ?`, sharedItemID); err != nil {
		t.Fatalf("install invalid legacy state: %v", err)
	}

	_, err = NewItemCRUDService(db).Copy(int(sharedItemID), CopyOptions{
		NewTitle:  "Rejected shared task copy",
		CreatorID: seed.UserID,
	})
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) || validationErr.Field != "is_task" {
		t.Fatalf("copy error = %v, want is_task ValidationError", err)
	}
	var count int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM items WHERE title = 'Rejected shared task copy'`).Scan(&count); err != nil {
		t.Fatalf("count copied items: %v", err)
	}
	if count != 0 {
		t.Fatalf("copied invalid tasks = %d, want 0", count)
	}
}

func taskInvariantInsertID(t *testing.T, tdb *testutils.TestDB, query string, args ...any) int {
	t.Helper()
	var id int
	if err := tdb.QueryRow(query, args...).Scan(&id); err != nil {
		t.Fatalf("fixture insert: %v\nquery: %s", err, query)
	}
	return id
}

func taskInvariantQueryID(t *testing.T, tdb *testutils.TestDB, query string, args ...any) int {
	t.Helper()
	var id int
	if err := tdb.QueryRow(query, args...).Scan(&id); err != nil {
		t.Fatalf("fixture query: %v\nquery: %s", err, query)
	}
	return id
}

func taskInvariantIntPointer(value int) *int { return &value }

// TestItemCopyAllowsValidPersonalTask is the WI-1719 regression: an owner
// copying their own personal task must succeed. Copy used to revalidate with a
// zero user id, so every is_task copy was rejected even when the actor owned
// the workspace.
func TestItemCopyAllowsValidPersonalTask(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	ownWorkspaceID := taskInvariantInsertID(t, tdb, `
		INSERT INTO workspaces (name, key, active, is_personal, owner_id)
		VALUES ('Copy tasks', 'COPY-TASKS', true, true, ?)
		RETURNING id
	`, seed.UserID)

	sourceID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID:      ownWorkspaceID,
		Title:            "Personal task source",
		StatusID:         taskInvariantIntPointer(constants.StatusIDOpen),
		IsTask:           true,
		CreatorID:        &seed.UserID,
		ValidatingUserID: seed.UserID,
	})
	if err != nil {
		t.Fatalf("create personal task: %v", err)
	}

	result, err := NewItemCRUDService(db).Copy(int(sourceID), CopyOptions{
		NewTitle:  "Personal task copy",
		CreatorID: seed.UserID,
	})
	if err != nil {
		t.Fatalf("copy personal task: %v", err)
	}

	var workspaceID, statusID, creatorID int
	var isTask bool
	var title string
	if err := db.QueryRow(`
		SELECT workspace_id, status_id, creator_id, is_task, title FROM items WHERE id = ?
	`, result.NewItemID).Scan(&workspaceID, &statusID, &creatorID, &isTask, &title); err != nil {
		t.Fatalf("load copied task: %v", err)
	}
	if workspaceID != ownWorkspaceID || statusID != constants.StatusIDOpen || creatorID != seed.UserID || !isTask || title != "Personal task copy" {
		t.Fatalf("copied task = ws:%d status:%d creator:%d is_task:%t title:%q",
			workspaceID, statusID, creatorID, isTask, title)
	}

	var historyCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_history WHERE item_id = ? AND field_name = 'title'`, result.NewItemID).Scan(&historyCount); err != nil {
		t.Fatalf("count copy creation history: %v", err)
	}
	if historyCount != 1 {
		t.Fatalf("copy creation history = %d, want 1", historyCount)
	}

	// A non-owner still cannot copy the task out of the owner's workspace.
	otherUserID := taskInvariantInsertID(t, tdb, `
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('copy-other@example.test', 'copy-other', 'Copy', 'Other', true)
		RETURNING id
	`)
	if _, err := NewItemCRUDService(db).Copy(int(sourceID), CopyOptions{
		NewTitle:  "Non-owner copy",
		CreatorID: otherUserID,
	}); err == nil {
		t.Fatal("non-owner copied a personal task")
	}
}
