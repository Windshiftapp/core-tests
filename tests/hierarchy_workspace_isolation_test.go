package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestHierarchyReadsHideInaccessibleWorkspaceItems proves the ancestor,
// descendant, and batch-ancestor endpoints disclose only items in workspaces
// the caller can view. Cross-workspace hierarchy links are allowed by design,
// so a visible parent must not leak a child (or ancestor) that lives in a
// gated workspace.
func TestHierarchyReadsHideInaccessibleWorkspaceItems(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	alphaID, _ := CreateTestWorkspace(t, server, "Hierarchy Alpha", shortKey("HALPHA"))
	betaID, _ := CreateTestWorkspace(t, server, "Hierarchy Beta", shortKey("HBETA"))

	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	initiative := RequireItemTypeID(t, itemTypes, "Initiative")
	epic := RequireItemTypeID(t, itemTypes, "Epic")

	// Admin authors both directions of the cross-workspace link while Beta is
	// still open: a Beta parent of an Alpha child, and a Beta child of an Alpha
	// parent.
	betaParent := createHierarchicalItem(t, server, betaID, "beta secret parent", initiative, 0)
	alphaChild := createHierarchicalItem(t, server, alphaID, "alpha child of beta parent", epic, betaParent)

	alphaRoot := createHierarchicalItem(t, server, alphaID, "alpha root", initiative, 0)
	betaChild := createHierarchicalItem(t, server, betaID, "beta secret child of alpha root", epic, alphaRoot)

	// Gate Beta; Bob keeps access to the open Alpha workspace only.
	LockDownWorkspace(t, server, betaID)
	_, bobUser, bobPass := CreateTestUserWithCredentials(t, server, "hierarchy_bob", "hierarchy_bob@example.test")
	bobToken := createTokenWithScopesAsUser(t, server, bobUser, bobPass, []string{"items:read"})

	// Sanity: admin (who can view Beta) sees both cross-workspace items, so the
	// relationships exist and the denial assertions below are not vacuous.
	adminAncestors := fetchAncestors(t, server, "", alphaChild)
	if len(adminAncestors) != 1 || adminAncestors[0] != betaParent {
		t.Fatalf("admin ancestors of alpha child = %v, want [%d]", adminAncestors, betaParent)
	}
	adminDescendants := fetchDescendants(t, server, "", alphaRoot)
	if !hierarchyHasItemID(adminDescendants, betaChild) {
		t.Fatalf("admin descendants of alpha root = %v, want to contain %d", adminDescendants, betaChild)
	}

	t.Run("ancestors", func(t *testing.T) {
		ancestors := fetchAncestors(t, server, bobToken, alphaChild)
		if hierarchyHasItemID(ancestors, betaParent) {
			t.Fatalf("ancestors leaked beta parent %d: %v", betaParent, ancestors)
		}
		if len(ancestors) != 0 {
			t.Fatalf("ancestors of alpha child = %v, want empty after redaction", ancestors)
		}
	})

	t.Run("descendants", func(t *testing.T) {
		descendants := fetchDescendants(t, server, bobToken, alphaRoot)
		if hierarchyHasItemID(descendants, betaChild) {
			t.Fatalf("descendants leaked beta child %d: %v", betaChild, descendants)
		}
	})

	t.Run("batch-ancestors", func(t *testing.T) {
		batch := fetchBatchAncestors(t, server, bobToken, alphaChild)
		if chain := batch[alphaChild]; hierarchyHasItemID(chain, betaParent) {
			t.Fatalf("batch-ancestors leaked beta parent %d: %v", betaParent, chain)
		}
	})
}

// TestReparentChildrenRequiresEditInEveryChildWorkspace proves that a caller
// with item.edit in a parent's workspace cannot detach or move children that
// live in another workspace where the caller has no edit rights. The refusal is
// atomic: the whole request fails and the cross-workspace child is untouched.
func TestReparentChildrenRequiresEditInEveryChildWorkspace(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	alphaID, _ := CreateTestWorkspace(t, server, "Reparent Alpha", shortKey("RALPHA"))
	betaID, _ := CreateTestWorkspace(t, server, "Reparent Beta", shortKey("RBETA"))

	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	initiative := RequireItemTypeID(t, itemTypes, "Initiative")
	epic := RequireItemTypeID(t, itemTypes, "Epic")

	// Gate Beta and give Bob edit rights only in Alpha.
	LockDownWorkspace(t, server, betaID)
	bobID, bobUser, bobPass := CreateTestUserWithCredentials(t, server, "reparent_bob", "reparent_bob@example.test")
	AssignWorkspaceRole(t, server, bobID, alphaID, "Editor")
	bobToken := createTokenWithScopesAsUser(t, server, bobUser, bobPass, []string{"items:read", "items:write"})

	for _, move := range []bool{true, false} {
		name := "detach"
		if move {
			name = "move"
		}
		t.Run(name, func(t *testing.T) {
			alphaParent := createHierarchicalItem(t, server, alphaID, "alpha parent", initiative, 0)
			betaChild := createHierarchicalItem(t, server, betaID, "beta child", epic, alphaParent)

			body := map[string]any{"parent_id": nil}
			if move {
				destination := createHierarchicalItem(t, server, alphaID, "alpha destination", initiative, 0)
				body = map[string]any{"parent_id": destination}
			}

			historyBefore := countParentHistory(t, server, betaChild)

			resp := MakeBearerRequestWithToken(t, server, bobToken, http.MethodPost,
				fmt.Sprintf("/rest/api/v2/items/%d/reparent-children", alphaParent), body)
			defer resp.Body.Close()
			// The child lives in a workspace Bob cannot edit, so the operation is
			// refused with the existence-masking 404 and nothing changes.
			AssertStatusCode(t, resp, http.StatusNotFound)

			assertItemParent(t, server, betaChild, alphaParent)
			if historyAfter := countParentHistory(t, server, betaChild); historyAfter != historyBefore {
				t.Fatalf("parent_id history rows changed from %d to %d after a refused reparent", historyBefore, historyAfter)
			}
		})
	}
}

// fetchAncestors returns the ancestor ids from GET /items/{id}/ancestors. An
// empty token uses the default admin session.
func fetchAncestors(t *testing.T, server *TestServer, token string, itemID int) []int {
	t.Helper()
	var resp *http.Response
	if token == "" {
		resp = MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/ancestors", itemID), nil)
	} else {
		resp = MakeBearerRequestWithToken(t, server, token, http.MethodGet, fmt.Sprintf("/rest/api/v2/items/%d/ancestors", itemID), nil)
	}
	AssertStatusCode(t, resp, http.StatusOK)
	var items []map[string]interface{}
	DecodeJSON(t, resp, &items)
	resp.Body.Close()
	return itemIDsFromMaps(items)
}

// fetchDescendants returns the descendant ids from GET /items/{id}/descendants.
// An empty token uses the default admin session.
func fetchDescendants(t *testing.T, server *TestServer, token string, rootID int) []int {
	t.Helper()
	var resp *http.Response
	if token == "" {
		resp = MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/descendants", rootID), nil)
	} else {
		resp = MakeBearerRequestWithToken(t, server, token, http.MethodGet, fmt.Sprintf("/rest/api/v2/items/%d/descendants", rootID), nil)
	}
	AssertStatusCode(t, resp, http.StatusOK)
	var items []map[string]interface{}
	DecodeJSON(t, resp, &items)
	resp.Body.Close()
	return itemIDsFromMaps(items)
}

// fetchBatchAncestors returns the ancestor ids per requested item id from
// POST /items/batch-ancestors using the given bearer token.
func fetchBatchAncestors(t *testing.T, server *TestServer, token string, ids ...int) map[int][]int {
	t.Helper()
	resp := MakeBearerRequestWithToken(t, server, token, http.MethodPost, "/rest/api/v2/items/batch-ancestors", map[string]any{"ids": ids})
	AssertStatusCode(t, resp, http.StatusOK)
	var entries []map[string]interface{}
	DecodeJSON(t, resp, &entries)
	resp.Body.Close()
	out := make(map[int][]int, len(entries))
	for _, entry := range entries {
		out[intField(entry, "item_id")] = itemIDsFromMaps(toAncestorMaps(entry["ancestors"]))
	}
	return out
}

func itemIDsFromMaps(items []map[string]interface{}) []int {
	ids := make([]int, 0, len(items))
	for _, item := range items {
		ids = append(ids, intField(item, "id"))
	}
	return ids
}

func hierarchyHasItemID(ids []int, want int) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func assertItemParent(t *testing.T, server *TestServer, itemID, wantParentID int) {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d", itemID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var item map[string]interface{}
	DecodeJSON(t, resp, &item)
	parentID, _ := item["parent_id"].(float64)
	if int(parentID) != wantParentID {
		t.Fatalf("item %d parent_id = %v, want %d", itemID, item["parent_id"], wantParentID)
	}
}

func countParentHistory(t *testing.T, server *TestServer, itemID int) int {
	t.Helper()
	var count int
	if err := server.server.DB().QueryRow(
		`SELECT COUNT(*) FROM item_history WHERE item_id = ? AND field_name = 'parent_id'`,
		itemID,
	).Scan(&count); err != nil {
		t.Fatalf("count parent_id history for item %d: %v", itemID, err)
	}
	return count
}
