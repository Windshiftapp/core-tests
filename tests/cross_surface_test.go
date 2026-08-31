// cross_surface_test.go asserts that the ws CLI and the MCP server return
// equivalent resultsets for the read-paths both expose. The two adapters
// (Cobra → REST API client, MCP tool → service layer) live in different
// code paths and could drift silently — these tests are the cheapest
// insurance against that.
package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"testing"

	"windshift/internal/wscli"
)

// TestCrossSurface_TaskList_vs_ListItems verifies `ws task ls -w X` and
// MCP `list_items workspace_id=X` return the same item-ID set.
func TestCrossSurface_TaskList_vs_ListItems(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	// CLI side.
	cliIDs := cliListAlpha(t, ts, w)

	// MCP side.
	session := dialMCP(t, ts)
	var mcpResp struct {
		Items []itemSummary `json:"items"`
	}
	callTool(t, session, "list_items", map[string]interface{}{
		"workspace_id": w.Alpha.ID,
		"limit":        200,
	}, &mcpResp)
	mcpIDs := make([]int, len(mcpResp.Items))
	for i, it := range mcpResp.Items {
		mcpIDs[i] = it.ID
	}
	sort.Ints(mcpIDs)

	if !equalIntSlices(cliIDs, mcpIDs) {
		t.Fatalf("CLI and MCP disagree:\n  CLI: %v\n  MCP: %v", cliIDs, mcpIDs)
	}
}

// TestCrossSurface_AssigneeFilter compares the assignee filter on both
// surfaces. The CLI uses `--assignee=N`, MCP uses `assignee_id=N`.
func TestCrossSurface_AssigneeFilter(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	for _, user := range []UserFx{w.Users.Alice, w.Users.Bob} {
		t.Run(user.Username, func(t *testing.T) {
			cliIDs := cliListByAssignee(t, ts, w, user.ID)

			session := dialMCP(t, ts)
			var resp struct {
				Items []itemSummary `json:"items"`
			}
			callTool(t, session, "list_items", map[string]interface{}{
				"workspace_id": w.Alpha.ID,
				"assignee_id":  user.ID,
				"limit":        200,
			}, &resp)
			mcpIDs := make([]int, len(resp.Items))
			for i, it := range resp.Items {
				mcpIDs[i] = it.ID
			}
			sort.Ints(mcpIDs)

			if !equalIntSlices(cliIDs, mcpIDs) {
				t.Fatalf("CLI and MCP disagree for assignee=%d:\n  CLI: %v\n  MCP: %v", user.ID, cliIDs, mcpIDs)
			}
		})
	}
}

// TestCrossSurface_StatusFilter compares the status filter. CLI uses
// `-s ID`, MCP uses `status_id=N`.
func TestCrossSurface_StatusFilter(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	for name, statusID := range map[string]int{
		"Open":       w.Statuses.Open,
		"InProgress": w.Statuses.InProgress,
		"Done":       w.Statuses.Done,
	} {
		t.Run(name, func(t *testing.T) {
			cliIDs := cliListByStatus(t, ts, w, statusID)

			session := dialMCP(t, ts)
			var resp struct {
				Items []itemSummary `json:"items"`
			}
			callTool(t, session, "list_items", map[string]interface{}{
				"workspace_id": w.Alpha.ID,
				"status_id":    statusID,
				"limit":        200,
			}, &resp)
			mcpIDs := make([]int, len(resp.Items))
			for i, it := range resp.Items {
				mcpIDs[i] = it.ID
			}
			sort.Ints(mcpIDs)

			if !equalIntSlices(cliIDs, mcpIDs) {
				t.Fatalf("CLI and MCP disagree for status=%d:\n  CLI: %v\n  MCP: %v", statusID, cliIDs, mcpIDs)
			}
		})
	}
}

// TestCrossSurface_Workspaces compares workspace listings. The CLI returns a
// paginated envelope, MCP returns `{workspaces: [...]}` — both should agree
// on the set of accessible workspace IDs.
func TestCrossSurface_Workspaces(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	_ = SeedWorld(t, ts)

	// CLI: ws workspace ls -o json
	var so, se bytes.Buffer
	code := wscli.Run(context.Background(),
		[]string{"workspace", "ls", "-o", "json"}, nil, &so, &se,
		map[string]string{"WS_URL": ts.BaseURL, "WS_TOKEN": ts.BearerToken},
	)
	if code != 0 {
		t.Fatalf("CLI workspace ls: code=%d stderr=%s", code, se.String())
	}
	cliIDs := workspaceIDsFromCLI(t, so.Bytes())

	// MCP: list_workspaces
	session := dialMCP(t, ts)
	var resp struct {
		Workspaces []struct {
			ID int `json:"id"`
		} `json:"workspaces"`
	}
	callTool(t, session, "list_workspaces", map[string]interface{}{}, &resp)
	mcpIDs := make([]int, len(resp.Workspaces))
	for i, ws := range resp.Workspaces {
		mcpIDs[i] = ws.ID
	}
	sort.Ints(mcpIDs)
	sort.Ints(cliIDs)

	if !equalIntSlices(cliIDs, mcpIDs) {
		t.Fatalf("workspace lists disagree:\n  CLI: %v\n  MCP: %v", cliIDs, mcpIDs)
	}
}

func cliListAlpha(t *testing.T, ts *TestServer, w *World) []int {
	t.Helper()
	out, stderr, code := runWS(t, ts, "task", "ls", "-w", w.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	ids, err := idsFromJSONListItems(out)
	if err != nil {
		t.Fatalf("decode CLI: %v", err)
	}
	return ids
}

func cliListByAssignee(t *testing.T, ts *TestServer, w *World, userID int) []int {
	t.Helper()
	out, stderr, code := runWS(t, ts, "task", "ls", "-w", w.Alpha.Key, "--assignee", strconv.Itoa(userID), "-o", "json")
	requireZero(t, code, stderr)
	ids, _ := idsFromJSONListItems(out)
	return ids
}

func cliListByStatus(t *testing.T, ts *TestServer, w *World, statusID int) []int {
	t.Helper()
	out, stderr, code := runWS(t, ts, "task", "ls", "-w", w.Alpha.Key, "-s", strconv.Itoa(statusID), "-o", "json")
	requireZero(t, code, stderr)
	ids, _ := idsFromJSONListItems(out)
	return ids
}

func workspaceIDsFromCLI(t *testing.T, raw []byte) []int {
	t.Helper()
	// `ws workspace ls -o json` emits the v1 paginated envelope.
	var paged struct {
		Data []struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &paged); err == nil && len(paged.Data) > 0 {
		out := make([]int, len(paged.Data))
		for i, ws := range paged.Data {
			out[i] = ws.ID
		}
		return out
	}
	// Fall back to bare array.
	var arr []struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("workspace CLI output: %v\nraw=%s", err, string(raw))
	}
	out := make([]int, len(arr))
	for i, ws := range arr {
		out[i] = ws.ID
	}
	return out
}
