package tests

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"windshift/internal/models"
)

// WI-1380: the exclude_personal opt-out must hold on every item surface, not
// only v2 list/search/detail. Today v1 List and v1 GetBatch ignore it.
func TestV1ExcludePersonalOptOutHonoredOnListAndBatch(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()

	CreateBearerToken(t, server)

	workspace, workspaceKey := CreateTestWorkspace(t, server, "Exclusion shared v1", shortKey("EXV"))
	sharedID := CreateTestItem(t, server, workspace, "V1 exclusion shared")

	response := MakeAuthRequest(t, server, http.MethodGet, "/workspaces/personal", nil)
	AssertStatusCode(t, response, http.StatusCreated)
	var personalWorkspace models.Workspace
	DecodeJSON(t, response, &personalWorkspace)
	response.Body.Close()

	response = MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v1/items",
		map[string]any{"workspace_id": personalWorkspace.ID, "title": "V1 exclusion personal", "is_task": true})
	AssertStatusCode(t, response, http.StatusCreated)
	var personal models.Item
	DecodeJSON(t, response, &personal)
	response.Body.Close()

	// Sanity: workspace keys with separators are exactly what personal
	// workspaces generate; the batch/list assertions below must hold for them.
	t.Logf("personal workspace key: %s (shared: %s)", personalWorkspace.Key, workspaceKey)

	t.Run("v1 list excludes personal", func(t *testing.T) {
		response := MakeBearerRequest(t, server, http.MethodGet,
			"/rest/api/v1/items?exclude_personal=true", nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)
		var document struct {
			Data []models.Item `json:"data"`
		}
		DecodeJSON(t, response, &document)
		ids := itemIDs(document.Data)
		if slices.Contains(ids, personal.ID) {
			t.Errorf("v1 list returned personal item %d despite exclude_personal=true (ids=%v)", personal.ID, ids)
		}
		if !slices.Contains(ids, sharedID) {
			t.Errorf("v1 list dropped shared item %d (ids=%v)", sharedID, ids)
		}
	})

	t.Run("v1 list without opt-out returns both", func(t *testing.T) {
		response := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v1/items", nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)
		var document struct {
			Data []models.Item `json:"data"`
		}
		DecodeJSON(t, response, &document)
		ids := itemIDs(document.Data)
		if !slices.Contains(ids, personal.ID) || !slices.Contains(ids, sharedID) {
			t.Errorf("v1 list baseline missing items: want both %d and %d in %v", personal.ID, sharedID, ids)
		}
	})

	t.Run("v1 batch excludes personal", func(t *testing.T) {
		response := MakeBearerRequest(t, server, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/items/batch?ids=%d,%d&exclude_personal=true", sharedID, personal.ID), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)
		var items []models.Item
		DecodeJSON(t, response, &items)
		ids := itemIDs(items)
		if slices.Contains(ids, personal.ID) {
			t.Errorf("v1 batch returned personal item %d despite exclude_personal=true (ids=%v)", personal.ID, ids)
		}
		if !slices.Contains(ids, sharedID) {
			t.Errorf("v1 batch dropped shared item %d (ids=%v)", sharedID, ids)
		}
	})
}

func itemIDs(items []models.Item) []int {
	ids := make([]int, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}
