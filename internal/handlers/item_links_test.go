//go:build test

package handlers

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// createItemLinkHandler creates an ItemLinkHandler with test services
func createItemLinkHandler(t *testing.T, tdb *testutils.TestDB) *ItemLinkHandler {
	t.Helper()
	permService, _, notifService := createTestServices(t, *tdb)
	return NewItemLinkHandler(tdb.GetDatabase(), notifService, permService)
}

// createSecondTestItem creates a second item in the test workspace for link tests
func createSecondTestItem(t *testing.T, tdb *testutils.TestDB, data testutils.TestDataSet) int {
	t.Helper()
	permService, actTracker, notifService := createTestServices(t, *tdb)
	itemHandler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)

	statusID := data.StatusID
	priorityID := data.PriorityID
	item := models.Item{
		WorkspaceID: data.WorkspaceID,
		Title:       "Second Item for Links",
		StatusID:    &statusID,
		PriorityID:  &priorityID,
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/items", item)
	rr := testutils.ExecuteAuthenticatedRequest(t, itemHandler.Create, req, nil)
	rr.AssertStatusCode(http.StatusCreated)

	var created models.Item
	rr.AssertJSONResponse(&created)
	return created.ID
}

// createTestLinkType creates a link type through the owning repository.
func createTestLinkType(t *testing.T, tdb *testutils.TestDB) int {
	t.Helper()
	return newServiceSetup(t, tdb).CreateLinkType("blocks", "blocks", "is blocked by")
}

// --- GetLinksForItem permission tests ---

func TestItemLinkHandler_GetLinksForItem_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createItemLinkHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)

	req := testutils.CreateJSONRequest(t, "GET", fmt.Sprintf("/api/items/%d/links", itemID), nil)
	req.SetPathValue("id", testutils.IntToString(itemID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetLinksForItem, req, nil)

	rr.AssertStatusCode(http.StatusOK)
}

func TestItemLinkHandler_GetLinksForItem_NonExistentItem(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createItemLinkHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/items/99999/links", nil)
	req.SetPathValue("id", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetLinksForItem, req, nil)

	// Handler now validates existence and fails closed — non-existent items
	// (or items the user can't see) return 404 to avoid leaking existence.
	rr.AssertStatusCode(http.StatusNotFound)
}

func TestItemLinkHandler_GetLinksForItem_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createItemLinkHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/items/1/links", nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteRequest(t, handler.GetLinksForItem, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

// --- CreateLink permission tests ---

func TestItemLinkHandler_CreateLink_RequiresSourceEditAndTargetView(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createItemLinkHandler(t, tdb)
	sourceID := createTestItemForComments(t, tdb, data)
	targetID := createSecondTestItem(t, tdb, data)
	linkTypeID := createTestLinkType(t, tdb)

	body := map[string]interface{}{
		"source_type":  "item",
		"source_id":    sourceID,
		"target_type":  "item",
		"target_id":    targetID,
		"link_type_id": linkTypeID,
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/item-links", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.CreateLink, req, nil)

	rr.AssertStatusCode(http.StatusCreated)
}

func TestItemLinkHandler_CreateLink_NonExistentSource(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createItemLinkHandler(t, tdb)
	targetID := createTestItemForComments(t, tdb, data)
	linkTypeID := createTestLinkType(t, tdb)

	body := map[string]interface{}{
		"source_type":  "item",
		"source_id":    99999,
		"target_type":  "item",
		"target_id":    targetID,
		"link_type_id": linkTypeID,
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/item-links", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.CreateLink, req, nil)

	// Returns 404 for non-existent source item
	rr.AssertStatusCode(http.StatusNotFound)
}

func TestItemLinkHandler_CreateLink_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createItemLinkHandler(t, tdb)

	// Use link_type_id 999 (not 1, which is the special "Tests" type that
	// requires one item and one test_case). Validation and duplicate check pass,
	// then RequireAuth returns 401.
	body := map[string]interface{}{
		"source_type":  "item",
		"source_id":    1,
		"target_type":  "item",
		"target_id":    2,
		"link_type_id": 999,
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/item-links", body)
	rr := testutils.ExecuteRequest(t, handler.CreateLink, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

// --- DeleteLink permission tests ---

func TestItemLinkHandler_DeleteLink_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createItemLinkHandler(t, tdb)
	sourceID := createTestItemForComments(t, tdb, data)
	targetID := createSecondTestItem(t, tdb, data)
	linkTypeID := createTestLinkType(t, tdb)

	// Create a link first
	body := map[string]interface{}{
		"source_type":  "item",
		"source_id":    sourceID,
		"target_type":  "item",
		"target_id":    targetID,
		"link_type_id": linkTypeID,
	}
	createReq := testutils.CreateJSONRequest(t, "POST", "/api/item-links", body)
	createRR := testutils.ExecuteAuthenticatedRequest(t, handler.CreateLink, createReq, nil)
	createRR.AssertStatusCode(http.StatusCreated)

	var createdLink models.ItemLink
	createRR.AssertJSONResponse(&createdLink)

	// Try to delete without authentication using the link's ID
	deleteReq := testutils.CreateJSONRequest(t, "DELETE",
		fmt.Sprintf("/api/item-links/%d", createdLink.ID), nil)
	deleteReq.SetPathValue("id", testutils.IntToString(createdLink.ID))
	rr := testutils.ExecuteRequest(t, handler.DeleteLink, deleteReq)

	rr.AssertStatusCode(http.StatusUnauthorized)
}
