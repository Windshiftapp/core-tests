package tests

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

func TestQLWorkspaceReferencesUseNamesAndKeysAcrossOperators(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	targetWorkspaceID, targetWorkspaceKey := CreateTestWorkspace(t, server, "Windshift GitHub", shortKey("WI"))
	otherWorkspaceID, _ := CreateTestWorkspace(t, server, "Other Workspace", shortKey("OPS"))
	targetItemID := CreateTestItem(t, server, targetWorkspaceID, "GitHub integration item")
	otherItemID := CreateTestItem(t, server, otherWorkspaceID, "Other GitHub item")

	label := createLabelFx(t, server, targetWorkspaceID, "github", "#24292f")
	setItemLabels(t, server, targetItemID, []int{label.ID})
	setItemLabels(t, server, otherItemID, []int{label.ID})

	queries := []string{
		fmt.Sprintf(`workspace = %s AND labels IN ("github")`, targetWorkspaceKey),
		fmt.Sprintf(`workspace = "%s" AND labels IN ("github")`, targetWorkspaceKey),
		fmt.Sprintf(`workspace IN (%s) AND labels IN ("github")`, targetWorkspaceKey),
		fmt.Sprintf(`workspace IN ("%s") AND labels IN ("github")`, targetWorkspaceKey),
		`workspace = "Windshift GitHub" AND labels IN ("github")`,
	}

	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			resp := MakeAuthRequest(t, server, http.MethodGet, "/items?limit=100&ql="+url.QueryEscape(query), nil)
			defer resp.Body.Close()
			AssertStatusCode(t, resp, http.StatusOK)

			var result struct {
				Items []struct {
					ID int `json:"id"`
				} `json:"items"`
			}
			DecodeJSON(t, resp, &result)
			if len(result.Items) != 1 || result.Items[0].ID != targetItemID {
				t.Fatalf("items = %#v, want only item %d", result.Items, targetItemID)
			}
		})
	}
}
