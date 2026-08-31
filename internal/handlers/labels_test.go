//go:build test

package handlers

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// createLabelHandler creates a LabelHandler with test services
func createLabelHandler(t *testing.T, tdb *testutils.TestDB) *LabelHandler {
	t.Helper()
	permService, _, _ := createTestServices(t, *tdb)
	db := tdb.GetDatabase()
	return NewLabelHandler(
		repository.NewLabelRepository(db),
		repository.NewItemRepository(db),
		permService,
		logger.NewAuditor(db),
	)
}

// createTestLabel creates a global label using a workspace as authorization
// context and returns it.
func createTestLabel(t *testing.T, tdb *testutils.TestDB, workspaceID int, name string) models.Label {
	t.Helper()
	return newServiceSetup(t, tdb).CreateLabel(workspaceID, name)
}

// --- GetAll permission tests ---

func TestLabelHandler_GetAll_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createLabelHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/labels?workspace_id=1", nil)
	rr := testutils.ExecuteRequest(t, handler.GetAll, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

func TestLabelHandler_GetAll_IgnoresLegacyWorkspaceFilter(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createLabelHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/labels?workspace_id=99999", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)

	rr.AssertStatusCode(http.StatusOK)
}

func TestLabelHandler_GetAll_WithPermission(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createLabelHandler(t, tdb)

	// Create a label in the workspace
	createTestLabel(t, tdb, data.WorkspaceID, "Test Label")

	req := testutils.CreateJSONRequest(t, "GET", "/api/labels", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var labels []models.Label
	rr.AssertJSONResponse(&labels)

	if len(labels) == 0 {
		t.Error("Expected at least one label")
	}
}

func TestLabelHandler_GetAll_ReturnsGlobalCatalog(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createLabelHandler(t, tdb)
	localLabel := createTestLabel(t, tdb, data.WorkspaceID, "Local")
	foreignWorkspaceID := newServiceSetup(t, tdb).CreateWorkspace("Foreign", "FOREIGN")
	foreignLabel := createTestLabel(t, tdb, foreignWorkspaceID, "Foreign")

	req := testutils.CreateJSONRequest(t, "GET", "/api/labels", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)

	rr.AssertStatusCode(http.StatusOK)
	var labels []models.Label
	rr.AssertJSONResponse(&labels)
	got := map[int]bool{}
	for _, label := range labels {
		got[label.ID] = true
	}
	if !got[localLabel.ID] || !got[foreignLabel.ID] {
		t.Fatalf("global labels = %#v, want label IDs %d and %d", labels, localLabel.ID, foreignLabel.ID)
	}
}

func TestLabelHandler_Create_RejectsDuplicateFromAnotherWorkspaceContext(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createLabelHandler(t, tdb)
	createTestLabel(t, tdb, data.WorkspaceID, "Shared")
	foreignWorkspaceID := newServiceSetup(t, tdb).CreateWorkspace("Foreign", "FOREIGN")
	req := testutils.CreateJSONRequest(t, "POST", "/api/labels", map[string]any{
		"name":         "shared",
		"workspace_id": foreignWorkspaceID,
	})
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

	rr.AssertStatusCode(http.StatusConflict)
}

// --- Get permission tests ---

func TestLabelHandler_Get_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createLabelHandler(t, tdb)

	label := createTestLabel(t, tdb, data.WorkspaceID, "Secret Label")

	req := testutils.CreateJSONRequest(t, "GET", fmt.Sprintf("/api/labels/%d", label.ID), nil)
	req.SetPathValue("id", testutils.IntToString(label.ID))
	rr := testutils.ExecuteRequest(t, handler.Get, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

// --- GetItemLabels permission tests ---

func TestLabelHandler_GetItemLabels_RequiresItemViewPermission(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createLabelHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)

	// User with permission should succeed
	req := testutils.CreateJSONRequest(t, "GET", fmt.Sprintf("/api/items/%d/labels", itemID), nil)
	req.SetPathValue("id", testutils.IntToString(itemID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetItemLabels, req, nil)

	rr.AssertStatusCode(http.StatusOK)
}

func TestLabelHandler_GetItemLabels_NonExistentItem(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createLabelHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/items/99999/labels", nil)
	req.SetPathValue("id", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetItemLabels, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}

func TestLabelHandler_GetItemLabels_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createLabelHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)

	req := testutils.CreateJSONRequest(t, "GET", fmt.Sprintf("/api/items/%d/labels", itemID), nil)
	req.SetPathValue("id", testutils.IntToString(itemID))
	rr := testutils.ExecuteRequest(t, handler.GetItemLabels, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

// --- SetItemLabels permission tests ---

func TestLabelHandler_SetItemLabels_RequiresItemEditPermission(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createLabelHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)
	label := createTestLabel(t, tdb, data.WorkspaceID, "Apply Label")

	body := map[string]interface{}{
		"label_ids": []int{label.ID},
	}

	req := testutils.CreateJSONRequest(t, "PUT", fmt.Sprintf("/api/items/%d/labels", itemID), body)
	req.SetPathValue("id", testutils.IntToString(itemID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.SetItemLabels, req, nil)

	rr.AssertStatusCode(http.StatusOK)
}

func TestLabelHandler_SetItemLabels_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createLabelHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)

	body := map[string]interface{}{
		"label_ids": []int{1},
	}

	req := testutils.CreateJSONRequest(t, "PUT", fmt.Sprintf("/api/items/%d/labels", itemID), body)
	req.SetPathValue("id", testutils.IntToString(itemID))
	rr := testutils.ExecuteRequest(t, handler.SetItemLabels, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

func TestLabelHandler_SetItemLabels_AcceptsGlobalLabelCreatedFromAnotherWorkspace(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createLabelHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)
	foreignWorkspaceID := newServiceSetup(t, tdb).CreateWorkspace("Foreign", "FOREIGN")
	globalLabel := createTestLabel(t, tdb, foreignWorkspaceID, "Global")
	req := testutils.CreateJSONRequest(t, "PUT", fmt.Sprintf("/api/items/%d/labels", itemID), map[string]any{
		"label_ids": []int{globalLabel.ID},
	})
	req.SetPathValue("id", testutils.IntToString(itemID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.SetItemLabels, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	labels, err := repository.NewLabelRepository(tdb.GetDatabase()).ListForItem(itemID)
	if err != nil {
		t.Fatalf("list labels after replacement: %v", err)
	}
	if len(labels) != 1 || labels[0].ID != globalLabel.ID {
		t.Fatalf("labels after replacement = %#v, want global label %d", labels, globalLabel.ID)
	}
}

// --- AddItemLabel permission tests ---

func TestLabelHandler_AddItemLabel_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createLabelHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)

	body := map[string]interface{}{
		"label_id": 1,
	}

	req := testutils.CreateJSONRequest(t, "POST", fmt.Sprintf("/api/items/%d/labels", itemID), body)
	req.SetPathValue("id", testutils.IntToString(itemID))
	rr := testutils.ExecuteRequest(t, handler.AddItemLabel, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

// --- RemoveItemLabel permission tests ---

func TestLabelHandler_RemoveItemLabel_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createLabelHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)

	req := testutils.CreateJSONRequest(t, "DELETE", fmt.Sprintf("/api/items/%d/labels/1", itemID), nil)
	req.SetPathValue("id", testutils.IntToString(itemID))
	req.SetPathValue("labelId", "1")
	rr := testutils.ExecuteRequest(t, handler.RemoveItemLabel, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

func TestLabelHandler_RemoveItemLabel_NonExistentItem(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createLabelHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/items/99999/labels/1", nil)
	req.SetPathValue("id", "99999")
	req.SetPathValue("labelId", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.RemoveItemLabel, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}
