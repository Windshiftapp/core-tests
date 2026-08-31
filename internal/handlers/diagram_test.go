//go:build test

package handlers

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// createDiagramHandler creates a DiagramHandler with test services
func createDiagramHandler(t *testing.T, tdb *testutils.TestDB) *DiagramHandler {
	t.Helper()
	permService, _, _ := createTestServices(t, *tdb)
	db := tdb.GetDatabase()
	return NewDiagramHandler(
		repository.NewDiagramRepository(db),
		repository.NewItemRepository(db),
		permService,
	)
}

// createTestDiagramForItem creates a diagram for an item and returns its ID
func createTestDiagramForItem(t *testing.T, handler *DiagramHandler, itemID int) int {
	t.Helper()
	body := map[string]interface{}{
		"name":         "Test Diagram",
		"diagram_data": `{"nodes":[],"edges":[]}`,
	}

	req := testutils.CreateJSONRequest(t, "POST", fmt.Sprintf("/api/items/%d/diagrams", itemID), body)
	req.SetPathValue("itemId", testutils.IntToString(itemID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	var diagram models.ItemDiagram
	rr.AssertJSONResponse(&diagram)
	return diagram.ID
}

// --- Create permission tests ---

func TestDiagramHandler_Create_RequiresItemEditPermission(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createDiagramHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)

	body := map[string]interface{}{
		"name":         "Architecture Diagram",
		"diagram_data": `{"nodes":[],"edges":[]}`,
	}

	req := testutils.CreateJSONRequest(t, "POST", fmt.Sprintf("/api/items/%d/diagrams", itemID), body)
	req.SetPathValue("itemId", testutils.IntToString(itemID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

	rr.AssertStatusCode(http.StatusOK)
}

func TestDiagramHandler_Create_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createDiagramHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)

	body := map[string]interface{}{
		"name":         "Hacked Diagram",
		"diagram_data": `{"nodes":[],"edges":[]}`,
	}

	req := testutils.CreateJSONRequest(t, "POST", fmt.Sprintf("/api/items/%d/diagrams", itemID), body)
	req.SetPathValue("itemId", testutils.IntToString(itemID))
	rr := testutils.ExecuteRequest(t, handler.Create, req)

	// CheckItemPermission returns 401 when unauthenticated
	rr.AssertStatusCode(http.StatusUnauthorized)
}

func TestDiagramHandler_Create_NonExistentItem(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createDiagramHandler(t, tdb)

	body := map[string]interface{}{
		"name":         "Test Diagram",
		"diagram_data": `{"nodes":[],"edges":[]}`,
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/items/99999/diagrams", body)
	req.SetPathValue("itemId", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}

// --- GetByItem permission tests ---

func TestDiagramHandler_GetByItem_RequiresItemViewPermission(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createDiagramHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)
	createTestDiagramForItem(t, handler, itemID)

	req := testutils.CreateJSONRequest(t, "GET", fmt.Sprintf("/api/items/%d/diagrams", itemID), nil)
	req.SetPathValue("itemId", testutils.IntToString(itemID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetByItem, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	var diagrams []models.ItemDiagram
	rr.AssertJSONResponse(&diagrams)

	if len(diagrams) == 0 {
		t.Error("Expected at least one diagram")
	}
}

func TestDiagramHandler_GetByItem_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createDiagramHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)

	req := testutils.CreateJSONRequest(t, "GET", fmt.Sprintf("/api/items/%d/diagrams", itemID), nil)
	req.SetPathValue("itemId", testutils.IntToString(itemID))
	rr := testutils.ExecuteRequest(t, handler.GetByItem, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

// --- Get (single diagram) permission tests ---

func TestDiagramHandler_Get_RequiresItemViewPermission(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createDiagramHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)
	diagramID := createTestDiagramForItem(t, handler, itemID)

	req := testutils.CreateJSONRequest(t, "GET", fmt.Sprintf("/api/diagrams/%d", diagramID), nil)
	req.SetPathValue("id", testutils.IntToString(diagramID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Get, req, nil)

	rr.AssertStatusCode(http.StatusOK)
}

func TestDiagramHandler_Get_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createDiagramHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)
	diagramID := createTestDiagramForItem(t, handler, itemID)

	req := testutils.CreateJSONRequest(t, "GET", fmt.Sprintf("/api/diagrams/%d", diagramID), nil)
	req.SetPathValue("id", testutils.IntToString(diagramID))
	rr := testutils.ExecuteRequest(t, handler.Get, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

func TestDiagramHandler_Get_NotFound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createDiagramHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/diagrams/99999", nil)
	req.SetPathValue("id", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Get, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}

// --- Update permission tests ---

func TestDiagramHandler_Update_RequiresItemEditPermission(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createDiagramHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)
	diagramID := createTestDiagramForItem(t, handler, itemID)

	body := map[string]interface{}{
		"name":         "Updated Diagram",
		"diagram_data": `{"nodes":[{"id":"1"}],"edges":[]}`,
	}

	req := testutils.CreateJSONRequest(t, "PUT", fmt.Sprintf("/api/diagrams/%d", diagramID), body)
	req.SetPathValue("id", testutils.IntToString(diagramID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)

	rr.AssertStatusCode(http.StatusOK)
}

func TestDiagramHandler_Update_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createDiagramHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)
	diagramID := createTestDiagramForItem(t, handler, itemID)

	body := map[string]interface{}{
		"name":         "Hacked Update",
		"diagram_data": `{"nodes":[],"edges":[]}`,
	}

	req := testutils.CreateJSONRequest(t, "PUT", fmt.Sprintf("/api/diagrams/%d", diagramID), body)
	req.SetPathValue("id", testutils.IntToString(diagramID))
	rr := testutils.ExecuteRequest(t, handler.Update, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

// --- Delete permission tests ---

func TestDiagramHandler_Delete_RequiresItemEditPermission(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createDiagramHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)
	diagramID := createTestDiagramForItem(t, handler, itemID)

	req := testutils.CreateJSONRequest(t, "DELETE", fmt.Sprintf("/api/diagrams/%d", diagramID), nil)
	req.SetPathValue("id", testutils.IntToString(diagramID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, req, nil)

	rr.AssertStatusCode(http.StatusOK)
}

func TestDiagramHandler_Delete_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createDiagramHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)
	diagramID := createTestDiagramForItem(t, handler, itemID)

	req := testutils.CreateJSONRequest(t, "DELETE", fmt.Sprintf("/api/diagrams/%d", diagramID), nil)
	req.SetPathValue("id", testutils.IntToString(diagramID))
	rr := testutils.ExecuteRequest(t, handler.Delete, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

func TestDiagramHandler_Delete_NotFound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createDiagramHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/diagrams/99999", nil)
	req.SetPathValue("id", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}
