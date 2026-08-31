//go:build test

package handlers

import (
	"net/http"
	"net/url"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

func TestItemHandler_GetAll_ScopedSearchMatchesTitleAndDescription(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)

	createItem := func(title, description string) int {
		t.Helper()
		req := testutils.CreateJSONRequest(t, http.MethodPost, "/api/items", models.Item{
			WorkspaceID: data.WorkspaceID,
			Title:       title,
			Description: description,
		})
		rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
		rr.AssertStatusCode(http.StatusCreated)
		var created models.Item
		rr.AssertJSONResponse(&created)
		return created.ID
	}

	titleMatchID := createItem("Scoped title needle", "ordinary body")
	descriptionMatchID := createItem("Ordinary title", "Contains the scoped description needle")
	createItem("Unrelated item", "Nothing to find here")

	tests := []struct {
		name  string
		query string
		want  int
	}{
		{name: "title", query: "title needle", want: titleMatchID},
		{name: "description", query: "description needle", want: descriptionMatchID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestURL := "/api/items?workspace_id=" + testutils.IntToString(data.WorkspaceID) +
				"&omit_descriptions=true&search=" + url.QueryEscape(tt.query)
			req := testutils.CreateJSONRequest(t, http.MethodGet, requestURL, nil)
			rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)
			rr.AssertStatusCode(http.StatusOK)

			var response models.PaginatedItemsResponse
			rr.AssertJSONResponse(&response)
			if len(response.Items) != 1 || response.Items[0].ID != tt.want {
				t.Fatalf("search items = %#v, want only item %d", response.Items, tt.want)
			}
			if response.Items[0].Description != "" {
				t.Fatalf("description = %q, want omitted summary response", response.Items[0].Description)
			}
		})
	}
}
