package tests

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

func TestAssetLinkedOfHidesUnauthorizedItemPredicates(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	setID, typeID := seedAssetSetAndType(t, server, "linked-query-permissions")
	workspaceID, _ := CreateTestWorkspace(t, server, "Private linked items", "ALQP")
	LockDownWorkspace(t, server, workspaceID)
	itemID := CreateTestItem(t, server, workspaceID, "Confidential acquisition")
	userID, username, password := CreateTestUserWithCredentials(t, server, "asset_query_viewer", "asset_query_viewer@test.com")
	assignAssetSetRole(t, server, setID, userID, getAssetRoleID(t, server, "Viewer"))
	token := createTokenWithScopesAsUser(t, server, username, password, []string{"assets:read", "items:read"})

	created := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/v2/asset-sets/%d/assets", setID), map[string]any{
		"title": "Visible laptop", "asset_type_id": typeID,
	})
	AssertStatusCode(t, created, http.StatusCreated)
	var asset map[string]any
	DecodeJSON(t, created, &asset)
	created.Body.Close()
	assetID := ExtractIDFromResponse(t, asset)
	linked := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/v2/assets/%d/links", assetID), map[string]any{
		"link_type_id": 4, "target_type": "item", "target_id": itemID,
	})
	AssertStatusCode(t, linked, http.StatusCreated)
	linked.Body.Close()
	otherWorkspaceID, _ := CreateTestWorkspace(t, server, "Private nested items", "ALQN")
	LockDownWorkspace(t, server, otherWorkspaceID)
	otherItemID := CreateTestItem(t, server, otherWorkspaceID, "Secret nested source")
	itemLink := MakeAuthRequest(t, server, http.MethodPost, "/links", map[string]any{
		"link_type_id": 4, "source_type": "item", "source_id": otherItemID,
		"target_type": "item", "target_id": itemID,
	})
	AssertStatusCode(t, itemLink, http.StatusCreated)
	itemLink.Body.Close()

	denied := MakeBearerRequestWithToken(t, server, token, http.MethodGet, fmt.Sprintf("/rest/api/v2/items/%d", itemID), nil)
	AssertStatusCode(t, denied, http.StatusNotFound)
	denied.Body.Close()

	checkGraph := func(wantItems int) {
		t.Helper()
		response := MakeBearerRequestWithToken(t, server, token, http.MethodGet, fmt.Sprintf("/rest/api/v2/assets/%d/relationship-graph", assetID), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)
		var graph struct {
			Nodes []struct {
				Type     string `json:"type"`
				EntityID int    `json:"entity_id"`
			} `json:"nodes"`
			Truncated bool `json:"truncated"`
		}
		DecodeJSON(t, response, &graph)
		if len(graph.Nodes) != 1+wantItems || graph.Truncated {
			t.Fatalf("graph = %+v", graph)
		}
		for _, node := range graph.Nodes {
			if node.Type == "item" && node.EntityID != itemID && (wantItems < 2 || node.EntityID != otherItemID) {
				t.Fatalf("unauthorized node = %+v", node)
			}
		}
	}
	checkGraph(0)
	query := `linkedOf("relates to", "title = 'Confidential acquisition'")`
	check := func(t *testing.T, query string, want int) {
		t.Helper()
		response := MakeBearerRequestWithToken(t, server, token, http.MethodGet,
			fmt.Sprintf("/rest/api/v2/asset-sets/%d/assets?ql=%s", setID, url.QueryEscape(query)), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)
		var result struct {
			Data []struct {
				ID int `json:"id"`
			} `json:"data"`
			Pagination struct {
				Total int `json:"total_items"`
			} `json:"pagination"`
		}
		DecodeJSON(t, response, &result)
		if len(result.Data) != want || result.Pagination.Total != want {
			t.Fatalf("query returned %d rows, total %d; want %d", len(result.Data), result.Pagination.Total, want)
		}
		if want == 1 && result.Data[0].ID != assetID {
			t.Fatalf("asset ID = %d, want %d", result.Data[0].ID, assetID)
		}
	}
	t.Run("private predicate returns no matches", func(t *testing.T) { check(t, query, 0) })
	AssignWorkspaceRole(t, server, userID, workspaceID, "Viewer")
	checkGraph(1)
	t.Run("authorized predicate returns the linked asset", func(t *testing.T) { check(t, query, 1) })
	nested := `linkedOf("relates to", "linkedOf('relates to', 'title = \"Secret nested source\"')")`
	t.Run("nested private predicate returns no matches", func(t *testing.T) { check(t, nested, 0) })
	AssignWorkspaceRole(t, server, userID, otherWorkspaceID, "Viewer")
	checkGraph(2)
	t.Run("authorized nested predicate returns the linked asset", func(t *testing.T) { check(t, nested, 1) })
}
