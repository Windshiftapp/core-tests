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
	ItemID             int    `json:"item_id"`
	LastActivity       string `json:"last_activity"`
	LastActiveAt       string `json:"last_active_at"`
	ItemTypeName       string `json:"item_type_name"`
	ItemTypeBuiltinKey string `json:"item_type_builtin_key"`
	ItemTypeIcon       string `json:"item_type_icon"`
	ItemTypeColor      string `json:"item_type_color"`
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
	milestoneResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/v2/workspaces/%d/milestones", workspaceID), map[string]interface{}{
		"name":   "Revoked homepage milestone",
		"status": "in-progress",
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

	watchResp := MakeAuthRequestWithToken(t, server, userToken, http.MethodPut, fmt.Sprintf("/v2/items/%d/watch", itemID), map[string]interface{}{})
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
	updateResp := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/workspaces/%d", workspaceID), map[string]interface{}{
		"name":  renamedWorkspace,
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
	assertHomepageOmitsWorkspace(t, after, workspaceID, renamedWorkspace, "")
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

	groupResp := MakeAuthRequest(t, server, http.MethodPost, "/v2/admin/groups", map[string]interface{}{
		"name":        "Homepage group revocation",
		"description": "Revocation contract group",
	})
	AssertStatusCode(t, groupResp, http.StatusCreated)
	var groupResult map[string]interface{}
	DecodeJSON(t, groupResp, &groupResult)
	groupResp.Body.Close()
	groupID := ExtractIDFromResponse(t, groupResult)

	memberResp := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/admin/groups/%d", groupID), map[string]interface{}{
		"member_ids": []int{userID},
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

	removeResp := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/admin/groups/%d", groupID), map[string]interface{}{
		"member_ids": []int{},
	})
	AssertStatusCode(t, removeResp, http.StatusOK)
	removeResp.Body.Close()
	renamedWorkspace := "Group Revocation Workspace Renamed"
	updateResp := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/workspaces/%d", workspaceID), map[string]interface{}{
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

func TestWorkspaceVisibilityLifecycleMatrix(t *testing.T) {
	testCases := []struct {
		name       string
		transition string
	}{
		{name: "group role revocation", transition: "group-role-revocation"},
		{name: "group deactivation", transition: "group-deactivation"},
		{name: "group deletion", transition: "group-deletion"},
		{name: "REST v1 group deletion", transition: "rest-group-deletion"},
		{name: "active to inactive", transition: "workspace-deactivation"},
		{name: "REST v1 active to inactive", transition: "rest-workspace-deactivation"},
		{name: "open to gated", transition: "open-to-gated"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server, cleanup := StartTestServer(t, GetDBType())
			defer cleanup()

			CreateBearerToken(t, server)
			workspaceID, _ := CreateTestWorkspace(t, server, "Lifecycle "+testCase.name, shortKey("LCM"))
			itemID := CreateTestItem(t, server, workspaceID, "Lifecycle restricted item")
			actorID, actorUsername, actorPassword := CreateTestUserWithCredentials(
				t, server, "lifecycle_actor", "lifecycle-actor@example.test",
			)
			actorToken, actorBearerToken := CreateAuthCredentialsForUser(t, server, actorUsername, actorPassword)
			keeperID, keeperUsername, keeperPassword := CreateTestUserWithCredentials(
				t, server, "lifecycle_keeper", "lifecycle-keeper@example.test",
			)
			keeperToken, _ := CreateAuthCredentialsForUser(t, server, keeperUsername, keeperPassword)
			roles := GetWorkspaceRoles(t, server)
			groupID := 0

			switch testCase.transition {
			case "group-role-revocation", "group-deactivation", "group-deletion", "rest-group-deletion":
				LockDownWorkspace(t, server, workspaceID)
				AssignWorkspaceRole(t, server, keeperID, workspaceID, "Viewer")
				groupResp := MakeAuthRequest(t, server, http.MethodPost, "/v2/admin/groups", map[string]interface{}{
					"name":        "Lifecycle group " + testCase.name,
					"description": "Workspace visibility lifecycle matrix",
				})
				AssertStatusCode(t, groupResp, http.StatusCreated)
				var groupResult map[string]interface{}
				DecodeJSON(t, groupResp, &groupResult)
				groupResp.Body.Close()
				groupID = ExtractIDFromResponse(t, groupResult)

				memberResp := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/admin/groups/%d", groupID), map[string]interface{}{
					"member_ids": []int{actorID},
				})
				AssertStatusCode(t, memberResp, http.StatusOK)
				memberResp.Body.Close()
				roleResp := MakeAuthRequest(t, server, http.MethodPost, "/workspace-roles/assign-group", map[string]interface{}{
					"group_id":     groupID,
					"workspace_id": workspaceID,
					"role_id":      roles["Viewer"],
				})
				AssertStatusCode(t, roleResp, http.StatusCreated)
				roleResp.Body.Close()
			case "workspace-deactivation", "rest-workspace-deactivation":
				LockDownWorkspace(t, server, workspaceID)
				AssignWorkspaceRole(t, server, actorID, workspaceID, "Viewer")
				AssignWorkspaceRole(t, server, keeperID, workspaceID, "Administrator")
			case "open-to-gated":
				// The actor initially relies on implicit Everyone access. Assigning the
				// keeper later is the production transition that gates the workspace.
			default:
				t.Fatalf("unknown lifecycle transition %q", testCase.transition)
			}

			visitResp := MakeAuthRequestWithToken(t, server, actorToken, http.MethodGet, fmt.Sprintf("/workspaces/%d", workspaceID), nil)
			AssertStatusCode(t, visitResp, http.StatusOK)
			visitResp.Body.Close()
			itemResp := MakeAuthRequestWithToken(t, server, actorToken, http.MethodGet, fmt.Sprintf("/items/%d", itemID), nil)
			AssertStatusCode(t, itemResp, http.StatusOK)
			itemResp.Body.Close()

			before := getHomepageActivity(t, server, actorToken)
			assertHomepageContainsWorkspace(t, before, workspaceID)
			beforeV1 := getV1Workspaces(t, server, actorBearerToken)
			assertV1WorkspacePresence(t, beforeV1, workspaceID, true)
			assertWorkspaceIDPresence(t, "cookie workspace list", getCookieWorkspaceIDs(t, server, actorToken), workspaceID, true)
			assertWorkspaceIDPresence(t, "MCP workspace list", getMCPWorkspaceIDs(t, server, actorBearerToken), workspaceID, true)

			renamedWorkspace := "Lifecycle restricted renamed " + testCase.name
			switch testCase.transition {
			case "group-role-revocation":
				resp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf(
					"/groups/%d/workspaces/%d/roles/%d", groupID, workspaceID, roles["Viewer"],
				), nil)
				AssertStatusCode(t, resp, http.StatusNoContent)
				resp.Body.Close()
			case "group-deactivation":
				resp := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/admin/groups/%d", groupID), map[string]interface{}{
					"name":        "Lifecycle group " + testCase.name,
					"description": "Workspace visibility lifecycle matrix",
					"is_active":   false,
				})
				AssertStatusCode(t, resp, http.StatusOK)
				resp.Body.Close()
			case "group-deletion":
				resp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/v2/admin/groups/%d", groupID), nil)
				AssertStatusCode(t, resp, http.StatusNoContent)
				resp.Body.Close()
			case "rest-group-deletion":
				resp := MakeBearerRequestWithToken(t, server, server.BearerToken, http.MethodDelete,
					fmt.Sprintf("/rest/api/v1/admin/groups/%d", groupID), nil)
				AssertStatusCode(t, resp, http.StatusNoContent)
				resp.Body.Close()
			case "workspace-deactivation":
				resp := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/workspaces/%d", workspaceID), map[string]interface{}{
					"active": false,
				})
				AssertStatusCode(t, resp, http.StatusOK)
				resp.Body.Close()
			case "rest-workspace-deactivation":
				resp := MakeBearerRequestWithToken(t, server, server.BearerToken, http.MethodPut,
					fmt.Sprintf("/rest/api/v1/workspaces/%d", workspaceID), map[string]interface{}{
						"active": false,
					})
				AssertStatusCode(t, resp, http.StatusOK)
				resp.Body.Close()
			case "open-to-gated":
				AssignWorkspaceRole(t, server, keeperID, workspaceID, "Viewer")
			}

			// The authorization mutation itself must invalidate the warm snapshot.
			// Assert before any later workspace mutation can invalidate caches too.
			immediateResp := MakeAuthRequestWithToken(t, server, actorToken, http.MethodGet, fmt.Sprintf("/items/%d", itemID), nil)
			AssertStatusCode(t, immediateResp, http.StatusNotFound)
			immediateResp.Body.Close()

			renameResp := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/workspaces/%d", workspaceID), map[string]interface{}{
				"name": renamedWorkspace,
			})
			AssertStatusCode(t, renameResp, http.StatusOK)
			renameResp.Body.Close()
			mutateResp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/items/%d", itemID), map[string]interface{}{
				"title": "Lifecycle restricted item mutated after transition",
			})
			AssertStatusCode(t, mutateResp, http.StatusOK)
			mutateResp.Body.Close()

			for _, path := range []string{
				fmt.Sprintf("/workspaces/%d", workspaceID),
				fmt.Sprintf("/items/%d", itemID),
			} {
				resp := MakeAuthRequestWithToken(t, server, actorToken, http.MethodGet, path, nil)
				AssertStatusCode(t, resp, http.StatusNotFound)
				resp.Body.Close()
			}

			after := getHomepageActivity(t, server, actorToken)
			assertHomepageOmitsWorkspace(t, after, workspaceID, renamedWorkspace, "")
			if after.TotalWorkspaceCount != before.TotalWorkspaceCount-1 {
				t.Errorf("total_workspace_count after %s = %d, want %d", testCase.transition, after.TotalWorkspaceCount, before.TotalWorkspaceCount-1)
			}
			if after.TotalItemCount != before.TotalItemCount-1 {
				t.Errorf("total_item_count after %s = %d, want %d", testCase.transition, after.TotalItemCount, before.TotalItemCount-1)
			}

			afterV1 := getV1Workspaces(t, server, actorBearerToken)
			assertV1WorkspacePresence(t, afterV1, workspaceID, false)
			assertWorkspaceIDPresence(t, "cookie workspace list", getCookieWorkspaceIDs(t, server, actorToken), workspaceID, false)
			assertWorkspaceIDPresence(t, "MCP workspace list", getMCPWorkspaceIDs(t, server, actorBearerToken), workspaceID, false)
			if afterV1.Pagination.Total != beforeV1.Pagination.Total-1 {
				t.Errorf("v1 workspace total after %s = %d, want %d", testCase.transition, afterV1.Pagination.Total, beforeV1.Pagination.Total-1)
			}

			keeperResp := MakeAuthRequestWithToken(t, server, keeperToken, http.MethodGet, fmt.Sprintf("/items/%d", itemID), nil)
			AssertStatusCode(t, keeperResp, http.StatusOK)
			keeperResp.Body.Close()
			adminResp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d", itemID), nil)
			AssertStatusCode(t, adminResp, http.StatusOK)
			adminResp.Body.Close()
		})
	}
}

func TestWorkspaceVisibilityPersonalOwnerControl(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()

	adminToken := CreateBearerToken(t, server)
	server.BearerToken = adminToken
	_, ownerUsername, ownerPassword := CreateTestUserWithCredentials(
		t, server, "personal_owner", "personal-owner@example.test",
	)
	ownerToken, ownerBearerToken := CreateAuthCredentialsForUser(t, server, ownerUsername, ownerPassword)
	_, outsiderUsername, outsiderPassword := CreateTestUserWithCredentials(
		t, server, "personal_outsider", "personal-outsider@example.test",
	)
	outsiderToken, outsiderBearerToken := CreateAuthCredentialsForUser(t, server, outsiderUsername, outsiderPassword)

	personalResp := MakeAuthRequestWithToken(t, server, ownerToken, http.MethodGet, "/workspaces/personal", nil)
	AssertStatusCode(t, personalResp, http.StatusCreated)
	var personalWorkspace struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, personalResp, &personalWorkspace)
	personalResp.Body.Close()

	ownerDirect := MakeAuthRequestWithToken(t, server, ownerToken, http.MethodGet, fmt.Sprintf("/workspaces/%d", personalWorkspace.ID), nil)
	AssertStatusCode(t, ownerDirect, http.StatusOK)
	ownerDirect.Body.Close()
	assertWorkspaceIDPresence(t, "owner cookie workspace list", getCookieWorkspaceIDs(t, server, ownerToken), personalWorkspace.ID, true)
	assertV1WorkspacePresence(t, getV1Workspaces(t, server, ownerBearerToken), personalWorkspace.ID, true)
	assertWorkspaceIDPresence(t, "owner MCP workspace list", getMCPWorkspaceIDs(t, server, ownerBearerToken), personalWorkspace.ID, true)

	outsiderDirect := MakeAuthRequestWithToken(t, server, outsiderToken, http.MethodGet, fmt.Sprintf("/workspaces/%d", personalWorkspace.ID), nil)
	AssertStatusCode(t, outsiderDirect, http.StatusNotFound)
	outsiderDirect.Body.Close()
	assertWorkspaceIDPresence(t, "outsider cookie workspace list", getCookieWorkspaceIDs(t, server, outsiderToken), personalWorkspace.ID, false)
	assertV1WorkspacePresence(t, getV1Workspaces(t, server, outsiderBearerToken), personalWorkspace.ID, false)
	assertWorkspaceIDPresence(t, "outsider MCP workspace list", getMCPWorkspaceIDs(t, server, outsiderBearerToken), personalWorkspace.ID, false)

	adminDirect := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/workspaces/%d", personalWorkspace.ID), nil)
	AssertStatusCode(t, adminDirect, http.StatusOK)
	adminDirect.Body.Close()
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
