package tests

import (
	"fmt"
	"net/http"
	"testing"
)

type homepageActivityResponse struct {
	RecentWorkspaces    []homepageWorkspaceActivity `json:"recent_workspaces"`
	TotalWorkspaceCount int                         `json:"total_workspace_count"`
	TotalItemCount      int                         `json:"total_item_count"`
	RecentlyViewed      []homepageItemActivity      `json:"recently_viewed"`
	RecentlyEdited      []homepageItemActivity      `json:"recently_edited"`
	RecentlyCommented   []homepageItemActivity      `json:"recently_commented"`
	WatchedItems        []homepageItemActivity      `json:"watched_items"`
	UpcomingMilestones  []homepageMilestone         `json:"upcoming_milestones"`
}

type homepageWorkspaceActivity struct {
	WorkspaceID   int    `json:"workspace_id"`
	WorkspaceName string `json:"workspace_name"`
	WorkspaceKey  string `json:"workspace_key"`
	Icon          string `json:"icon"`
	Color         string `json:"color"`
	AvatarURL     string `json:"avatar_url"`
}

type workspaceListResponse struct {
	Data []struct {
		ID int `json:"id"`
	} `json:"data"`
	Pagination struct {
		Total int `json:"total"`
	} `json:"pagination"`
}

type homepageItemActivity struct {
	ItemID int `json:"item_id"`
}

type homepageMilestone struct {
	MilestoneID int `json:"milestone_id"`
}

func TestHomepageFiltersActivityAfterWorkspaceAccessRevocation(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()

	adminToken := CreateBearerToken(t, server)
	server.BearerToken = adminToken

	workspaceID, _ := CreateTestWorkspace(t, server, "Homepage Permission Feed", shortKey("HPF"))
	LockDownWorkspace(t, server, workspaceID)
	milestoneResp := MakeAuthRequest(t, server, http.MethodPost, "/milestones", map[string]interface{}{
		"name":         "Revoked homepage milestone",
		"status":       "in-progress",
		"is_global":    false,
		"workspace_id": workspaceID,
	})
	AssertStatusCode(t, milestoneResp, http.StatusCreated)
	var milestoneResult map[string]interface{}
	DecodeJSON(t, milestoneResp, &milestoneResult)
	milestoneResp.Body.Close()
	milestoneID := ExtractIDFromResponse(t, milestoneResult)
	itemID := CreateTestItem(t, server, workspaceID, "Revoked homepage item")

	userID, username, password := CreateTestUserWithCredentials(t, server, "homepage_revoked_user", "homepage-revoked@test.com")
	AssignWorkspaceRole(t, server, userID, workspaceID, "Editor")
	userToken, userBearerToken := CreateAuthCredentialsForUser(t, server, username, password)

	workspaceResp := MakeAuthRequestWithToken(t, server, userToken, http.MethodGet, fmt.Sprintf("/workspaces/%d", workspaceID), nil)
	AssertStatusCode(t, workspaceResp, http.StatusOK)
	workspaceResp.Body.Close()

	viewResp := MakeAuthRequestWithToken(t, server, userToken, http.MethodGet, fmt.Sprintf("/items/%d", itemID), nil)
	AssertStatusCode(t, viewResp, http.StatusOK)
	viewResp.Body.Close()

	editResp := MakeAuthRequestWithToken(t, server, userToken, http.MethodPut, fmt.Sprintf("/items/%d", itemID), map[string]interface{}{
		"title":         "Revoked homepage item edited",
		"milestone_ids": []int{milestoneID},
	})
	AssertStatusCode(t, editResp, http.StatusOK)
	editResp.Body.Close()

	commentResp := MakeAuthRequestWithToken(t, server, userToken, http.MethodPost, fmt.Sprintf("/items/%d/comments", itemID), map[string]interface{}{
		"content": "Homepage permission regression comment",
	})
	AssertStatusCode(t, commentResp, http.StatusCreated)
	commentResp.Body.Close()

	watchResp := MakeAuthRequestWithToken(t, server, userToken, http.MethodPost, fmt.Sprintf("/items/%d/watch", itemID), map[string]interface{}{})
	AssertStatusCode(t, watchResp, http.StatusOK)
	watchResp.Body.Close()

	before := getHomepageActivity(t, server, userToken)
	assertHomepageFeedsContainItem(t, before, itemID, milestoneID)
	assertHomepageContainsWorkspace(t, before, workspaceID)
	beforeV1 := getV1Workspaces(t, server, userBearerToken)
	assertV1WorkspacePresence(t, beforeV1, workspaceID, true)
	assertWorkspaceIDPresence(t, "cookie workspace list", getCookieWorkspaceIDs(t, server, userToken), workspaceID, true)
	assertWorkspaceIDPresence(t, "MCP workspace list", getMCPWorkspaceIDs(t, server, userBearerToken), workspaceID, true)

	roles := GetWorkspaceRoles(t, server)
	RevokeWorkspaceRole(t, server, userID, workspaceID, roles["Editor"])

	renamedWorkspace := "Homepage Permission Feed Renamed"
	renamedKey := shortKey("HPFR")
	updateResp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/workspaces/%d", workspaceID), map[string]interface{}{
		"name":  renamedWorkspace,
		"key":   renamedKey,
		"icon":  "lock",
		"color": "#123456",
	})
	AssertStatusCode(t, updateResp, http.StatusOK)
	updateResp.Body.Close()

	deniedResp := MakeAuthRequestWithToken(t, server, userToken, http.MethodGet, fmt.Sprintf("/items/%d", itemID), nil)
	AssertStatusCode(t, deniedResp, http.StatusNotFound)
	deniedResp.Body.Close()

	after := getHomepageActivity(t, server, userToken)
	assertHomepageFeedsOmitItem(t, after, itemID, milestoneID)
	assertHomepageOmitsWorkspace(t, after, workspaceID, renamedWorkspace, renamedKey)
	if after.TotalWorkspaceCount != before.TotalWorkspaceCount-1 {
		t.Errorf("total_workspace_count after revocation = %d, want %d", after.TotalWorkspaceCount, before.TotalWorkspaceCount-1)
	}
	if after.TotalItemCount != before.TotalItemCount-1 {
		t.Errorf("total_item_count after revocation = %d, want %d", after.TotalItemCount, before.TotalItemCount-1)
	}

	afterV1 := getV1Workspaces(t, server, userBearerToken)
	assertV1WorkspacePresence(t, afterV1, workspaceID, false)
	assertWorkspaceIDPresence(t, "cookie workspace list", getCookieWorkspaceIDs(t, server, userToken), workspaceID, false)
	assertWorkspaceIDPresence(t, "MCP workspace list", getMCPWorkspaceIDs(t, server, userBearerToken), workspaceID, false)
	if afterV1.Pagination.Total != beforeV1.Pagination.Total-1 {
		t.Errorf("v1 workspace total after revocation = %d, want %d", afterV1.Pagination.Total, beforeV1.Pagination.Total-1)
	}
}

func TestHomepageAndWorkspaceListsFilterGroupDerivedRevocation(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()

	adminToken := CreateBearerToken(t, server)
	server.BearerToken = adminToken
	workspaceID, _ := CreateTestWorkspace(t, server, "Group Revocation Workspace", shortKey("GRW"))
	LockDownWorkspace(t, server, workspaceID)
	userID, username, password := CreateTestUserWithCredentials(t, server, "homepage_group_revoked_user", "homepage-group-revoked@test.com")
	userToken, userBearerToken := CreateAuthCredentialsForUser(t, server, username, password)

	groupResp := MakeAuthRequest(t, server, http.MethodPost, "/groups", map[string]interface{}{
		"name":        "Homepage group revocation",
		"description": "Revocation contract group",
	})
	AssertStatusCode(t, groupResp, http.StatusCreated)
	var groupResult map[string]interface{}
	DecodeJSON(t, groupResp, &groupResult)
	groupResp.Body.Close()
	groupID := ExtractIDFromResponse(t, groupResult)

	memberResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/groups/%d/members", groupID), map[string]interface{}{
		"user_ids": []int{userID},
	})
	AssertStatusCode(t, memberResp, http.StatusOK)
	memberResp.Body.Close()
	roles := GetWorkspaceRoles(t, server)
	roleResp := MakeAuthRequest(t, server, http.MethodPost, "/workspace-roles/assign-group", map[string]interface{}{
		"group_id":     groupID,
		"workspace_id": workspaceID,
		"role_id":      roles["Viewer"],
	})
	AssertStatusCode(t, roleResp, http.StatusCreated)
	roleResp.Body.Close()

	visitResp := MakeAuthRequestWithToken(t, server, userToken, http.MethodGet, fmt.Sprintf("/workspaces/%d", workspaceID), nil)
	AssertStatusCode(t, visitResp, http.StatusOK)
	visitResp.Body.Close()
	before := getHomepageActivity(t, server, userToken)
	assertHomepageContainsWorkspace(t, before, workspaceID)
	beforeV1 := getV1Workspaces(t, server, userBearerToken)
	assertV1WorkspacePresence(t, beforeV1, workspaceID, true)

	removeResp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/groups/%d/members", groupID), map[string]interface{}{
		"user_ids": []int{userID},
	})
	AssertStatusCode(t, removeResp, http.StatusOK)
	removeResp.Body.Close()
	renamedWorkspace := "Group Revocation Workspace Renamed"
	updateResp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/workspaces/%d", workspaceID), map[string]interface{}{
		"name": renamedWorkspace,
	})
	AssertStatusCode(t, updateResp, http.StatusOK)
	updateResp.Body.Close()

	after := getHomepageActivity(t, server, userToken)
	assertHomepageOmitsWorkspace(t, after, workspaceID, renamedWorkspace, "")
	if after.TotalWorkspaceCount != before.TotalWorkspaceCount-1 {
		t.Errorf("total_workspace_count after group revocation = %d, want %d", after.TotalWorkspaceCount, before.TotalWorkspaceCount-1)
	}
	afterV1 := getV1Workspaces(t, server, userBearerToken)
	assertV1WorkspacePresence(t, afterV1, workspaceID, false)
	assertWorkspaceIDPresence(t, "cookie workspace list", getCookieWorkspaceIDs(t, server, userToken), workspaceID, false)
	assertWorkspaceIDPresence(t, "MCP workspace list", getMCPWorkspaceIDs(t, server, userBearerToken), workspaceID, false)
	if afterV1.Pagination.Total != beforeV1.Pagination.Total-1 {
		t.Errorf("v1 workspace total after group revocation = %d, want %d", afterV1.Pagination.Total, beforeV1.Pagination.Total-1)
	}
}

func getV1Workspaces(t *testing.T, server *TestServer, token string) workspaceListResponse {
	t.Helper()
	resp := MakeBearerRequestWithToken(t, server, token, http.MethodGet, "/rest/api/v1/workspaces?limit=100", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var result workspaceListResponse
	DecodeJSON(t, resp, &result)
	return result
}

func getCookieWorkspaceIDs(t *testing.T, server *TestServer, token string) []int {
	t.Helper()
	resp := MakeAuthRequestWithToken(t, server, token, http.MethodGet, "/workspaces", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var workspaces []struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, resp, &workspaces)
	ids := make([]int, len(workspaces))
	for i, workspace := range workspaces {
		ids[i] = workspace.ID
	}
	return ids
}

func getMCPWorkspaceIDs(t *testing.T, server *TestServer, token string) []int {
	t.Helper()
	var response struct {
		Workspaces []struct {
			ID int `json:"id"`
		} `json:"workspaces"`
	}
	callTool(t, dialMCPWithToken(t, server, token), "list_workspaces", map[string]interface{}{}, &response)
	ids := make([]int, len(response.Workspaces))
	for i, workspace := range response.Workspaces {
		ids[i] = workspace.ID
	}
	return ids
}

func getHomepageActivity(t *testing.T, server *TestServer, token string) homepageActivityResponse {
	t.Helper()
	resp := MakeAuthRequestWithToken(t, server, token, http.MethodGet, "/homepage", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var result homepageActivityResponse
	DecodeJSON(t, resp, &result)
	return result
}

func assertHomepageFeedsContainItem(t *testing.T, homepage homepageActivityResponse, itemID, milestoneID int) {
	t.Helper()
	feeds := map[string][]homepageItemActivity{
		"recently_viewed":    homepage.RecentlyViewed,
		"recently_edited":    homepage.RecentlyEdited,
		"recently_commented": homepage.RecentlyCommented,
		"watched_items":      homepage.WatchedItems,
	}
	for name, feed := range feeds {
		if !homepageFeedContainsItem(feed, itemID) {
			t.Errorf("%s should contain item %d before access is revoked", name, itemID)
		}
	}
	if !homepageMilestonesContain(homepage.UpcomingMilestones, milestoneID) {
		t.Errorf("upcoming_milestones should contain milestone %d before access is revoked", milestoneID)
	}
}

func assertHomepageFeedsOmitItem(t *testing.T, homepage homepageActivityResponse, itemID, milestoneID int) {
	t.Helper()
	feeds := map[string][]homepageItemActivity{
		"recently_viewed":    homepage.RecentlyViewed,
		"recently_edited":    homepage.RecentlyEdited,
		"recently_commented": homepage.RecentlyCommented,
		"watched_items":      homepage.WatchedItems,
	}
	for name, feed := range feeds {
		if homepageFeedContainsItem(feed, itemID) {
			t.Errorf("%s leaked revoked item %d", name, itemID)
		}
	}
	if homepageMilestonesContain(homepage.UpcomingMilestones, milestoneID) {
		t.Errorf("upcoming_milestones leaked revoked milestone %d", milestoneID)
	}
}

func assertHomepageContainsWorkspace(t *testing.T, homepage homepageActivityResponse, workspaceID int) {
	t.Helper()
	for _, workspace := range homepage.RecentWorkspaces {
		if workspace.WorkspaceID == workspaceID {
			return
		}
	}
	t.Fatalf("recent_workspaces should contain workspace %d before access is revoked", workspaceID)
}

func assertHomepageOmitsWorkspace(t *testing.T, homepage homepageActivityResponse, workspaceID int, name, key string) {
	t.Helper()
	for _, workspace := range homepage.RecentWorkspaces {
		if workspace.WorkspaceID == workspaceID || workspace.WorkspaceName == name || (key != "" && workspace.WorkspaceKey == key) {
			t.Errorf("recent_workspaces leaked revoked workspace metadata: %+v", workspace)
		}
	}
}

func assertV1WorkspacePresence(t *testing.T, response workspaceListResponse, workspaceID int, want bool) {
	t.Helper()
	found := false
	for _, workspace := range response.Data {
		if workspace.ID == workspaceID {
			found = true
			break
		}
	}
	if found != want {
		t.Errorf("v1 workspace %d presence = %t, want %t", workspaceID, found, want)
	}
}

func assertWorkspaceIDPresence(t *testing.T, surface string, workspaceIDs []int, workspaceID int, want bool) {
	t.Helper()
	found := false
	for _, id := range workspaceIDs {
		if id == workspaceID {
			found = true
			break
		}
	}
	if found != want {
		t.Errorf("%s workspace %d presence = %t, want %t", surface, workspaceID, found, want)
	}
}

func homepageMilestonesContain(milestones []homepageMilestone, milestoneID int) bool {
	for _, milestone := range milestones {
		if milestone.MilestoneID == milestoneID {
			return true
		}
	}
	return false
}

func homepageFeedContainsItem(feed []homepageItemActivity, itemID int) bool {
	for _, item := range feed {
		if item.ItemID == itemID {
			return true
		}
	}
	return false
}
