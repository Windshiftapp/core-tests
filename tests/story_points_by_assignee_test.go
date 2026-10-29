package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestStoryPointsByAssigneeAggregate exercises the v2 aggregate behind the
// "Story Points by Assignee" dashboard widget (GH #255 / WI-1340): only open
// items count, sums group per assignee, results scope to the caller's
// accessible workspaces, and cross-workspace narrowing is refused.
func TestStoryPointsByAssigneeAggregate(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, wsKey := CreateTestWorkspace(t, server, "Story Points Rollup WS", shortKey("SPR"))

	aliceID, _, alicePass := CreateTestUserWithCredentials(t, server, "spalice", "spalice@test.local")
	AssignWorkspaceRole(t, server, aliceID, wsID, "Editor")
	bobID, _, _ := CreateTestUserWithCredentials(t, server, "spbob", "spbob@test.local")
	AssignWorkspaceRole(t, server, bobID, wsID, "Editor")

	// A workspace the caller cannot see must 404 (no existence leak). The
	// everyone-viewer fallback closes once ANY member holds the Viewer role
	// explicitly (permission_cache_builder.go), so seed that before alice's
	// first request fills her accessible-workspace snapshot.
	adminWS, _ := CreateTestWorkspace(t, server, "SP Admin WS", shortKey("SPAW"))
	AssignWorkspaceRole(t, server, bobID, adminWS, "Viewer")

	statuses := lookupDefaultStatuses(t, server, wsID)

	// Alice: 3 open points + 5 open points; Bob: 2 open points; plus a
	// completed item with points (excluded) and a points-less item (excluded).
	createTestItemWithPoints(t, server, wsID, "Alice A", statuses.Open, aliceID, 3)
	createTestItemWithPoints(t, server, wsID, "Alice B", statuses.Open, aliceID, 5)
	createTestItemWithPoints(t, server, wsID, "Bob A", statuses.Open, bobID, 2)
	createTestItemWithPoints(t, server, wsID, "Alice done", statuses.Done, aliceID, 100)

	// CreateAuthCredentialsForUser returns (session cookie, bearer token);
	// CreateBearerTokenForUser misleadingly returns the cookie.
	_, aliceToken := CreateAuthCredentialsForUser(t, server, "spalice", alicePass)

	resp := MakeBearerRequestWithToken(t, server, aliceToken, http.MethodGet, "/rest/api/v2/items/story-points/by-assignee", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var rows []map[string]interface{}
	DecodeJSON(t, resp, &rows)
	if len(rows) != 2 {
		t.Fatalf("rows = %#v, want two assignees", rows)
	}
	// Ordered by total points desc: Alice (8) before Bob (2).
	if int(rows[0]["assignee_id"].(float64)) != aliceID {
		t.Fatalf("first row = %#v, want alice", rows[0])
	}
	if rows[0]["total_points"].(float64) != 8 {
		t.Errorf("alice total = %v, want 8 (completed item excluded)", rows[0]["total_points"])
	}
	if int(rows[0]["item_count"].(float64)) != 2 {
		t.Errorf("alice item_count = %v, want 2", rows[0]["item_count"])
	}
	if rows[1]["total_points"].(float64) != 2 {
		t.Errorf("bob total = %v, want 2", rows[1]["total_points"])
	}

	// Workspace narrowing works for an accessible workspace.
	narrowResp := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/items/story-points/by-assignee?workspace_id=%d", wsID), nil)
	defer narrowResp.Body.Close()
	AssertStatusCode(t, narrowResp, http.StatusOK)

	deniedResp := MakeBearerRequestWithToken(t, server, aliceToken, http.MethodGet, fmt.Sprintf("/rest/api/v2/items/story-points/by-assignee?workspace_id=%d", adminWS), nil)
	defer deniedResp.Body.Close()
	AssertStatusCode(t, deniedResp, http.StatusNotFound)

	_ = wsKey
}

// createTestItemWithPoints creates an item with story points and an assignee.
func createTestItemWithPoints(t *testing.T, server *TestServer, workspaceID int, title string, statusID, assigneeID int, points float64) int {
	t.Helper()

	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")

	body := map[string]interface{}{
		"title":        title,
		"workspace_id": workspaceID,
		"item_type_id": itemTypeID,
		"status_id":    statusID,
		"assignee_id":  assigneeID,
		"story_points": points,
	}
	resp := MakeAuthRequest(t, server, http.MethodPost, "/items", body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)
	return ExtractIDFromResponse(t, result)
}
