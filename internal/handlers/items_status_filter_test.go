//go:build test

package handlers

import (
	"net/http"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// Covers the multi-status include/exclude query params on GET /api/items
// (status_id / status_id_not, CSV). The board's split fetch relies on these
// to page non-completed columns separately from the capped rightmost column.
func TestItemHandler_GetAll_StatusIDFilters(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)

	statusA := data.StatusID
	var statusB int
	if err := tdb.QueryRow(`SELECT id FROM statuses WHERE id != ? ORDER BY id LIMIT 1`, statusA).Scan(&statusB); err != nil {
		t.Fatalf("find second status: %v", err)
	}

	createItem := func(title string, statusID int) {
		t.Helper()
		item := models.Item{
			WorkspaceID: data.WorkspaceID,
			Title:       title,
			StatusID:    &statusID,
		}
		req := testutils.CreateJSONRequest(t, "POST", "/api/items", item)
		testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil).AssertStatusCode(http.StatusCreated)
	}
	for i := 0; i < 2; i++ {
		createItem("Status A item "+testutils.IntToString(i+1), statusA)
	}
	for i := 0; i < 3; i++ {
		createItem("Status B item "+testutils.IntToString(i+1), statusB)
	}

	list := func(query string) models.PaginatedItemsResponse {
		t.Helper()
		req := testutils.CreateJSONRequest(t, "GET",
			"/api/items?workspace_id="+testutils.IntToString(data.WorkspaceID)+query, nil)
		rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)
		rr.AssertStatusCode(http.StatusOK)
		var response models.PaginatedItemsResponse
		rr.AssertJSONResponse(&response)
		return response
	}

	t.Run("status_id includes only matching items", func(t *testing.T) {
		response := list("&status_id=" + testutils.IntToString(statusA))
		if len(response.Items) != 2 || response.Pagination.Total != 2 {
			t.Errorf("expected 2 items / total 2, got %d / %d", len(response.Items), response.Pagination.Total)
		}
		for _, item := range response.Items {
			if item.StatusID == nil || *item.StatusID != statusA {
				t.Errorf("item %q has unexpected status", item.Title)
			}
		}
	})

	t.Run("status_id accepts a CSV list", func(t *testing.T) {
		response := list("&status_id=" + testutils.IntToString(statusA) + "," + testutils.IntToString(statusB))
		if len(response.Items) != 5 || response.Pagination.Total != 5 {
			t.Errorf("expected 5 items / total 5, got %d / %d", len(response.Items), response.Pagination.Total)
		}
	})

	t.Run("status_id_not excludes matching items", func(t *testing.T) {
		response := list("&status_id_not=" + testutils.IntToString(statusA))
		if len(response.Items) != 3 || response.Pagination.Total != 3 {
			t.Errorf("expected 3 items / total 3, got %d / %d", len(response.Items), response.Pagination.Total)
		}
		for _, item := range response.Items {
			if item.StatusID != nil && *item.StatusID == statusA {
				t.Errorf("item %q should have been excluded", item.Title)
			}
		}
	})

	t.Run("pagination total respects the exclusion filter", func(t *testing.T) {
		response := list("&status_id_not=" + testutils.IntToString(statusA) + "&limit=2&page=1")
		if len(response.Items) != 2 {
			t.Errorf("expected 2 items on page, got %d", len(response.Items))
		}
		if response.Pagination.Total != 3 || response.Pagination.TotalPages != 2 {
			t.Errorf("expected total 3 / 2 pages, got %d / %d", response.Pagination.Total, response.Pagination.TotalPages)
		}
	})
}
