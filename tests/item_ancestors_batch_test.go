package tests

import (
	"net/http"
	"testing"
)

// createHierarchicalItem creates an item with an explicit parent via the
// production API so parent/hierarchy validation stays in the loop.
func createHierarchicalItem(t *testing.T, server *TestServer, workspaceID int, title string, itemTypeID int, parentID int) int {
	t.Helper()

	itemData := map[string]interface{}{
		"title":        title,
		"workspace_id": workspaceID,
		"item_type_id": itemTypeID,
	}
	if parentID != 0 {
		itemData["parent_id"] = parentID
	}

	resp := MakeAuthRequest(t, server, http.MethodPost, "/items", itemData)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	var created map[string]interface{}
	DecodeJSON(t, resp, &created)
	return intField(created, "id")
}

// TestItemsBatchAncestorsEndpoint exercises POST /api/items/batch-ancestors —
// the bulk ancestor-chain fetch that backs the tree view's hierarchy repair.
// It verifies chains come back root -> parent excluding the item itself, that
// root items get an empty chain, and that unknown ids are silently omitted.
func TestItemsBatchAncestorsEndpoint(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Ancestors Batch WS", shortKey("ABWS"))
	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)

	root := createHierarchicalItem(t, server, wsID, "Root Epic", itemTypes["Epic"], 0)
	mid := createHierarchicalItem(t, server, wsID, "Mid Story", itemTypes["Story"], root)
	leaf := createHierarchicalItem(t, server, wsID, "Leaf Task", itemTypes["Task"], mid)
	const missing = 999999999

	// Duplicate id + non-existent id included intentionally.
	resp := MakeAuthRequest(t, server, http.MethodPost, "/v2/items/batch-ancestors", map[string]any{
		"ids": []int{leaf, root, mid, leaf, missing},
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var got []map[string]interface{}
	DecodeJSON(t, resp, &got)

	byItemID := make(map[int][]map[string]interface{}, len(got))
	for _, entry := range got {
		byItemID[intField(entry, "item_id")] = toAncestorMaps(entry["ancestors"])
	}

	if len(got) != 3 {
		t.Fatalf("want entries for the 3 distinct requested ids, got %d", len(got))
	}

	// Leaf chain is the full root -> parent walk, excluding the leaf itself.
	leafChain := byItemID[leaf]
	if len(leafChain) != 2 {
		t.Fatalf("leaf chain: want 2 ancestors [root, mid], got %v", leafChain)
	}
	if id := intField(leafChain[0], "id"); id != root {
		t.Fatalf("leaf chain[0]: want root %d, got %d", root, id)
	}
	if id := intField(leafChain[1], "id"); id != mid {
		t.Fatalf("leaf chain[1]: want mid %d, got %d", mid, id)
	}
	if title, _ := leafChain[0]["title"].(string); title != "Root Epic" {
		t.Fatalf("leaf chain[0] title: want %q, got %q", "Root Epic", title)
	}
	// Chains must carry parent_id so the view can nest the fetched rows.
	if pid := intField(leafChain[1], "parent_id"); pid != root {
		t.Fatalf("mid ancestor parent_id: want %d, got %d", root, pid)
	}

	// Mid chain is just the root; root has an empty chain.
	if chain := byItemID[mid]; len(chain) != 1 || intField(chain[0], "id") != root {
		t.Fatalf("mid chain: want [root %d], got %v", root, chain)
	}
	if chain := byItemID[root]; len(chain) != 0 {
		t.Fatalf("root chain: want empty, got %v", chain)
	}
	if _, ok := byItemID[missing]; ok {
		t.Fatalf("non-existent id %d should be omitted", missing)
	}
}

// TestItemsBatchAncestorsEndpoint_DeepChain verifies chains span multiple
// hierarchy levels in order (the tree nests fetched context from them).
func TestItemsBatchAncestorsEndpoint_DeepChain(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Ancestors Deep WS", shortKey("ADWS"))
	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)

	l1 := createHierarchicalItem(t, server, wsID, "Level 1", itemTypes["Epic"], 0)
	l2 := createHierarchicalItem(t, server, wsID, "Level 2", itemTypes["Story"], l1)
	l3 := createHierarchicalItem(t, server, wsID, "Level 3", itemTypes["Task"], l2)
	l4 := createHierarchicalItem(t, server, wsID, "Level 4", itemTypes["Sub-task"], l3)

	resp := MakeAuthRequest(t, server, http.MethodPost, "/v2/items/batch-ancestors", map[string]any{
		"ids": []int{l4},
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var got []map[string]interface{}
	DecodeJSON(t, resp, &got)

	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	chain := toAncestorMaps(got[0]["ancestors"])
	wantIDs := []int{l1, l2, l3}
	if len(chain) != len(wantIDs) {
		t.Fatalf("chain: want %d ancestors %v, got %v", len(wantIDs), wantIDs, chain)
	}
	for i, want := range wantIDs {
		if id := intField(chain[i], "id"); id != want {
			t.Fatalf("chain[%d]: want %d, got %d", i, want, id)
		}
	}
}

// TestItemsBatchAncestorsEndpoint_NoIDs verifies no usable ids yields an empty
// array rather than an error.
func TestItemsBatchAncestorsEndpoint_NoIDs(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	resp := MakeAuthRequest(t, server, http.MethodPost, "/v2/items/batch-ancestors", map[string]any{"ids": []int{}})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var got []map[string]interface{}
	DecodeJSON(t, resp, &got)
	if len(got) != 0 {
		t.Fatalf("want empty array, got %v", got)
	}
}

// TestItemsBatchAncestorsEndpoint_Cap verifies the endpoint rejects more than
// 500 ids like the item batch endpoint.
func TestItemsBatchAncestorsEndpoint_Cap(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	ids := make([]int, 501)
	for i := range ids {
		ids[i] = i + 1
	}
	resp := MakeAuthRequest(t, server, http.MethodPost, "/v2/items/batch-ancestors", map[string]any{"ids": ids})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusBadRequest)
}

// TestItemsBatchAncestorsEndpoint_PermissionFiltering verifies items the
// caller cannot view are silently omitted (no existence leak), matching the
// item batch contract.
func TestItemsBatchAncestorsEndpoint_PermissionFiltering(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminToken := CreateBearerToken(t, server)
	server.BearerToken = adminToken

	wsA, _ := CreateTestWorkspace(t, server, "Ancestors Perm A", shortKey("APa"))
	wsB, _ := CreateTestWorkspace(t, server, "Ancestors Perm B", shortKey("APb"))
	LockDownWorkspace(t, server, wsA)
	LockDownWorkspace(t, server, wsB)

	visible := CreateTestItem(t, server, wsA, "Visible Item")
	hidden := CreateTestItem(t, server, wsB, "Hidden Item")

	userID, username, password := CreateTestUserWithCredentials(t, server, "anc_batch_perm", "anc_batch@test.com")
	AssignWorkspaceRole(t, server, userID, wsA, "Editor")
	userToken := CreateBearerTokenForUser(t, server, username, password)

	resp := MakeAuthRequestWithToken(t, server, userToken, http.MethodPost, "/v2/items/batch-ancestors", map[string]any{"ids": []int{visible, hidden}})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var got []map[string]interface{}
	DecodeJSON(t, resp, &got)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 visible entry, got %d", len(got))
	}
	if id := intField(got[0], "item_id"); id != visible {
		t.Fatalf("want visible item %d, got %d", visible, id)
	}
}

func toAncestorMaps(raw interface{}) []map[string]interface{} {
	ancestors, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(ancestors))
	for _, a := range ancestors {
		m, ok := a.(map[string]interface{})
		if !ok {
			continue
		}
		out = append(out, m)
	}
	return out
}
