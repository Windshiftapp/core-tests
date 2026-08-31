package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestItemsBatchEndpoint exercises GET /api/items/batch — the bulk item fetch
// that backs api.items.getMany(), replacing the per-id GET /items/{id} fan-out
// that could exhaust the DB pool on a collection delta refresh. It verifies the
// response is an array of full item objects, that duplicate ids are tolerated,
// and that ids which don't exist are silently omitted.
func TestItemsBatchEndpoint(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server) // completes setup + admin login; sets SessionCookie

	wsID, _ := CreateTestWorkspace(t, server, "Items Batch WS", shortKey("IBWS"))
	itemA := CreateTestItem(t, server, wsID, "Item A")
	itemB := CreateTestItem(t, server, wsID, "Item B")
	itemC := CreateTestItem(t, server, wsID, "Item C")
	const missing = 999999999

	// Duplicate id + a non-existent id included intentionally.
	endpoint := fmt.Sprintf("/items/batch?ids=%d,%d,%d,%d,%d", itemA, itemB, itemC, itemA, missing)
	resp := MakeAuthRequest(t, server, http.MethodGet, endpoint, nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var got []map[string]interface{}
	DecodeJSON(t, resp, &got)

	byID := make(map[int]map[string]interface{}, len(got))
	for _, it := range got {
		byID[intField(it, "id")] = it
	}

	if len(byID) != 3 {
		t.Fatalf("want 3 distinct items, got %d (ids %v)", len(byID), keysOf(byID))
	}
	for _, id := range []int{itemA, itemB, itemC} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("expected item %d in batch response, got ids %v", id, keysOf(byID))
		}
	}
	if _, ok := byID[missing]; ok {
		t.Fatalf("non-existent id %d should be omitted, got %v", missing, keysOf(byID))
	}

	// Full detail shape: title + workspace_id populated (consumers Object.assign
	// these onto loaded rows, so the batch shape must match GET /items/{id}).
	a := byID[itemA]
	if title, _ := a["title"].(string); title != "Item A" {
		t.Fatalf("item A title: want %q, got %q", "Item A", a["title"])
	}
	if ws := intField(a, "workspace_id"); ws != wsID {
		t.Fatalf("item A workspace_id: want %d, got %d", wsID, ws)
	}
}

// TestItemsBatchEndpoint_NoIDs verifies the endpoint returns an empty array
// (not an error) when no usable ids are supplied.
func TestItemsBatchEndpoint_NoIDs(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	resp := MakeAuthRequest(t, server, http.MethodGet, "/items/batch?ids=", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var got []map[string]interface{}
	DecodeJSON(t, resp, &got)
	if len(got) != 0 {
		t.Fatalf("want empty array, got %v", got)
	}
}

// TestItemsBatchEndpoint_Cap verifies the endpoint rejects more than 500 ids.
func TestItemsBatchEndpoint_Cap(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	ids := make([]string, 501)
	for i := range ids {
		ids[i] = fmt.Sprintf("%d", i+1)
	}
	endpoint := "/items/batch?ids=" + strings.Join(ids, ",")
	resp := MakeAuthRequest(t, server, http.MethodGet, endpoint, nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusBadRequest)
}

// TestItemsBatchEndpoint_PermissionFiltering verifies that a batch request
// silently omits items the caller cannot view (the 404-no-leak contract): the
// request succeeds with 200 and returns only the visible items, never
// disclosing the existence of items in workspaces the user can't access.
func TestItemsBatchEndpoint_PermissionFiltering(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminToken := CreateBearerToken(t, server)
	server.BearerToken = adminToken

	wsA, _ := CreateTestWorkspace(t, server, "Batch Perm A", shortKey("BPA"))
	wsB, _ := CreateTestWorkspace(t, server, "Batch Perm B", shortKey("BPB"))
	LockDownWorkspace(t, server, wsA)
	LockDownWorkspace(t, server, wsB)

	itemA := CreateTestItem(t, server, wsA, "Visible Item")
	itemB := CreateTestItem(t, server, wsB, "Hidden Item")

	userID, username, password := CreateTestUserWithCredentials(t, server, "batch_perm_user", "batch_perm@test.com")
	AssignWorkspaceRole(t, server, userID, wsA, "Editor")
	userToken := CreateBearerTokenForUser(t, server, username, password)

	endpoint := fmt.Sprintf("/items/batch?ids=%d,%d", itemA, itemB)
	resp := MakeAuthRequestWithToken(t, server, userToken, http.MethodGet, endpoint, nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var got []map[string]interface{}
	DecodeJSON(t, resp, &got)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 visible item, got %d", len(got))
	}
	if id := intField(got[0], "id"); id != itemA {
		t.Fatalf("want visible item %d, got %d", itemA, id)
	}
}

func keysOf(m map[int]map[string]interface{}) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
