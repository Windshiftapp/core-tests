// mcp_items_test exercises every item-family MCP tool against SeedWorld:
// list_items, get_item, search_items, create_item, update_item,
// delete_item, get_item_children, transition_item. Filter resultsets are
// compared to the seed's expected sets (the matrix in fixtures.go is the
// source of truth). State-changing tools verify the change with a
// follow-up read.
package tests

import (
	"sort"
	"strconv"
	"strings"
	"testing"
)

// itemSummary mirrors the JSON shape every items-tool returns in its `items`
// (or top-level) payload. We only consume the few fields the assertions need.
type itemSummary struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	WorkspaceID int    `json:"workspace_id"`
	Status      string `json:"status,omitempty"`
}

func TestMCP_ListItems(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	cases := []struct {
		name string
		args map[string]interface{}
		want []int
	}{
		{
			"all in alpha",
			map[string]interface{}{"workspace_id": w.Alpha.ID, "limit": 200},
			w.ItemsInWorkspace(w.Alpha.ID),
		},
		{
			"status_id=Open",
			map[string]interface{}{"workspace_id": w.Alpha.ID, "status_id": w.Statuses.Open, "limit": 200},
			w.ItemsByStatusName("Open"),
		},
		{
			"status_id=InProgress",
			map[string]interface{}{"workspace_id": w.Alpha.ID, "status_id": w.Statuses.InProgress, "limit": 200},
			w.ItemsByStatusName("InProgress"),
		},
		{
			"assignee_id=Bob",
			map[string]interface{}{"workspace_id": w.Alpha.ID, "assignee_id": w.Users.Bob.ID, "limit": 200},
			intersectInts(w.ItemsByAssignee(w.Users.Bob.ID), w.ItemsInWorkspace(w.Alpha.ID)),
		},
		{
			"CQL filter milestone=Q1",
			map[string]interface{}{
				"workspace_id": w.Alpha.ID,
				"filter":       "milestone = " + strconv.Itoa(w.Milestones.Q1.ID),
				"limit":        200,
			},
			w.ItemsInMilestone("Q1"),
		},
		{
			"CQL filter iteration=Sprint2",
			map[string]interface{}{
				"workspace_id": w.Alpha.ID,
				"filter":       "iteration = " + strconv.Itoa(w.Iterations.Sprint2.ID),
				"limit":        200,
			},
			w.ItemsInIteration("Sprint2"),
		},
		{
			// CQL `label = X` resolves X against labels.name (case-insensitive
			// LIKE). It does NOT take a label ID.
			"CQL filter label=bug",
			map[string]interface{}{
				"workspace_id": w.Alpha.ID,
				"filter":       `label = "` + w.Labels.Bug.Name + `"`,
				"limit":        200,
			},
			intersectInts(w.ItemsByLabel(w.Labels.Bug.ID), w.ItemsInWorkspace(w.Alpha.ID)),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out struct {
				Items []itemSummary `json:"items"`
				Total int           `json:"total"`
			}
			callTool(t, session, "list_items", tc.args, &out)
			gotIDs := make([]int, len(out.Items))
			for i, it := range out.Items {
				gotIDs[i] = it.ID
			}
			sort.Ints(gotIDs)
			want := append([]int(nil), tc.want...)
			sort.Ints(want)
			if !equalIntSlices(gotIDs, want) {
				t.Fatalf("got %v, want %v", gotIDs, want)
			}
		})
	}
}

func TestMCP_GetItem(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	target := w.Items[1] // alpha feature ramp, in progress, alice

	t.Run("by id", func(t *testing.T) {
		var got itemSummary
		callTool(t, session, "get_item", map[string]interface{}{"item_id": target.ID}, &got)
		if got.ID != target.ID || got.Title != target.Title {
			t.Fatalf("got %+v want id=%d title=%q", got, target.ID, target.Title)
		}
	})

	t.Run("by key", func(t *testing.T) {
		var got itemSummary
		callTool(t, session, "get_item", map[string]interface{}{"item_key": target.Key}, &got)
		if got.ID != target.ID {
			t.Fatalf("by key: got id=%d want %d", got.ID, target.ID)
		}
	})
}

func TestMCP_SearchItems(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	// "alpha bug rfc" is unique enough that only one seeded item matches.
	var out struct {
		Items []itemSummary `json:"items"`
		Total int           `json:"total"`
	}
	callTool(t, session, "search_items", map[string]interface{}{
		"query":        "alpha bug rfc",
		"workspace_id": w.Alpha.ID,
	}, &out)

	found := false
	for _, it := range out.Items {
		if it.ID == w.Items[0].ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected seed item %d in search results, got %v", w.Items[0].ID, out.Items)
	}
}

func TestMCP_CreateItem_RoundTrip(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	var created itemSummary
	callTool(t, session, "create_item", map[string]interface{}{
		"workspace_id": w.Alpha.ID,
		"title":        "mcp-created sentinel",
		"assignee_id":  w.Users.Alice.ID,
	}, &created)
	if created.ID == 0 || created.Title != "mcp-created sentinel" {
		t.Fatalf("create_item returned %+v", created)
	}

	// Verify visible from list_items now.
	var listed struct {
		Items []itemSummary `json:"items"`
	}
	callTool(t, session, "list_items", map[string]interface{}{
		"workspace_id": w.Alpha.ID,
		"limit":        200,
	}, &listed)
	if !containsItemID(listed.Items, created.ID) {
		t.Fatalf("created item %d not in list_items result", created.ID)
	}

	var rejected map[string]string
	callTool(t, session, "create_item", map[string]interface{}{
		"workspace_id": w.Alpha.ID,
		"title":        "mcp-rejected-assignee",
		"assignee_id":  1_000_000,
	}, &rejected)
	if rejected["error"] != "create failed: assignee_id: Assignee user not found" {
		t.Fatalf("invalid assignee error = %q, want field-scoped not-found error", rejected["error"])
	}

	callTool(t, session, "list_items", map[string]interface{}{
		"workspace_id": w.Alpha.ID,
		"limit":        200,
	}, &listed)
	for _, item := range listed.Items {
		if item.Title == "mcp-rejected-assignee" {
			t.Fatalf("invalid-assignee MCP item was persisted: %+v", item)
		}
	}
}

func TestMCP_UpdateItem(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	target := w.Items[9] // unstructured

	// update title + assignee
	newTitle := "mcp updated title"
	callTool(t, session, "update_item", map[string]interface{}{
		"item_id":     target.ID,
		"title":       newTitle,
		"assignee_id": w.Users.Bob.ID,
	}, nil)

	// Re-read via get_item.
	var got struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	}
	callTool(t, session, "get_item", map[string]interface{}{"item_id": target.ID}, &got)
	if got.Title != newTitle {
		t.Fatalf("update title: got %q want %q", got.Title, newTitle)
	}
}

func TestMCP_DeleteItem(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	target := w.Items[9]

	callTool(t, session, "delete_item", map[string]interface{}{"item_id": target.ID}, nil)

	// list_items should no longer include the deleted ID.
	var listed struct {
		Items []itemSummary `json:"items"`
	}
	callTool(t, session, "list_items", map[string]interface{}{
		"workspace_id": w.Alpha.ID,
		"limit":        200,
	}, &listed)
	if containsItemID(listed.Items, target.ID) {
		t.Fatalf("deleted item %d still listed", target.ID)
	}
}

func TestMCP_GetItemChildren(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	parent := w.Items[0]
	// Create three children via the MCP tool.
	for i := 0; i < 3; i++ {
		callTool(t, session, "create_item", map[string]interface{}{
			"workspace_id": w.Alpha.ID,
			"title":        "mcp child " + strconv.Itoa(i),
			"parent_id":    parent.ID,
		}, nil)
	}

	raw := callToolRaw(t, session, "get_item_children", map[string]interface{}{"item_id": parent.ID})
	if !strings.Contains(raw, "mcp child") {
		t.Fatalf("get_item_children did not return seeded children. raw=%s", raw)
	}
}

func TestMCP_TransitionItem(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	target := w.Items[0] // currently Open
	callTool(t, session, "transition_item", map[string]interface{}{
		"item_id":      target.ID,
		"to_status_id": w.Statuses.InProgress,
	}, nil)

	var got struct {
		Status string `json:"status"`
	}
	callTool(t, session, "get_item", map[string]interface{}{"item_id": target.ID}, &got)
	if got.Status != "In Progress" {
		t.Fatalf("transition: status=%q want \"In Progress\"", got.Status)
	}
}

func containsItemID(items []itemSummary, id int) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}
