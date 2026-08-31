//go:build test

package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// newWorkspaceHandlerForSettings constructs a WorkspaceHandler wired with real
// services + an initialized key cache. Mirrors the pattern in workspace_test.go.
func newWorkspaceHandlerForSettings(t *testing.T, tdb *testutils.TestDB) *WorkspaceHandler {
	t.Helper()
	permService, actTracker, _ := createTestServices(t, *tdb)
	keyCache := NewWorkspaceKeyCache(repository.NewWorkspaceRepository(tdb.GetDatabase()))
	return NewWorkspaceHandler(tdb.GetDatabase(), permService, actTracker, keyCache)
}

// --- GetHomepageLayout ------------------------------------------------------

func TestWorkspaceHandler_GetHomepageLayout_EmptyDefault(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces/1/homepage/layout", nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetHomepageLayout, req, nil)

	rr.AssertStatusCode(http.StatusOK)
	var layout models.WorkspaceHomepageLayout
	rr.AssertJSONResponse(&layout)

	if layout.Sections == nil {
		t.Error("Expected non-nil Sections slice, got nil")
	}
	if layout.Widgets == nil {
		t.Error("Expected non-nil Widgets slice, got nil")
	}
	if len(layout.Sections) != 0 || len(layout.Widgets) != 0 {
		t.Errorf("Expected empty layout, got %d sections / %d widgets", len(layout.Sections), len(layout.Widgets))
	}
}

func TestWorkspaceHandler_GetHomepageLayout_PersistedRoundTrip(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	// Seed a saved layout directly in the DB (bypasses UpdateHomepageLayout).
	saved := models.WorkspaceHomepageLayout{
		Sections: []models.WorkspaceHomepageSection{
			{ID: "s-1", Title: "Overview", DisplayOrder: 0, WidgetIDs: []string{"w-1"}},
		},
		Widgets: []models.WorkspaceWidget{
			{ID: "w-1", Type: "stats", SectionID: "s-1", Position: 0, Width: 2},
		},
		Gradient: 3,
	}
	blob, err := json.Marshal(saved)
	if err != nil {
		t.Fatalf("marshal layout: %v", err)
	}
	if _, err := tdb.Exec(`UPDATE workspaces SET homepage_layout = ? WHERE id = 1`, string(blob)); err != nil {
		t.Fatalf("seed layout: %v", err)
	}

	req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces/1/homepage/layout", nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetHomepageLayout, req, nil)

	rr.AssertStatusCode(http.StatusOK)
	var got models.WorkspaceHomepageLayout
	rr.AssertJSONResponse(&got)

	if len(got.Sections) != 1 || got.Sections[0].Title != "Overview" {
		t.Errorf("Sections didn't round-trip: %+v", got.Sections)
	}
	if len(got.Widgets) != 1 || got.Widgets[0].Type != "stats" || got.Widgets[0].Width != 2 {
		t.Errorf("Widgets didn't round-trip: %+v", got.Widgets)
	}
	if got.Gradient != 3 {
		t.Errorf("Expected Gradient=3, got %d", got.Gradient)
	}
}

func TestWorkspaceHandler_GetHomepageLayout_UnknownWorkspace(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	grantSystemAdmin(t, tdb, 1) // bypass permission check so we reach the not-found path
	handler := newWorkspaceHandlerForSettings(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces/99999/homepage/layout", nil)
	req.SetPathValue("id", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetHomepageLayout, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}

// --- UpdateHomepageLayout ---------------------------------------------------

func TestWorkspaceHandler_UpdateHomepageLayout_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	layout := models.WorkspaceHomepageLayout{
		Sections: []models.WorkspaceHomepageSection{
			{ID: "s-1", Title: "Charts", DisplayOrder: 0, WidgetIDs: []string{"w-1"}},
		},
		Widgets: []models.WorkspaceWidget{
			{ID: "w-1", Type: "completion-chart", SectionID: "s-1", Position: 0, Width: 3},
		},
		Gradient:        5,
		ApplyToAllViews: true,
	}

	req := testutils.CreateJSONRequest(t, "PUT", "/api/workspaces/1/homepage/layout", layout)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateHomepageLayout, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	// Verify the DB row was updated.
	var stored string
	if err := tdb.QueryRow(`SELECT homepage_layout FROM workspaces WHERE id = 1`).Scan(&stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	var got models.WorkspaceHomepageLayout
	if err := json.Unmarshal([]byte(stored), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Widgets) != 1 || got.Widgets[0].Type != "completion-chart" {
		t.Errorf("Widget didn't persist: %+v", got.Widgets)
	}
	if got.Gradient != 5 || !got.ApplyToAllViews {
		t.Errorf("Gradient/ApplyToAllViews didn't persist: %+v", got)
	}
}

func TestWorkspaceHandler_UpdateHomepageLayout_AcceptsSavedSearchTypeAndConfig(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	layout := models.WorkspaceHomepageLayout{
		Sections: []models.WorkspaceHomepageSection{{
			ID:           "s-1",
			Title:        "Work",
			WidgetIDs:    []string{"w-1"},
			DisplayOrder: 0,
		}},
		Widgets: []models.WorkspaceWidget{{
			ID:        "w-1",
			Type:      "saved-search",
			SectionID: "s-1",
			Width:     2,
			Config:    map[string]any{"collectionId": 42},
		}},
	}

	req := testutils.CreateJSONRequest(t, "PUT", "/api/workspaces/1/homepage/layout", layout)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateHomepageLayout, req, nil)

	rr.AssertStatusCode(http.StatusOK)
	var stored string
	if err := tdb.QueryRow(`SELECT homepage_layout FROM workspaces WHERE id = 1`).Scan(&stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	var saved models.WorkspaceHomepageLayout
	if err := json.Unmarshal([]byte(stored), &saved); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if saved.Widgets[0].Type != "saved-search" {
		t.Fatalf("saved widget type = %q, want saved-search", saved.Widgets[0].Type)
	}
	if saved.Widgets[0].Config["collectionId"] != float64(42) {
		t.Fatalf("saved collection id = %v, want 42", saved.Widgets[0].Config["collectionId"])
	}
}

func TestWorkspaceHandler_UpdateHomepageLayout_InvalidWidgetType(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	layout := models.WorkspaceHomepageLayout{
		Widgets: []models.WorkspaceWidget{
			{ID: "w-1", Type: "not-a-real-widget", SectionID: "s-1", Width: 1},
		},
	}

	req := testutils.CreateJSONRequest(t, "PUT", "/api/workspaces/1/homepage/layout", layout)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateHomepageLayout, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
	testutils.AssertValidationError(t, rr, "Invalid widget type")
}

func TestWorkspaceHandler_UpdateHomepageLayout_InvalidWidgetWidth(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	layout := models.WorkspaceHomepageLayout{
		Widgets: []models.WorkspaceWidget{
			{ID: "w-1", Type: "stats", SectionID: "s-1", Width: 99},
		},
	}

	req := testutils.CreateJSONRequest(t, "PUT", "/api/workspaces/1/homepage/layout", layout)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateHomepageLayout, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
	testutils.AssertValidationError(t, rr, "Invalid widget width")
}

func TestWorkspaceHandler_UpdateHomepageLayout_PermissionDenied(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	// SeedTestData grants Administrator role, but Administrator does not include
	// workspace.admin for everyone — simulate a caller that only has item.view.
	otherUser := testutils.TestUserWithID(999)
	req := testutils.CreateJSONRequest(t, "PUT", "/api/workspaces/1/homepage/layout", models.WorkspaceHomepageLayout{})
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateHomepageLayout, req, otherUser)

	rr.AssertStatusCode(http.StatusForbidden)
}

// --- GetStatuses ------------------------------------------------------------

func TestWorkspaceHandler_GetStatuses_UsesDefaultWorkflow(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces/1/statuses", nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetStatuses, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	var statuses []models.Status
	rr.AssertJSONResponse(&statuses)
	// A fresh test DB has the default workflow seeded by Initialize(); it should
	// expose at least one status.
	if len(statuses) == 0 {
		t.Error("Expected at least one status from the default workflow")
	}
}

func TestWorkspaceHandler_GetStatuses_InvalidItemTypeID(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces/1/statuses?item_type_id=not-a-number", nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetStatuses, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
}
