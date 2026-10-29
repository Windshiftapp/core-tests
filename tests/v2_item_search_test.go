package tests

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"windshift/internal/models"
)

func TestV2ItemSearch(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	visible, key := CreateTestWorkspace(t, server, "Search visible", shortKey("SV"))
	hidden, hiddenKey := CreateTestWorkspace(t, server, "Search hidden", shortKey("SH"))
	LockDownWorkspace(t, server, hidden)
	first := CreateTestItem(t, server, visible, "Searchcontract Alpha")
	second := CreateTestItem(t, server, visible, "Searchcontract Beta")
	secret := CreateTestItem(t, server, hidden, "Searchcontract Secret")
	userID, username, password := CreateTestUserWithCredentials(t, server, "search_reader", "search_reader@example.com")
	AssignWorkspaceRole(t, server, userID, visible, "Viewer")
	reader := CreateBearerTokenForUser(t, server, username, password)
	response := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/items/%d", first), nil)
	AssertStatusCode(t, response, http.StatusOK)
	var item models.Item
	DecodeJSON(t, response, &item)
	response.Body.Close()
	response = MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/items/%d", secret), nil)
	AssertStatusCode(t, response, http.StatusOK)
	var hiddenItem models.Item
	DecodeJSON(t, response, &hiddenItem)
	response.Body.Close()
	for _, tc := range []struct {
		name, query string
		want        []int
		total       int
	}{
		{"text newest first", "q=searchcontract", []int{second, first}, 2},
		{"key", "q=" + url.QueryEscape(strings.ToLower(fmt.Sprintf("%s-%d", key, item.WorkspaceItemNumber))), []int{first}, 1},
		{"key does not fall back to title", "q=" + key + "-99999", nil, 0},
		{"hidden key", fmt.Sprintf("q=%s-%d", hiddenKey, hiddenItem.WorkspaceItemNumber), nil, 0},
		{"hidden workspace", fmt.Sprintf("q=Searchcontract&workspace_id=%d", hidden), nil, 0},
		{"workspace intersection", fmt.Sprintf("q=Searchcontract&workspace_id=%d&workspace_id=%d", visible, hidden), []int{second, first}, 2},
		{"cql", "q=" + url.QueryEscape(`title = "Searchcontract Alpha"`), []int{first}, 1},
		{"explicit cql", "ql=" + url.QueryEscape(`title = "Searchcontract Beta"`), []int{second}, 1},
		{"empty", fmt.Sprintf("workspace_id=%d", visible), []int{second, first}, 2},
		{"pagination", "q=Searchcontract&page_size=1&page=2", []int{first}, 2},
		{"status filter", fmt.Sprintf("q=Searchcontract&status=%d", *item.StatusID), []int{second, first}, 2},
		{"nonmatching status", "q=Searchcontract&status=999999", nil, 0},
		{"nonmatching priority", "q=Searchcontract&priority=999999", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := MakeAuthRequestWithToken(t, server, reader, http.MethodGet, "/v2/items/search?"+tc.query, nil)
			defer response.Body.Close()
			AssertStatusCode(t, response, http.StatusOK)
			var body struct {
				Data       []models.Item `json:"data"`
				Pagination struct {
					Total int `json:"total_items"`
				} `json:"pagination"`
			}
			DecodeJSON(t, response, &body)
			var ids []int
			for _, found := range body.Data {
				ids = append(ids, found.ID)
			}
			if !slices.Equal(ids, tc.want) || body.Pagination.Total != tc.total {
				t.Fatalf("got IDs %v total %d; want %v total %d", ids, body.Pagination.Total, tc.want, tc.total)
			}
		})
	}
	for _, query := range []string{"q=" + strings.Repeat("a", 501), "workspace_id=bad", "status=bad", "priority=0", "ql=" + url.QueryEscape("status ="), "page_size=1001"} {
		t.Run("invalid "+query, func(t *testing.T) {
			response := MakeAuthRequestWithToken(t, server, reader, http.MethodGet, "/v2/items/search?"+query, nil)
			defer response.Body.Close()
			AssertStatusCode(t, response, http.StatusBadRequest)
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			DecodeJSON(t, response, &body)
			if body.Error.Code != "invalid_request" {
				t.Fatalf("error code = %q", body.Error.Code)
			}
		})
	}
}

func TestV2ItemSearchRequiresReadScope(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	token := createTokenWithScopesAsUser(t, server, "admin", "testpass123", []string{"workspaces:read"})
	response := MakeBearerRequestWithToken(t, server, token, http.MethodGet, "/rest/api/v2/items/search?q=anything", nil)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusForbidden)
	var denial struct {
		Error struct {
			Code    string              `json:"code"`
			Message string              `json:"message"`
			Details map[string][]string `json:"details"`
		} `json:"error"`
	}
	DecodeJSON(t, response, &denial)
	if denial.Error.Code != "insufficient_permission" || denial.Error.Message != "Token lacks a required scope" || !reflect.DeepEqual(denial.Error.Details, map[string][]string{"required": {"items:read"}}) {
		t.Fatalf("scope denial = %+v", denial)
	}
}
