package tests

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

func TestEmptyCollectionQueryPreservesFilteredPagination(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Empty collection", shortKey("ECQ"))
	for i := range 11 {
		CreateTestItem(t, server, workspaceID, fmt.Sprintf("Empty query item %d", i))
	}
	type itemPage struct {
		Data []struct {
			ID int `json:"id"`
		} `json:"data"`
		Pagination struct {
			Page       int `json:"page"`
			TotalItems int `json:"total_items"`
			TotalPages int `json:"total_pages"`
		} `json:"pagination"`
	}
	read := func(t *testing.T, path string) itemPage {
		t.Helper()
		response := MakeAuthRequest(t, server, http.MethodGet, path, nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)
		var result itemPage
		DecodeJSON(t, response, &result)
		return result
	}
	for _, query := range []string{"", "   "} {
		t.Run(fmt.Sprintf("query=%q", query), func(t *testing.T) {
			response := MakeAuthRequest(t, server, http.MethodPost, "/v2/collections", map[string]any{
				"name": "Empty query", "ql_query": query,
			})
			defer response.Body.Close()
			AssertStatusCode(t, response, http.StatusCreated)
			var collection struct {
				Data struct {
					ID int `json:"id"`
				} `json:"data"`
			}
			DecodeJSON(t, response, &collection)
			bootstrap := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/collections/%d/board-configuration/bootstrap", collection.Data.ID), nil)
			defer bootstrap.Body.Close()
			AssertStatusCode(t, bootstrap, http.StatusOK)
			var metadata struct {
				Data struct {
					WorkspaceIDs []int `json:"referenced_workspace_ids"`
				} `json:"data"`
			}
			DecodeJSON(t, bootstrap, &metadata)
			if !reflect.DeepEqual(metadata.Data.WorkspaceIDs, []int{workspaceID}) {
				t.Errorf("board workspace scope = %v, want [%d]", metadata.Data.WorkspaceIDs, workspaceID)
			}
			for _, subQL := range []string{"", "status_completed = false", "status_completed = true"} {
				for page := 1; page <= 2; page++ {
					path := fmt.Sprintf("/v2/items?page_size=10&page=%d&sort=title&sub_ql=%s", page, url.QueryEscape(subQL))
					want := read(t, path)
					if subQL == "" && (want.Pagination.TotalItems != 11 || want.Pagination.TotalPages != 2 || len(want.Data) != map[int]int{1: 10, 2: 1}[page]) {
						t.Fatalf("unexpected baseline page: %+v", want)
					}
					got := read(t, fmt.Sprintf("%s&collection_id=%d", path, collection.Data.ID))
					if !reflect.DeepEqual(got, want) {
						t.Errorf("subQL=%q page=%d: collection page=%+v, unfiltered scope=%+v", subQL, page, got, want)
					}
				}
			}
		})
	}
}

func TestEmptyCollectionQueryExcludesInaccessibleWorkspaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	visibleWS, _ := CreateTestWorkspace(t, server, "Visible collection scope", shortKey("ECV"))
	hiddenWS, _ := CreateTestWorkspace(t, server, "Hidden collection scope", shortKey("ECH"))
	LockDownWorkspace(t, server, visibleWS)
	LockDownWorkspace(t, server, hiddenWS)
	visibleID := CreateTestItem(t, server, visibleWS, "Visible item")
	hiddenID := CreateTestItem(t, server, hiddenWS, "Hidden item")
	userID, username, password := CreateTestUserWithCredentials(t, server, "empty_collection_reader", "empty_collection_reader@example.com")
	AssignWorkspaceRole(t, server, userID, visibleWS, "Editor")
	token := CreateBearerTokenForUser(t, server, username, password)
	response := MakeAuthRequestWithToken(t, server, token, http.MethodPost, "/v2/collections", map[string]any{"name": "All my accessible items", "ql_query": ""})
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusCreated)
	var collection struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, response, &collection)
	for _, filter := range []string{"", "&sub_ql=" + url.QueryEscape(fmt.Sprintf("id = %d", hiddenID)), fmt.Sprintf("&id=%d", hiddenID)} {
		response := MakeAuthRequestWithToken(t, server, token, http.MethodGet, fmt.Sprintf("/v2/items?collection_id=%d%s", collection.Data.ID, filter), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)
		var result struct {
			Data []struct {
				ID int `json:"id"`
			} `json:"data"`
			Pagination struct {
				TotalItems int `json:"total_items"`
			} `json:"pagination"`
		}
		DecodeJSON(t, response, &result)
		if filter == "" {
			if result.Pagination.TotalItems != 1 || len(result.Data) != 1 || result.Data[0].ID != visibleID {
				t.Fatalf("visible page = %+v, want only item %d", result, visibleID)
			}
		} else if result.Pagination.TotalItems != 0 || len(result.Data) != 0 {
			t.Fatalf("inaccessible item filter returned %+v, want empty page", result)
		}
	}
	bootstrap := MakeAuthRequestWithToken(t, server, token, http.MethodGet, fmt.Sprintf("/v2/collections/%d/board-configuration/bootstrap", collection.Data.ID), nil)
	defer bootstrap.Body.Close()
	AssertStatusCode(t, bootstrap, http.StatusOK)
	var metadata struct {
		Data struct {
			WorkspaceIDs []int `json:"referenced_workspace_ids"`
		} `json:"data"`
	}
	DecodeJSON(t, bootstrap, &metadata)
	if !reflect.DeepEqual(metadata.Data.WorkspaceIDs, []int{visibleWS}) {
		t.Fatalf("board scope = %v, want only workspace %d", metadata.Data.WorkspaceIDs, visibleWS)
	}
}
