package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestTimeProjectWorklogsRedactItemsFromInaccessibleWorkspaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	projectID := createTimeProjectFixture(t, server, "Mixed workspace worklogs")
	visibleWorkspaceID, visibleWorkspaceKey := CreateTestWorkspace(t, server, "Visible worklogs", shortKey("VWL"))
	hiddenWorkspaceID, _ := CreateTestWorkspace(t, server, "Hidden worklogs", shortKey("HWL"))
	LockDownWorkspace(t, server, visibleWorkspaceID)
	LockDownWorkspace(t, server, hiddenWorkspaceID)
	visibleItemID := CreateTestItem(t, server, visibleWorkspaceID, "Visible worklog item")
	hiddenItemID := CreateTestItem(t, server, hiddenWorkspaceID, "Hidden worklog item")

	managerID, username, password := CreateTestUserWithCredentials(t, server, "mixed_worklog_manager", "mixed_worklog_manager@test.com")
	AssignWorkspaceRole(t, server, managerID, visibleWorkspaceID, "Viewer")
	manager := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/time/projects/%d/managers", projectID), map[string]any{
		"principal_type": "user", "principal_id": managerID,
	})
	defer manager.Body.Close()
	AssertStatusCode(t, manager, http.StatusCreated)

	for _, worklog := range []struct {
		description string
		itemID      int
	}{
		{"visible", visibleItemID},
		{"hidden", hiddenItemID},
	} {
		response := MakeAuthRequest(t, server, http.MethodPost, "/v2/time/worklogs", map[string]any{
			"project_id": projectID, "item_id": worklog.itemID, "description": worklog.description,
			"date": "2026-09-05", "duration_minutes": 30,
		})
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusCreated)
	}

	token := createTokenWithScopesAsUser(t, server, username, password, []string{"time:read"})
	response := MakeBearerRequestWithToken(t, server, token, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/time/projects/%d/worklogs?page_size=10", projectID), nil)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)
	var page struct {
		Data []struct {
			Description         string `json:"description"`
			ItemID              *int   `json:"item_id"`
			ItemTitle           string `json:"item_title"`
			WorkspaceID         *int   `json:"workspace_id"`
			WorkspaceKey        string `json:"workspace_key"`
			WorkspaceItemNumber int    `json:"workspace_item_number"`
		} `json:"data"`
		Pagination struct {
			TotalItems int `json:"total_items"`
		} `json:"pagination"`
	}
	DecodeJSON(t, response, &page)
	if len(page.Data) != 2 || page.Pagination.TotalItems != 2 {
		t.Fatalf("worklog page length/total = %d/%d, want 2/2", len(page.Data), page.Pagination.TotalItems)
	}
	for _, worklog := range page.Data {
		switch worklog.Description {
		case "visible":
			if worklog.ItemID == nil || *worklog.ItemID != visibleItemID || worklog.WorkspaceID == nil || *worklog.WorkspaceID != visibleWorkspaceID || worklog.ItemTitle != "Visible worklog item" || worklog.WorkspaceKey != visibleWorkspaceKey {
				t.Fatalf("visible item metadata was not preserved: %+v", worklog)
			}
		case "hidden":
			if worklog.ItemID != nil || worklog.WorkspaceID != nil || worklog.ItemTitle != "" || worklog.WorkspaceKey != "" || worklog.WorkspaceItemNumber != 0 {
				t.Fatalf("hidden item metadata was exposed: %+v", worklog)
			}
		default:
			t.Fatalf("unexpected worklog: %+v", worklog)
		}
	}
}
