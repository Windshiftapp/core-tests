package tests

import (
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"windshift/internal/models"
)

func TestV2CollectionSearchAndSlug(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	admin := CreateBearerToken(t, server)
	userID, username, password := CreateTestUserWithCredentials(t, server, "collection_reader", "collection_reader@example.com")
	visibleWS, _ := CreateTestWorkspace(t, server, "Visible collection workspace", shortKey("CSV"))
	AssignWorkspaceRole(t, server, userID, visibleWS, "Editor")
	reader := CreateBearerTokenForUser(t, server, username, password)
	hiddenWS, _ := CreateTestWorkspace(t, server, "Hidden collection workspace", shortKey("CSH"))
	LockDownWorkspace(t, server, hiddenWS)
	create := func(name, slug string, public, private bool, workspaceID *int) models.Collection {
		t.Helper()
		query := ""
		if workspaceID != nil {
			query = fmt.Sprintf("workspace_id = %d", *workspaceID)
		}
		response := MakeAuthRequestWithToken(t, server, admin, http.MethodPost, "/v2/collections", map[string]any{
			"name": name, "ql_query": query, "is_public": public, "is_private": private, "public_slug": slug, "workspace_id": workspaceID,
		})
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusCreated)
		var result struct {
			Data models.Collection `json:"data"`
		}
		DecodeJSON(t, response, &result)
		return result.Data
	}
	first := create("Roadmap Alpha", "roadmap-alpha", true, false, &visibleWS)
	second := create("ROADMAP Beta", "roadmap-beta", true, false, &visibleWS)
	create("Unrelated", "unrelated", true, false, &visibleWS)
	private := create("Roadmap Private", "roadmap-private", false, true, nil)
	hidden := create("Roadmap Hidden", "roadmap-hidden", true, false, &hiddenWS)
	t.Run("search filters before pagination and counts only visible matches", func(t *testing.T) {
		var ids []int
		for page := 1; page <= 2; page++ {
			response := MakeAuthRequestWithToken(t, server, reader, http.MethodGet, fmt.Sprintf("/v2/collections?q=%%20rOaDmAp%%20&page_size=1&page=%d", page), nil)
			defer response.Body.Close()
			AssertStatusCode(t, response, http.StatusOK)
			var result struct {
				Data       []models.Collection `json:"data"`
				Pagination struct {
					TotalItems int `json:"total_items"`
					TotalPages int `json:"total_pages"`
				} `json:"pagination"`
			}
			DecodeJSON(t, response, &result)
			if result.Pagination.TotalItems != 2 || result.Pagination.TotalPages != 2 || len(result.Data) != 1 {
				t.Fatalf("search page = %+v, want one result and two total matches/pages", result)
			}
			ids = append(ids, result.Data[0].ID)
		}
		slices.Sort(ids)
		if !reflect.DeepEqual(ids, []int{first.ID, second.ID}) {
			t.Fatalf("IDs = %v, want [%d %d]", ids, first.ID, second.ID)
		}
	})
	for _, tc := range []struct {
		name, query string
		want        int
	}{
		{"no matches", "q=absent", 0},
		{"empty search", "q=", 3},
		{"whitespace search", "q=%20%20", 3},
		{"workspace and name filters", fmt.Sprintf("q=alpha&workspace_id=%d", visibleWS), 1},
		{"inaccessible workspace filter", fmt.Sprintf("q=roadmap&workspace_id=%d", hiddenWS), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := MakeAuthRequestWithToken(t, server, reader, http.MethodGet, "/v2/collections?"+tc.query, nil)
			defer response.Body.Close()
			AssertStatusCode(t, response, http.StatusOK)
			var result struct {
				Data       []models.Collection `json:"data"`
				Pagination struct {
					TotalItems int `json:"total_items"`
				} `json:"pagination"`
			}
			DecodeJSON(t, response, &result)
			if len(result.Data) != tc.want || result.Pagination.TotalItems != tc.want {
				t.Fatalf("page = %+v, want %d visible matches", result, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		name, key, token string
		want             models.Collection
	}{
		{"numeric ID", fmt.Sprint(first.ID), reader, first},
		{"public slug", "roadmap-alpha", reader, first},
		{"owner private slug", "roadmap-private", admin, private},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := MakeAuthRequestWithToken(t, server, tc.token, http.MethodGet, "/v2/collections/"+tc.key, nil)
			defer response.Body.Close()
			AssertStatusCode(t, response, http.StatusOK)
			var result struct {
				Data models.Collection `json:"data"`
			}
			DecodeJSON(t, response, &result)
			if !reflect.DeepEqual(result.Data, tc.want) {
				t.Fatalf("collection = %+v, want %+v", result.Data, tc.want)
			}
		})
	}
	for _, key := range []string{"roadmap-private", fmt.Sprint(private.ID), "roadmap-hidden", fmt.Sprint(hidden.ID), "missing-slug"} {
		t.Run("denies "+key, func(t *testing.T) {
			response := MakeAuthRequestWithToken(t, server, reader, http.MethodGet, "/v2/collections/"+key, nil)
			defer response.Body.Close()
			AssertStatusCode(t, response, http.StatusNotFound)
			var result struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			DecodeJSON(t, response, &result)
			if result.Error.Code != "not_found" || result.Error.Message != "Resource not found" {
				t.Fatalf("denial = %+v", result)
			}
		})
	}
}
