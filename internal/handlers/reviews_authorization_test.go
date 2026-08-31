//go:build test

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func TestGetCompletedItemsExcludesWorkspacesWithoutItemView(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	setup := newServiceSetup(t, tdb)
	viewerID := setup.CreateUser("review-viewer@example.test", "review-viewer", "Review", "Viewer")
	otherID := setup.CreateUser("review-other@example.test", "review-other", "Review", "Other")
	visibleWorkspaceID := setup.CreateWorkspace("Visible Review Workspace", "VRW")
	hiddenWorkspaceID := setup.CreateWorkspace("Hidden Review Workspace", "HRW")
	grantViewerRoleForAuthorizationTest(t, tdb, viewerID, visibleWorkspaceID)
	grantViewerRoleForAuthorizationTest(t, tdb, otherID, hiddenWorkspaceID)

	categoryID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO status_categories (name, color, is_completed)
		VALUES ('Review Complete Category', '#22c55e', true)
	`)
	statusID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO statuses (name, category_id)
		VALUES ('Review Complete Status', ?)
	`, categoryID)
	visibleItemID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO items
			(workspace_id, workspace_item_number, title, description, status_id, assignee_id, creator_id, frac_index)
		VALUES (?, 1, 'Visible completed item', 'Visible details', ?, ?, ?, ?)
	`, visibleWorkspaceID, statusID, viewerID, viewerID, testutils.NextTestFracIndex())
	hiddenItemID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO items
			(workspace_id, workspace_item_number, title, description, status_id, assignee_id, creator_id, frac_index)
		VALUES (?, 1, 'Restricted completed item', 'Restricted details', ?, ?, ?, ?)
	`, hiddenWorkspaceID, statusID, viewerID, otherID, testutils.NextTestFracIndex())
	for _, itemID := range []int{visibleItemID, hiddenItemID} {
		if _, err := tdb.ExecWrite(`
			INSERT INTO item_history (item_id, user_id, changed_at, field_name, old_value, new_value)
			VALUES (?, ?, '2026-08-25 12:00:00', 'status_id', NULL, ?)
		`, itemID, viewerID, statusID); err != nil {
			t.Fatalf("insert completion history for item %d: %v", itemID, err)
		}
	}

	permissionService, err := services.NewPermissionService(tdb.GetDatabase(), services.PermissionCacheConfig{
		TTL: 0, MaxCacheSize: 8, WarmupOnStartup: false, PreWarmActive: false, BatchSize: 10,
	})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = permissionService.Close() })
	handler := NewReviewHandler(tdb.GetDatabase(), permissionService)
	req := testutils.WithAuthContext(
		httptest.NewRequest(http.MethodGet, "/api/reviews/completed-items?start_date=2026-08-25&end_date=2026-08-25", nil),
		testutils.TestUserWithID(viewerID),
	)
	recorder := testutils.ExecuteRequest(t, handler.GetCompletedItems, req)
	recorder.AssertStatusCode(http.StatusOK)

	var items []models.Item
	if err := json.NewDecoder(recorder.Body).Decode(&items); err != nil {
		t.Fatalf("decode completed items: %v", err)
	}
	if len(items) != 1 || items[0].ID != visibleItemID || items[0].Title != "Visible completed item" {
		t.Fatalf("completed items = %+v, want only the item in a visible workspace", items)
	}
}
