//go:build test

package handlers

import (
	"net/http"
	"strconv"
	"testing"

	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

func TestItemWorkspaceMoveRequiresDestinationCreatePermission(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	seed := tdb.SeedTestData(t)

	var destinationID, itemTypeID int
	if err := tdb.QueryRow(`INSERT INTO workspaces (name, key, active) VALUES ('Destination', 'DST', true) RETURNING id`).Scan(&destinationID); err != nil {
		t.Fatalf("create destination workspace: %v", err)
	}
	var otherUserID, editorRoleID int
	if err := tdb.QueryRow(`INSERT INTO users (email, username, first_name, last_name, password_hash, is_active) VALUES ('other@example.com', 'other', 'Other', 'User', 'x', true) RETURNING id`).Scan(&otherUserID); err != nil {
		t.Fatalf("create destination editor: %v", err)
	}
	if err := tdb.QueryRow(`SELECT id FROM workspace_roles WHERE name = 'Editor'`).Scan(&editorRoleID); err != nil {
		t.Fatalf("load editor role: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO user_workspace_roles (user_id, workspace_id, role_id) VALUES (?, ?, ?)`, otherUserID, destinationID, editorRoleID); err != nil {
		t.Fatalf("restrict destination editor role: %v", err)
	}
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE hierarchy_level != -1 ORDER BY is_default DESC, id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("load item type: %v", err)
	}
	// Item through the production create path so the preview handler sees a
	// real work item (canonical rank, workflow defaults).
	f := factory.NewTestFactory(tdb.GetDatabase())
	itemID, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: seed.WorkspaceID,
		ItemTypeID:  &itemTypeID,
		Title:       "Move denied",
		StatusID:    &seed.StatusID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	perm, tracker, notifications := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), perm, tracker, notifications)
	req := testutils.CreateAuthenticatedJSONRequest(t, http.MethodPost, "/api/items/1/move-workspace/preview", services.ItemWorkspaceMoveInput{
		DestinationWorkspaceID: destinationID,
	}, nil)
	req.SetPathValue("id", strconv.Itoa(itemID))
	rr := testutils.ExecuteRequest(t, handler.PreviewWorkspaceMove, req)
	rr.AssertStatusCode(http.StatusNotFound)

	var workspaceID int
	if err := tdb.QueryRow(`SELECT workspace_id FROM items WHERE id = ?`, itemID).Scan(&workspaceID); err != nil || workspaceID != seed.WorkspaceID {
		t.Fatalf("denied preview mutated item workspace = %d, %v", workspaceID, err)
	}
}

func TestItemWorkspaceMovePreviewAndMoveHandlers(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	seed := tdb.SeedTestData(t)

	var destinationID, itemTypeID, adminRoleID int
	if err := tdb.QueryRow(`INSERT INTO workspaces (name, key, active) VALUES ('Destination', 'DST', true) RETURNING id`).Scan(&destinationID); err != nil {
		t.Fatalf("create destination workspace: %v", err)
	}
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE hierarchy_level != -1 ORDER BY is_default DESC, id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("load item type: %v", err)
	}
	if err := tdb.QueryRow(`SELECT id FROM workspace_roles WHERE name = 'Administrator'`).Scan(&adminRoleID); err != nil {
		t.Fatalf("load administrator role: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO user_workspace_roles (user_id, workspace_id, role_id) VALUES (?, ?, ?)`, seed.UserID, destinationID, adminRoleID); err != nil {
		t.Fatalf("grant destination role: %v", err)
	}
	// Item through the production create path; the key assertions below pin
	// the fixture's workspace item number.
	f := factory.NewTestFactory(tdb.GetDatabase())
	itemID, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: seed.WorkspaceID,
		ItemTypeID:  &itemTypeID,
		Title:       "Move allowed",
		StatusID:    &seed.StatusID,
		PriorityID:  &seed.PriorityID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if _, err := tdb.Exec(`UPDATE items SET workspace_item_number = 12 WHERE id = ?`, itemID); err != nil {
		t.Fatalf("pin fixture item number: %v", err)
	}

	perm, tracker, notifications := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), perm, tracker, notifications)
	previewReq := testutils.CreateAuthenticatedJSONRequest(t, http.MethodPost, "/api/items/1/move-workspace/preview", services.ItemWorkspaceMoveInput{
		DestinationWorkspaceID: destinationID,
	}, nil)
	previewReq.SetPathValue("id", strconv.Itoa(itemID))
	previewRR := testutils.ExecuteRequest(t, handler.PreviewWorkspaceMove, previewReq)
	previewRR.AssertStatusCode(http.StatusOK)
	var preview services.ItemWorkspaceMovePreview
	previewRR.AssertJSONResponse(&preview)

	moveReq := testutils.CreateAuthenticatedJSONRequest(t, http.MethodPost, "/api/items/1/move-workspace", services.ItemWorkspaceMoveInput{
		DestinationWorkspaceID: destinationID,
		TargetItemTypeID:       preview.TargetItemTypeID,
		TargetStatusID:         preview.TargetStatusID,
		TargetPriorityID:       preview.TargetPriorityID,
	}, nil)
	moveReq.SetPathValue("id", strconv.Itoa(itemID))
	moveRR := testutils.ExecuteRequest(t, handler.MoveWorkspace, moveReq)
	moveRR.AssertStatusCode(http.StatusOK)
	var result services.ItemWorkspaceMoveResult
	moveRR.AssertJSONResponse(&result)
	if result.Item == nil || result.Item.WorkspaceID != destinationID || result.OldKey != "TEST-12" || result.NewKey != "DST-1" {
		t.Fatalf("move response = %+v", result)
	}
}
