package tests

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

func TestRelativeDateCQLHTTP(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Relative date CQL", "RDCQL")
	categoryIDs := CreateTestStatusCategories(t, server, "RDCQL")
	statusIDs := CreateTestStatuses(t, server, "RDCQL", categoryIDs)
	BindStatusesToWorkspace(t, server, workspaceID, "Relative date CQL workflow", statusIDs)
	statusOpen := statusIDs[0]
	statusDone := statusIDs[4]

	itemTypes := GetItemTypes(t, server, GetDefaultConfigurationSet(t, server))
	bugTypeID := RequireItemTypeID(t, itemTypes, "Bug")

	milestoneResponse := MakeAuthRequest(t, server, http.MethodPost, "/milestones", map[string]interface{}{
		"name":         "Relative date milestone",
		"description":  "Milestone for relative date CQL",
		"status":       "in-progress",
		"workspace_id": workspaceID,
	})
	AssertStatusCode(t, milestoneResponse, http.StatusCreated)
	var milestone map[string]interface{}
	DecodeJSON(t, milestoneResponse, &milestone)
	milestoneResponse.Body.Close()
	milestoneID := ExtractIDFromResponse(t, milestone)

	createItem := func(title string, milestoneIDs []int) int {
		t.Helper()
		data := map[string]interface{}{
			"workspace_id": workspaceID,
			"item_type_id": bugTypeID,
			"title":        title,
			"status_id":    statusOpen,
		}
		if len(milestoneIDs) > 0 {
			data["milestone_ids"] = milestoneIDs
		}
		resp := MakeAuthRequest(t, server, http.MethodPost, "/items", data)
		AssertStatusCode(t, resp, http.StatusCreated)
		var item map[string]interface{}
		DecodeJSON(t, resp, &item)
		resp.Body.Close()
		return ExtractIDFromResponse(t, item)
	}

	transitionToDone := func(itemID int) {
		t.Helper()
		resp := MakeAuthRequest(t, server, http.MethodPost,
			fmt.Sprintf("/items/%d/transition", itemID),
			map[string]interface{}{"to_status_id": statusDone})
		AssertStatusCode(t, resp, http.StatusOK)
		resp.Body.Close()
	}

	recentID := createItem("Recent unmilestoned bug", nil)
	transitionToDone(recentID)
	milestonedID := createItem("Recent milestoned bug", []int{milestoneID})
	transitionToDone(milestonedID)
	openID := createItem("Open unmilestoned bug", nil)

	query := `itemtypename = Bug AND completed_at >= -90d AND milestonename IS EMPTY`
	resp := MakeAuthRequest(t, server, http.MethodGet,
		"/items?ql="+url.QueryEscape(query)+"&limit=100", nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var body struct {
		Items []struct {
			ID int `json:"id"`
		} `json:"items"`
	}
	DecodeJSON(t, resp, &body)
	resp.Body.Close()

	if len(body.Items) != 1 || body.Items[0].ID != recentID {
		t.Fatalf("relative CQL results = %#v, want only item %d (milestoned=%d, open=%d)", body.Items, recentID, milestonedID, openID)
	}

	invalid := MakeAuthRequest(t, server, http.MethodGet,
		"/items?ql="+url.QueryEscape(`completed_at >= +30d`), nil)
	AssertStatusCode(t, invalid, http.StatusBadRequest)
	invalid.Body.Close()
}
