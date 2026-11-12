package tests

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

func decodeV2ItemList(t *testing.T, resp *http.Response) []map[string]interface{} {
	t.Helper()
	var document struct {
		Data []map[string]interface{} `json:"data"`
	}
	DecodeJSON(t, resp, &document)
	return document.Data
}

// TestItemListIncludeHasChildren verifies the opt-in has_children projection:
// a parent reports true, a leaf omits the field, and the default payload does
// not compute it at all.
func TestItemListIncludeHasChildren(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "HasChildren WS", shortKey("HCWS"))
	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)

	parent := createHierarchicalItem(t, server, wsID, "Parent Epic", itemTypes["Epic"], 0)
	child := createHierarchicalItem(t, server, wsID, "Child Story", itemTypes["Story"], parent)

	resp := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/items?workspace_id=%d&include_has_children=true", wsID), nil)
	items := decodeV2ItemList(t, resp)
	resp.Body.Close()

	byID := make(map[int]map[string]interface{}, len(items))
	for _, item := range items {
		byID[intField(item, "id")] = item
	}
	if len(byID) != 2 {
		t.Fatalf("want 2 items, got %d", len(byID))
	}
	if got := byID[parent]["has_children"]; got != true {
		t.Fatalf("parent has_children = %v, want true", got)
	}
	if _, present := byID[child]["has_children"]; present {
		t.Fatalf("leaf must omit has_children, got %v", byID[child]["has_children"])
	}

	resp = MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/items?workspace_id=%d", wsID), nil)
	items = decodeV2ItemList(t, resp)
	resp.Body.Close()
	for _, item := range items {
		if _, present := item["has_children"]; present {
			t.Fatalf("has_children must be opt-in, got %v on item %v", item["has_children"], item["id"])
		}
	}
}

// TestItemLevelCountsEndpoint verifies the per-level count read matches the
// filtered item set.
func TestItemLevelCountsEndpoint(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Level Counts WS", shortKey("LCWS"))
	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)

	epic := createHierarchicalItem(t, server, wsID, "Level Epic", itemTypes["Epic"], 0)
	storyA := createHierarchicalItem(t, server, wsID, "Level Story A", itemTypes["Story"], epic)
	createHierarchicalItem(t, server, wsID, "Level Story B", itemTypes["Story"], epic)
	createHierarchicalItem(t, server, wsID, "Level Task", itemTypes["Task"], storyA)

	resp := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/items/level-counts?workspace_id=%d", wsID), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var document struct {
		Data []map[string]interface{} `json:"data"`
	}
	DecodeJSON(t, resp, &document)
	resp.Body.Close()

	counts := make(map[int]int, len(document.Data))
	names := make(map[int]string, len(document.Data))
	for _, row := range document.Data {
		counts[intField(row, "level")] = intField(row, "total")
		if name, ok := row["name"].(string); ok {
			names[intField(row, "level")] = name
		}
	}
	if counts[1] != 1 || counts[2] != 2 || counts[3] != 1 {
		t.Fatalf("level counts = %v, want Epic=1 Story=2 Task=1", counts)
	}
	if names[1] != "Epic" || names[2] != "Story" {
		t.Fatalf("level names = %v, want Epic/Story", names)
	}

	filtered := url.QueryEscape(`title = "Level Story A"`)
	resp = MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/items/level-counts?workspace_id=%d&sub_ql=%s", wsID, filtered), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	document.Data = nil
	DecodeJSON(t, resp, &document)
	resp.Body.Close()
	counts = make(map[int]int, len(document.Data))
	for _, row := range document.Data {
		counts[intField(row, "level")] = intField(row, "total")
	}
	if counts[2] != 1 {
		t.Fatalf("filtered level counts = %v, want only Story=1", counts)
	}

	// A completion filter (status_categories) must resolve in the count query.
	completionFilter := url.QueryEscape("status_completed = false")
	resp = MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/items/level-counts?workspace_id=%d&sub_ql=%s", wsID, completionFilter), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	document.Data = nil
	DecodeJSON(t, resp, &document)
	resp.Body.Close()
	counts = make(map[int]int, len(document.Data))
	for _, row := range document.Data {
		counts[intField(row, "level")] = intField(row, "total")
	}
	if counts[1] != 1 || counts[2] != 2 || counts[3] != 1 {
		t.Fatalf("completion-filtered counts = %v, want Epic=1 Story=2 Task=1", counts)
	}
}

// TestItemHierarchyEndpoint exercises the ancestor-scoped descendant read:
// ancestry across levels, breadth-first truncation, per-root cap, depth guard,
// and flattening a matching descendant under a filtered-out intermediate.
func TestItemHierarchyEndpoint(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Hierarchy Read WS", shortKey("HRWS"))
	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)

	epic := createHierarchicalItem(t, server, wsID, "Hierarchy Epic", itemTypes["Epic"], 0)
	storyA := createHierarchicalItem(t, server, wsID, "Hierarchy Story A", itemTypes["Story"], epic)
	storyB := createHierarchicalItem(t, server, wsID, "Hierarchy Story B", itemTypes["Story"], epic)
	task := createHierarchicalItem(t, server, wsID, "Hierarchy Task", itemTypes["Task"], storyA)
	subtask := createHierarchicalItem(t, server, wsID, "Hierarchy Sub-task", itemTypes["Sub-task"], task)

	post := func(body map[string]any) []map[string]interface{} {
		t.Helper()
		resp := MakeAuthRequest(t, server, http.MethodPost, "/v2/items/hierarchy", body)
		AssertStatusCode(t, resp, http.StatusOK)
		var document struct {
			Data []map[string]interface{} `json:"data"`
		}
		DecodeJSON(t, resp, &document)
		resp.Body.Close()
		return document.Data
	}

	groups := post(map[string]any{
		"root_ids": []int{epic}, "workspace_id": wsID, "max_depth": 0, "per_root_limit": 100,
	})
	if len(groups) != 1 {
		t.Fatalf("want 1 root group, got %d", len(groups))
	}

	// The browser client sends explicit nulls for the unset scope fields; the
	// decoder must accept that shape.
	groups = post(map[string]any{
		"root_ids": []int{epic}, "workspace_id": wsID, "collection_id": nil,
		"sub_ql": nil, "ql": nil, "max_depth": 0, "per_root_limit": 100, "per_root_offset": 0,
	})
	if len(groups) != 1 {
		t.Fatalf("null-scope shape: want 1 root group, got %d", len(groups))
	}
	group := groups[0]
	if intField(group, "root_id") != epic {
		t.Fatalf("root_id = %v, want %d", group["root_id"], epic)
	}
	if total := intField(group, "total"); total != 4 {
		t.Fatalf("total = %d, want 4", total)
	}
	if group["has_more"] != false {
		t.Fatalf("has_more = %v, want false", group["has_more"])
	}
	depthByID := map[int]int{}
	for _, item := range toAncestorMaps(group["items"]) {
		depthByID[intField(item, "id")] = intField(item, "depth")
	}
	if depthByID[storyA] != 1 || depthByID[storyB] != 1 || depthByID[task] != 2 || depthByID[subtask] != 3 {
		t.Fatalf("depths = %v, want story=1 task=2 sub-task=3", depthByID)
	}

	groups = post(map[string]any{
		"root_ids": []int{epic}, "workspace_id": wsID, "max_depth": 1, "per_root_limit": 100,
	})
	group = groups[0]
	if total := intField(group, "total"); total != 2 {
		t.Fatalf("max_depth=1 total = %d, want 2", total)
	}
	for _, item := range toAncestorMaps(group["items"]) {
		if intField(item, "depth") != 1 {
			t.Fatalf("max_depth=1 returned depth %v", item["depth"])
		}
	}

	// Breadth-first truncation keeps shallow levels whole: a cap of 2 returns
	// both stories and no task, so no child is orphaned.
	groups = post(map[string]any{
		"root_ids": []int{epic}, "workspace_id": wsID, "max_depth": 0, "per_root_limit": 2,
	})
	group = groups[0]
	if group["has_more"] != true {
		t.Fatalf("capped group must report has_more, got %v", group["has_more"])
	}
	if total := intField(group, "total"); total != 4 {
		t.Fatalf("capped total = %d, want 4", total)
	}
	capped := toAncestorMaps(group["items"])
	if len(capped) != 2 {
		t.Fatalf("capped items = %d, want 2", len(capped))
	}
	for _, item := range capped {
		if intField(item, "depth") != 1 {
			t.Fatalf("breadth-first cap returned depth %v, want 1", item["depth"])
		}
	}

	// Per-root offset pages within a root: offset 2 skips the two stories and
	// returns the deeper rows, so a per-root "Load more" can continue.
	groups = post(map[string]any{
		"root_ids": []int{epic}, "workspace_id": wsID, "max_depth": 0,
		"per_root_limit": 2, "per_root_offset": 2,
	})
	group = groups[0]
	offsetItems := toAncestorMaps(group["items"])
	if len(offsetItems) != 2 {
		t.Fatalf("offset items = %d, want 2", len(offsetItems))
	}
	for _, item := range offsetItems {
		if intField(item, "depth") < 2 {
			t.Fatalf("offset skipped shallow rows, got depth %v", item["depth"])
		}
	}

	// A completion filter references status_categories; the ranked CTE must
	// carry the same joins the list query does.
	groups = post(map[string]any{
		"root_ids": []int{epic}, "workspace_id": wsID, "max_depth": 0, "per_root_limit": 100,
		"sub_ql": "status_completed = false",
	})
	if total := intField(groups[0], "total"); total != 4 {
		t.Fatalf("status_completed filter total = %d, want 4", total)
	}

	// A QL that excludes the intermediate story still returns the matching task
	// flattened under the root.
	groups = post(map[string]any{
		"root_ids": []int{epic}, "workspace_id": wsID, "max_depth": 0, "per_root_limit": 100,
		"sub_ql": `title = "Hierarchy Task"`,
	})
	group = groups[0]
	matched := toAncestorMaps(group["items"])
	if len(matched) != 1 {
		t.Fatalf("filtered items = %d, want 1", len(matched))
	}
	if id := intField(matched[0], "id"); id != task {
		t.Fatalf("filtered item id = %d, want task %d", id, task)
	}
	if depth := intField(matched[0], "depth"); depth != 2 {
		t.Fatalf("flattened task depth = %d, want 2", depth)
	}
}

// TestItemHierarchyEndpointPermissionFiltering verifies a root in a workspace
// the caller cannot view yields no descendants (no cross-workspace leak).
func TestItemHierarchyEndpointPermissionFiltering(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminToken := CreateBearerToken(t, server)
	server.BearerToken = adminToken

	wsA, _ := CreateTestWorkspace(t, server, "Hierarchy Perm A", shortKey("HPA"))
	wsB, _ := CreateTestWorkspace(t, server, "Hierarchy Perm B", shortKey("HPB"))
	LockDownWorkspace(t, server, wsA)
	LockDownWorkspace(t, server, wsB)

	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	parent := createHierarchicalItem(t, server, wsA, "Visible Parent", itemTypes["Epic"], 0)
	child := createHierarchicalItem(t, server, wsB, "Hidden Child", itemTypes["Story"], parent)

	userID, username, password := CreateTestUserWithCredentials(t, server, "hier_read_perm", "hier_read_perm@test.com")
	AssignWorkspaceRole(t, server, userID, wsA, "Editor")
	userToken := createTokenWithScopesAsUser(t, server, username, password, []string{"items:read"})

	resp := MakeBearerRequestWithToken(t, server, userToken, http.MethodPost, "/rest/api/v2/items/hierarchy",
		map[string]any{"root_ids": []int{parent}, "workspace_id": wsA, "max_depth": 0, "per_root_limit": 100})
	AssertStatusCode(t, resp, http.StatusOK)
	var document struct {
		Data []map[string]interface{} `json:"data"`
	}
	DecodeJSON(t, resp, &document)
	resp.Body.Close()

	if len(document.Data) != 1 {
		t.Fatalf("want 1 group, got %d", len(document.Data))
	}
	for _, item := range toAncestorMaps(document.Data[0]["items"]) {
		if intField(item, "id") == child {
			t.Fatalf("hierarchy read leaked hidden child %d", child)
		}
	}
	if total := intField(document.Data[0], "total"); total != 0 {
		t.Fatalf("visible parent total = %d, want 0", total)
	}
}
