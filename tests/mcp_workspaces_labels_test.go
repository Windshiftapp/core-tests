// mcp_workspaces_labels_test covers list_workspaces, get_workspace,
// list_labels, and set_item_labels. Each tool is exercised against
// SeedWorld; resultsets are compared to the typed handles in fixtures.go.
package tests

import (
	"sort"
	"testing"
)

func TestMCP_ListWorkspaces(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	var out struct {
		Workspaces []struct {
			ID  int    `json:"id"`
			Key string `json:"key"`
		} `json:"workspaces"`
	}
	callTool(t, session, "list_workspaces", map[string]interface{}{}, &out)

	got := map[int]string{}
	for _, ws := range out.Workspaces {
		got[ws.ID] = ws.Key
	}
	if got[w.Alpha.ID] != w.Alpha.Key {
		t.Fatalf("Alpha %d not in list_workspaces (got %v)", w.Alpha.ID, got)
	}
	if got[w.Beta.ID] != w.Beta.Key {
		t.Fatalf("Beta %d not in list_workspaces (got %v)", w.Beta.ID, got)
	}
}

func TestMCP_GetWorkspace(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	var got struct {
		ID  int    `json:"id"`
		Key string `json:"key"`
	}
	callTool(t, session, "get_workspace", map[string]interface{}{"workspace_id": w.Alpha.ID}, &got)
	if got.ID != w.Alpha.ID || got.Key != w.Alpha.Key {
		t.Fatalf("get_workspace: %+v want id=%d key=%s", got, w.Alpha.ID, w.Alpha.Key)
	}
}

func TestMCP_ListLabels(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	var out struct {
		Labels []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"labels"`
	}
	callTool(t, session, "list_labels", map[string]interface{}{"workspace_id": w.Alpha.ID}, &out)

	want := []int{w.Labels.Bug.ID, w.Labels.Feature.ID, w.Labels.Tech.ID, w.Labels.Customer.ID}
	sort.Ints(want)
	got := make([]int, len(out.Labels))
	for i, l := range out.Labels {
		got[i] = l.ID
	}
	sort.Ints(got)
	if !equalIntSlices(got, want) {
		t.Fatalf("list_labels: got %v want %v", got, want)
	}
}

func TestMCP_SetItemLabels(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	target := w.Items[9] // unstructured: starts with no labels
	desired := []int{w.Labels.Tech.ID, w.Labels.Feature.ID}

	var out struct {
		ItemID   int   `json:"item_id"`
		LabelIDs []int `json:"label_ids"`
		Updated  bool  `json:"updated"`
	}
	callTool(t, session, "set_item_labels", map[string]interface{}{
		"item_id":   target.ID,
		"label_ids": desired,
	}, &out)
	if !out.Updated {
		t.Fatalf("set_item_labels did not report updated=true: %+v", out)
	}

	// Re-list items by Tech label and confirm target is included.
	var listed struct {
		Items []itemSummary `json:"items"`
	}
	callTool(t, session, "list_items", map[string]interface{}{
		"workspace_id": w.Alpha.ID,
		"filter":       `label = "` + w.Labels.Tech.Name + `"`,
		"limit":        200,
	}, &listed)
	if !containsItemID(listed.Items, target.ID) {
		t.Fatalf("after set_item_labels, item %d missing from label=tech-debt list (got %v)", target.ID, listed.Items)
	}
}
