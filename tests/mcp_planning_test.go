// mcp_planning_test covers list_milestones, list_iterations,
// list_custom_fields, and list_recent_activity.
package tests

import (
	"sort"
	"testing"
)

func TestMCP_ListMilestones(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	t.Run("all in alpha", func(t *testing.T) {
		var out struct {
			Milestones []struct {
				ID     int    `json:"id"`
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"milestones"`
		}
		callTool(t, session, "list_milestones", map[string]interface{}{"workspace_id": w.Alpha.ID}, &out)
		got := make([]int, len(out.Milestones))
		for i, m := range out.Milestones {
			got[i] = m.ID
		}
		sort.Ints(got)
		want := []int{w.Milestones.Q1.ID, w.Milestones.Q2.ID, w.Milestones.Backlog.ID, w.Milestones.Released.ID}
		sort.Ints(want)
		if !equalIntSlices(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})

	t.Run("status=in-progress", func(t *testing.T) {
		var out struct {
			Milestones []struct {
				ID     int    `json:"id"`
				Status string `json:"status"`
			} `json:"milestones"`
		}
		callTool(t, session, "list_milestones", map[string]interface{}{
			"workspace_id": w.Alpha.ID,
			"status":       "in-progress",
		}, &out)
		// Q1 was seeded with status="in-progress" — should be exactly that one.
		if len(out.Milestones) != 1 || out.Milestones[0].ID != w.Milestones.Q1.ID {
			t.Fatalf("status=in-progress: got %+v want exactly Q1 (id=%d)", out.Milestones, w.Milestones.Q1.ID)
		}
	})
}

func TestMCP_ListIterations(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	t.Run("all in alpha", func(t *testing.T) {
		var out struct {
			Iterations []struct {
				ID     int    `json:"id"`
				Status string `json:"status"`
			} `json:"iterations"`
		}
		callTool(t, session, "list_iterations", map[string]interface{}{"workspace_id": w.Alpha.ID}, &out)
		got := make([]int, len(out.Iterations))
		for i, it := range out.Iterations {
			got[i] = it.ID
		}
		sort.Ints(got)
		want := []int{w.Iterations.Sprint1.ID, w.Iterations.Sprint2.ID, w.Iterations.Sprint3.ID}
		sort.Ints(want)
		if !equalIntSlices(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})

	t.Run("status=active", func(t *testing.T) {
		var out struct {
			Iterations []struct {
				ID int `json:"id"`
			} `json:"iterations"`
		}
		callTool(t, session, "list_iterations", map[string]interface{}{
			"workspace_id": w.Alpha.ID,
			"status":       "active",
		}, &out)
		if len(out.Iterations) != 1 || out.Iterations[0].ID != w.Iterations.Sprint2.ID {
			t.Fatalf("status=active: got %+v want exactly Sprint2 (id=%d)", out.Iterations, w.Iterations.Sprint2.ID)
		}
	})
}

func TestMCP_ListCustomFields(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	_ = SeedWorld(t, ts)
	session := dialMCP(t, ts)

	// Create a known custom field and confirm list_custom_fields returns it.
	cfID := CreateTestCustomField(t, ts, "world_priority_note", "text", "")

	var out struct {
		CustomFields []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"custom_fields"`
	}
	callTool(t, session, "list_custom_fields", map[string]interface{}{}, &out)

	found := false
	for _, f := range out.CustomFields {
		if f.ID == cfID && f.Name == "world_priority_note" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("seeded custom field id=%d not in list_custom_fields (%+v)", cfID, out.CustomFields)
	}
}

func TestMCP_ListRecentActivity(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	target := w.Items[0]

	// Generate some activity: add a comment + edit the title.
	callTool(t, session, "add_comment", map[string]interface{}{
		"item_id": target.ID,
		"content": "activity sentinel",
	}, nil)
	newTitle := "title for activity check"
	callTool(t, session, "update_item", map[string]interface{}{
		"item_id": target.ID,
		"title":   newTitle,
	}, nil)

	var out struct {
		Changes  []map[string]interface{} `json:"changes"`
		Comments []map[string]interface{} `json:"comments"`
	}
	callTool(t, session, "list_recent_activity", map[string]interface{}{
		"workspace_id": w.Alpha.ID,
		"limit":        100,
	}, &out)

	// At minimum the new comment must show up.
	foundComment := false
	for _, c := range out.Comments {
		if content, _ := c["content"].(string); content == "activity sentinel" {
			foundComment = true
			break
		}
	}
	if !foundComment {
		t.Fatalf("recent activity missing seeded comment (got %d comments, %d changes)", len(out.Comments), len(out.Changes))
	}
}
