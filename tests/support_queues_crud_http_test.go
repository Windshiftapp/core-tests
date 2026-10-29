package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func createQueue(t *testing.T, server *TestServer, path string, body map[string]any) map[string]any {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodPost, path, body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var view map[string]any
	DecodeJSON(t, resp, &view)
	return view
}

func listQueuesAt(t *testing.T, server *TestServer, path string) []map[string]any {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodGet, path, nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var queues []map[string]any
	DecodeJSON(t, resp, &queues)
	return queues
}

func queueByID(queues []map[string]any, id int) map[string]any {
	for _, queue := range queues {
		if value, ok := queue["id"].(float64); ok && int(value) == id {
			return queue
		}
	}
	return nil
}

func queueByKey(queues []map[string]any, key string) map[string]any {
	for _, queue := range queues {
		if queue["key"] == key {
			return queue
		}
	}
	return nil
}

func queueCountOf(t *testing.T, queue map[string]any) int {
	t.Helper()
	value, ok := queue["count"].(float64)
	if !ok {
		t.Fatalf("queue %v has no numeric count", queue)
	}
	return int(value)
}

func createQueueCollection(t *testing.T, server *TestServer, workspaceID int, name string) int {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodPost, "/v2/collections", map[string]any{
		"name": name, "workspace_id": workspaceID, "ql_query": "", "is_private": true,
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var doc struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, resp, &doc)
	return doc.ID
}

// TestSupportQueuesWorkspaceCRUDContract covers the custom-queue lifecycle,
// built-in dismissal/restore, ordering, and validation for the workspace
// default scope.
func TestSupportQueuesWorkspaceCRUDContract(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	fix := setupSupportQueueFixture(t, server, "Queue crud")
	base := fmt.Sprintf("/v2/workspaces/%s/queues", fix.workspaceKe)

	queues := listQueuesAt(t, server, base)
	if len(queues) != 7 {
		t.Fatalf("initial catalog length = %d, want 7", len(queues))
	}

	custom := createQueue(t, server, base, map[string]any{
		"name":         "My custom queue",
		"ql_query":     `title ~ "Queue overdue" AND statusCompleted = false`,
		"filter_state": `{"search":"Queue overdue"}`,
	})
	customID := int(custom["id"].(float64))
	if customID == 0 {
		t.Fatal("custom queue has no id")
	}
	if custom["builtin"] != false {
		t.Fatalf("custom queue builtin = %v, want false", custom["builtin"])
	}

	queues = listQueuesAt(t, server, base)
	if len(queues) != 8 {
		t.Fatalf("catalog length after create = %d, want 8", len(queues))
	}
	if last := queues[len(queues)-1]; int(last["id"].(float64)) != customID {
		t.Fatalf("custom queue is not last in the catalog: %v", last)
	}
	customView := queueByID(queues, customID)
	if got := queueCountOf(t, customView); got != 1 {
		t.Fatalf("custom queue count = %d, want 1", got)
	}
	if customView["filter_state"] != `{"search":"Queue overdue"}` {
		t.Fatalf("custom filter_state did not round-trip: %v", customView["filter_state"])
	}

	update := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/queues/%d", customID), map[string]any{
		"name": "Renamed queue", "ql_query": "statusCompleted = false",
	})
	AssertStatusCode(t, update, http.StatusOK)
	update.Body.Close()
	queues = listQueuesAt(t, server, base)
	if renamed := queueByID(queues, customID); renamed["name"] != "Renamed queue" {
		t.Fatalf("updated queue name = %v, want Renamed queue", renamed["name"])
	}

	// Dismissing a built-in persists; restoring brings it back.
	for range 2 {
		dismiss := MakeAuthRequest(t, server, http.MethodPut, base+"/builtins/waiting", map[string]any{"hidden": true})
		AssertStatusCode(t, dismiss, http.StatusOK)
		dismiss.Body.Close()
	}
	if queueByKey(listQueuesAt(t, server, base), "waiting") != nil {
		t.Fatal("waiting is still visible after dismissal")
	}
	restore := MakeAuthRequest(t, server, http.MethodPut, base+"/builtins/waiting", map[string]any{"hidden": false})
	AssertStatusCode(t, restore, http.StatusOK)
	restore.Body.Close()
	if queueByKey(listQueuesAt(t, server, base), "waiting") == nil {
		t.Fatal("waiting was not restored")
	}

	unknown := MakeAuthRequest(t, server, http.MethodPut, base+"/builtins/not-a-queue", map[string]any{"hidden": true})
	AssertStatusCode(t, unknown, http.StatusNotFound)
	unknown.Body.Close()

	// Reorder puts the second custom queue ahead of the first.
	second := createQueue(t, server, base, map[string]any{"name": "Second", "ql_query": "statusCompleted = false"})
	secondID := int(second["id"].(float64))
	order := MakeAuthRequest(t, server, http.MethodPut, base+"/order", map[string]any{"ids": []int{secondID, customID}})
	AssertStatusCode(t, order, http.StatusOK)
	order.Body.Close()
	queues = listQueuesAt(t, server, base)
	firstIndex, secondIndex := -1, -1
	for i, queue := range queues {
		switch int(queue["id"].(float64)) {
		case customID:
			firstIndex = i
		case secondID:
			secondIndex = i
		}
	}
	if secondIndex == -1 || firstIndex == -1 || secondIndex > firstIndex {
		t.Fatalf("reorder positions: second=%d custom=%d, want second before custom", secondIndex, firstIndex)
	}

	del := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/v2/queues/%d", customID), nil)
	AssertStatusCode(t, del, http.StatusNoContent)
	del.Body.Close()
	if queueByID(listQueuesAt(t, server, base), customID) != nil {
		t.Fatal("custom queue still visible after delete")
	}

	for _, body := range []map[string]any{
		{"name": "  ", "ql_query": "statusCompleted = false"},
		{"name": "Bad", "ql_query": "statusCompleted = ((("},
	} {
		resp := MakeAuthRequest(t, server, http.MethodPost, base, body)
		AssertStatusCode(t, resp, http.StatusBadRequest)
		resp.Body.Close()
	}
}

// TestSupportQueuesCollectionScopeIsolation verifies collection and workspace
// scopes keep independent custom queues and built-in dismissals.
func TestSupportQueuesCollectionScopeIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	fix := setupSupportQueueFixture(t, server, "Queue scope")
	workspaceBase := fmt.Sprintf("/v2/workspaces/%s/queues", fix.workspaceKe)
	collectionID := createQueueCollection(t, server, fix.workspaceID, "Queue scope collection")
	collectionBase := fmt.Sprintf("/v2/collections/%d/queues", collectionID)

	workspaceQueue := createQueue(t, server, workspaceBase, map[string]any{"name": "Workspace queue", "ql_query": "statusCompleted = false"})
	collectionQueue := createQueue(t, server, collectionBase, map[string]any{"name": "Collection queue", "ql_query": "statusCompleted = false"})
	workspaceQueueID := int(workspaceQueue["id"].(float64))
	collectionQueueID := int(collectionQueue["id"].(float64))

	if queueByID(listQueuesAt(t, server, workspaceBase), collectionQueueID) != nil {
		t.Fatal("collection queue leaked into the workspace scope")
	}
	if queueByID(listQueuesAt(t, server, collectionBase), workspaceQueueID) != nil {
		t.Fatal("workspace queue leaked into the collection scope")
	}

	dismiss := MakeAuthRequest(t, server, http.MethodPut, workspaceBase+"/builtins/waiting", map[string]any{"hidden": true})
	AssertStatusCode(t, dismiss, http.StatusOK)
	dismiss.Body.Close()
	if queueByKey(listQueuesAt(t, server, workspaceBase), "waiting") != nil {
		t.Fatal("workspace dismissal did not apply")
	}
	if queueByKey(listQueuesAt(t, server, collectionBase), "waiting") == nil {
		t.Fatal("workspace dismissal leaked into the collection scope")
	}

	// A second collection never sees the first collection's custom queues.
	otherCollectionID := createQueueCollection(t, server, fix.workspaceID, "Queue scope other")
	otherBase := fmt.Sprintf("/v2/collections/%d/queues", otherCollectionID)
	if queueByID(listQueuesAt(t, server, otherBase), collectionQueueID) != nil {
		t.Fatal("collection queue leaked into a sibling collection")
	}
}

// TestSupportQueuesPermissionContract pins the read and write denial contract
// for workspace and collection scopes.
func TestSupportQueuesPermissionContract(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	fix := setupSupportQueueFixture(t, server, "Queue permissions")
	base := fmt.Sprintf("/v2/workspaces/%s/queues", fix.workspaceKe)

	viewerID, viewerUser, viewerPass := CreateTestUserWithCredentials(t, server, "queue_viewer", "queue-viewer@test.com")
	AssignWorkspaceRole(t, server, viewerID, fix.workspaceID, "Viewer")
	viewerToken, _ := CreateAuthCredentialsForUser(t, server, viewerUser, viewerPass)

	read := MakeAuthRequestWithToken(t, server, viewerToken, http.MethodGet, base, nil)
	AssertStatusCode(t, read, http.StatusOK)
	read.Body.Close()

	write := MakeAuthRequestWithToken(t, server, viewerToken, http.MethodPost, base, map[string]any{
		"name": "Viewer queue", "ql_query": "statusCompleted = false",
	})
	AssertStatusCode(t, write, http.StatusNotFound)
	write.Body.Close()

	_, strangerUser, strangerPass := CreateTestUserWithCredentials(t, server, "queue_stranger", "queue-stranger@test.com")
	strangerToken, _ := CreateAuthCredentialsForUser(t, server, strangerUser, strangerPass)
	strangerRead := MakeAuthRequestWithToken(t, server, strangerToken, http.MethodGet, base, nil)
	AssertStatusCode(t, strangerRead, http.StatusNotFound)
	strangerRead.Body.Close()

	missing := MakeAuthRequest(t, server, http.MethodGet, "/v2/workspaces/NOSUCHWS/queues", nil)
	AssertStatusCode(t, missing, http.StatusNotFound)
	missing.Body.Close()

	// A non-owner cannot read or write a private collection's queues.
	collectionID := createQueueCollection(t, server, fix.workspaceID, "Queue permission collection")
	collectionBase := fmt.Sprintf("/v2/collections/%d/queues", collectionID)
	collectionRead := MakeAuthRequestWithToken(t, server, viewerToken, http.MethodGet, collectionBase, nil)
	AssertStatusCode(t, collectionRead, http.StatusNotFound)
	collectionRead.Body.Close()
	collectionWrite := MakeAuthRequestWithToken(t, server, viewerToken, http.MethodPost, collectionBase, map[string]any{
		"name": "Nope", "ql_query": "statusCompleted = false",
	})
	AssertStatusCode(t, collectionWrite, http.StatusNotFound)
	collectionWrite.Body.Close()
}

// TestSupportQueuesGlobalCollectionScope verifies a collection that spans
// workspaces owns saved views and counts across the caller's workspaces.
func TestSupportQueuesGlobalCollectionScope(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	fix := setupSupportQueueFixture(t, server, "Queue global")

	// A second workspace contributes to the same saved view's count.
	emailFixtureSeq++
	secondWorkspaceID, _ := CreateTestWorkspace(t, server, "Queue global second", fmt.Sprintf("SQG%d", emailFixtureSeq))
	configSetID := GetDefaultConfigurationSet(t, server)
	AssociateWorkspaceWithConfigSet(t, server, secondWorkspaceID, configSetID)
	itemTypes := GetItemTypes(t, server, configSetID)
	secondItem := MakeAuthRequest(t, server, http.MethodPost, "/items", map[string]any{
		"title": "Queue global second item", "workspace_id": secondWorkspaceID,
		"item_type_id": itemTypes["Task"],
	})
	AssertStatusCode(t, secondItem, http.StatusCreated)
	secondItem.Body.Close()

	// A global collection (no workspace) owns a saved view.
	collectionResp := MakeAuthRequest(t, server, http.MethodPost, "/v2/collections", map[string]any{
		"name": "Global saved views", "ql_query": "", "is_private": false,
	})
	AssertStatusCode(t, collectionResp, http.StatusCreated)
	var collection struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, collectionResp, &collection)
	collectionResp.Body.Close()
	collectionBase := fmt.Sprintf("/v2/collections/%d/queues", collection.ID)

	view := createQueue(t, server, collectionBase, map[string]any{
		"name": "Open across workspaces", "ql_query": "statusCompleted = false",
	})
	viewID := int(view["id"].(float64))

	// The view's count spans both workspaces: four unfinished fixture tickets
	// plus the second workspace's item.
	list := listQueuesAt(t, server, collectionBase)
	if got := queueCountOf(t, queueByID(list, viewID)); got != 5 {
		t.Fatalf("global saved view count = %d, want 5", got)
	}

	// Scope isolation: the view is not a workspace default view.
	workspaceBase := fmt.Sprintf("/v2/workspaces/%s/queues", fix.workspaceKe)
	if queueByID(listQueuesAt(t, server, workspaceBase), viewID) != nil {
		t.Fatal("global collection view leaked into the workspace scope")
	}

	// A non-owner cannot read or write a private global collection's views.
	privateResp := MakeAuthRequest(t, server, http.MethodPost, "/v2/collections", map[string]any{
		"name": "Private global views", "ql_query": "", "is_private": true,
	})
	AssertStatusCode(t, privateResp, http.StatusCreated)
	var privateCollection struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, privateResp, &privateCollection)
	privateResp.Body.Close()
	privateBase := fmt.Sprintf("/v2/collections/%d/queues", privateCollection.ID)

	_, strangerUser, strangerPass := CreateTestUserWithCredentials(t, server, "queue_global_stranger", "queue-global-stranger@test.com")
	strangerToken, _ := CreateAuthCredentialsForUser(t, server, strangerUser, strangerPass)
	strangerRead := MakeAuthRequestWithToken(t, server, strangerToken, http.MethodGet, privateBase, nil)
	AssertStatusCode(t, strangerRead, http.StatusNotFound)
	strangerRead.Body.Close()
	strangerWrite := MakeAuthRequestWithToken(t, server, strangerToken, http.MethodPost, privateBase, map[string]any{
		"name": "Nope", "ql_query": "statusCompleted = false",
	})
	AssertStatusCode(t, strangerWrite, http.StatusNotFound)
	strangerWrite.Body.Close()

	// Restrict both workspaces with an explicit role so a user with no role
	// has no accessible workspace. The public global collection stays readable,
	// but its count must be zero rather than a cross-workspace total.
	bystanderID, _, _ := CreateTestUserWithCredentials(t, server, "queue_global_bystander", "queue-global-bystander@test.com")
	AssignWorkspaceRole(t, server, bystanderID, fix.workspaceID, "Viewer")
	AssignWorkspaceRole(t, server, bystanderID, secondWorkspaceID, "Viewer")

	strangerPublicRead := MakeAuthRequestWithToken(t, server, strangerToken, http.MethodGet, collectionBase, nil)
	AssertStatusCode(t, strangerPublicRead, http.StatusOK)
	var strangerQueues []map[string]any
	DecodeJSON(t, strangerPublicRead, &strangerQueues)
	strangerPublicRead.Body.Close()
	if got := queueCountOf(t, queueByID(strangerQueues, viewID)); got != 0 {
		t.Fatalf("stranger global saved view count = %d, want 0", got)
	}
}
