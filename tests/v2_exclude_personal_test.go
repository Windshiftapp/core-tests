package tests

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"windshift/internal/models"
	"windshift/internal/services"
)

func TestV2ExcludePersonal(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspace, workspaceKey := CreateTestWorkspace(t, server, "Shared integration items", shortKey("EPI"))
	sharedID := CreateTestItem(t, server, workspace, "Exclusion contract shared")
	response := MakeAuthRequest(t, server, http.MethodGet, "/workspaces/personal", nil)
	AssertStatusCode(t, response, http.StatusCreated)
	var personalWorkspace models.Workspace
	DecodeJSON(t, response, &personalWorkspace)
	response.Body.Close()
	response = MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/items", map[string]any{"workspace_id": personalWorkspace.ID, "title": "Exclusion contract personal", "is_task": true})
	AssertStatusCode(t, response, http.StatusCreated)
	var personal models.Item
	DecodeJSON(t, response, &personal)
	response.Body.Close()
	response = MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/collections", map[string]any{"name": "Exclusion collection", "ql_query": fmt.Sprintf("workspace IN (%s, %s)", workspaceKey, personalWorkspace.Key)})
	AssertStatusCode(t, response, http.StatusCreated)
	var collection models.Collection
	DecodeJSON(t, response, &collection)
	response.Body.Close()

	for _, option := range []struct {
		value   string
		exclude bool
	}{{"", false}, {"false", false}, {"0", false}, {"true", true}, {"1", true}} {
		t.Run("exclude="+option.value, func(t *testing.T) {
			for _, selector := range []string{"search=Exclusion", "ql=" + url.QueryEscape(fmt.Sprintf("workspace IN (%s, %s)", workspaceKey, personalWorkspace.Key)), fmt.Sprintf("collection_id=%d", collection.ID), fmt.Sprintf("workspace_id=%d", personalWorkspace.ID)} {
				t.Run(selector, func(t *testing.T) {
					want := []int{sharedID, personal.ID}
					if selector == fmt.Sprintf("workspace_id=%d", personalWorkspace.ID) {
						want = []int{personal.ID}
					}
					if option.exclude {
						want = slices.DeleteFunc(want, func(id int) bool { return id == personal.ID })
					}
					var got []int
					for page := 1; page <= max(1, len(want)); page++ {
						response := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/items?%s&exclude_personal=%s&page_size=1&page=%d&sort=title", selector, option.value, page), nil)
						defer response.Body.Close()
						AssertStatusCode(t, response, http.StatusOK)
						var document struct {
							Data       []models.Item `json:"data"`
							Pagination struct {
								Total int `json:"total_items"`
							} `json:"pagination"`
						}
						DecodeJSON(t, response, &document)
						if document.Pagination.Total != len(want) {
							t.Fatalf("total = %d, want %d", document.Pagination.Total, len(want))
						}
						for _, item := range document.Data {
							got = append(got, item.ID)
						}
					}
					slices.Sort(want)
					slices.Sort(got)
					if !slices.Equal(got, want) {
						t.Fatalf("items = %v, want %v", got, want)
					}
				})
			}
			for _, path := range []string{fmt.Sprintf("/items/%d", personal.ID), fmt.Sprintf("/items/%s-%d", personalWorkspace.Key, personal.WorkspaceItemNumber), fmt.Sprintf("/workspaces/%s/items/%d", personalWorkspace.Key, personal.WorkspaceItemNumber), fmt.Sprintf("/items/%d", sharedID)} {
				t.Run(path, func(t *testing.T) {
					response := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2"+path+"?exclude_personal="+option.value, nil)
					defer response.Body.Close()
					if option.exclude && path != fmt.Sprintf("/items/%d", sharedID) {
						AssertStatusCode(t, response, http.StatusNotFound)
						var document struct {
							Error struct {
								Code    string `json:"code"`
								Message string `json:"message"`
							} `json:"error"`
						}
						DecodeJSON(t, response, &document)
						if document.Error.Code != "not_found" || document.Error.Message != "Item not found" {
							t.Fatalf("denial = %+v", document.Error)
						}
					} else {
						AssertStatusCode(t, response, http.StatusOK)
						var item models.Item
						DecodeJSON(t, response, &item)
						want := personal.ID
						if path == fmt.Sprintf("/items/%d", sharedID) {
							want = sharedID
						}
						if item.ID != want {
							t.Fatalf("item = %d, want %d", item.ID, want)
						}
					}
				})
			}
		})
	}
}

// WI-1380: exclude_personal must also hold on batch, backlog, changes, and
// detail-summary — the surfaces that gained plumbing with the 0.8.9 fix.
func TestV2ExcludePersonalNewSurfaces(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()

	CreateBearerToken(t, server)

	workspace, workspaceKey := CreateTestWorkspace(t, server, "Exclusion new surfaces shared", shortKey("EXN"))
	sharedID := CreateTestItem(t, server, workspace, "New surfaces shared")

	response := MakeAuthRequest(t, server, http.MethodGet, "/workspaces/personal", nil)
	AssertStatusCode(t, response, http.StatusCreated)
	var personalWorkspace models.Workspace
	DecodeJSON(t, response, &personalWorkspace)
	response.Body.Close()

	response = MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/items", map[string]any{"workspace_id": personalWorkspace.ID, "title": "New surfaces personal", "is_task": true})
	AssertStatusCode(t, response, http.StatusCreated)
	var personal models.Item
	DecodeJSON(t, response, &personal)
	response.Body.Close()

	response = MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/collections", map[string]any{"name": "Exclusion new-surfaces collection", "ql_query": fmt.Sprintf("workspace IN (%s, %s)", workspaceKey, personalWorkspace.Key)})
	AssertStatusCode(t, response, http.StatusCreated)
	var collection models.Collection
	DecodeJSON(t, response, &collection)
	response.Body.Close()

	t.Run("batch", func(t *testing.T) {
		response := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/items/batch?exclude_personal=true", map[string]any{"ids": []int{sharedID, personal.ID}})
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)
		var items []models.Item
		DecodeJSON(t, response, &items)
		got := itemIDs(items)
		if slices.Contains(got, personal.ID) {
			t.Errorf("batch returned personal item %d despite exclude_personal=true (ids=%v)", personal.ID, got)
		}
		if !slices.Contains(got, sharedID) {
			t.Errorf("batch dropped shared item %d (ids=%v)", sharedID, got)
		}
	})

	t.Run("backlog", func(t *testing.T) {
		for _, selector := range []string{fmt.Sprintf("collection_id=%d", collection.ID), fmt.Sprintf("workspace_id=%d", workspace)} {
			response := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/items/backlog?exclude_personal=true&"+selector, nil)
			defer response.Body.Close()
			AssertStatusCode(t, response, http.StatusOK)
			var document struct {
				Data []models.Item `json:"data"`
			}
			DecodeJSON(t, response, &document)
			got := itemIDs(document.Data)
			if slices.Contains(got, personal.ID) {
				t.Errorf("backlog (%s) returned personal item %d despite exclude_personal=true (ids=%v)", selector, personal.ID, got)
			}
		}
	})

	t.Run("changes", func(t *testing.T) {
		response := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/items/changes?exclude_personal=true&since=0", nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)
		var result services.ItemChangesResult
		DecodeJSON(t, response, &result)
		if slices.Contains(result.ChangedItemIDs, personal.ID) {
			t.Errorf("changes reported personal item %d despite exclude_personal=true (changed=%v)", personal.ID, result.ChangedItemIDs)
		}
		if !slices.Contains(result.ChangedItemIDs, sharedID) {
			t.Errorf("changes dropped shared item %d (changed=%v)", sharedID, result.ChangedItemIDs)
		}
	})

	t.Run("detail-summary", func(t *testing.T) {
		for _, path := range []string{fmt.Sprintf("/items/%d/detail-summary", personal.ID), fmt.Sprintf("/workspaces/%s/items/%d/detail-summary", personalWorkspace.Key, personal.WorkspaceItemNumber)} {
			response := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2"+path+"?exclude_personal=true", nil)
			AssertStatusCode(t, response, http.StatusNotFound)
			response.Body.Close()
		}
		// Shared item and the personal key route without exclusion stay readable.
		response := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/items/%d/detail-summary", sharedID), nil)
		AssertStatusCode(t, response, http.StatusOK)
		response.Body.Close()
		response = MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/workspaces/"+personalWorkspace.Key+"/items/"+fmt.Sprint(personal.WorkspaceItemNumber), nil)
		AssertStatusCode(t, response, http.StatusOK)
		response.Body.Close()
	})
}
