package tests

import (
	"fmt"
	"io"
	"net/http"
	"testing"
)

type pageSummaryRow struct {
	WorkspaceID int    `json:"workspace_id"`
	PageID      int    `json:"page_id"`
	ParentID    *int   `json:"parent_id"`
	Title       string `json:"title"`
	Depth       int    `json:"depth"`
	IsHome      bool   `json:"is_home"`
	UpdatedAt   string `json:"updated_at"`
}

func createPageViaAPI(t *testing.T, server *TestServer, workspaceID int, body map[string]any) int {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/v2/workspaces/%d/pages", workspaceID), body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create page: %d - %s", resp.StatusCode, string(raw))
	}
	var page struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, resp, &page)
	if page.ID == 0 {
		t.Fatal("created page has no id")
	}
	return page.ID
}

func fetchPageSummaries(t *testing.T, server *TestServer, query string) (int, []pageSummaryRow) {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodGet, "/v2/pages/summaries"+query, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	var rows []pageSummaryRow
	DecodeJSON(t, resp, &rows)
	return resp.StatusCode, rows
}

// TestPageSummariesAcrossWorkspacesHTTP covers the cross-workspace list/search
// endpoint that lets the mobile pages surface batch workspace ids instead of
// one request per workspace (WI-1918).
func TestPageSummariesAcrossWorkspacesHTTP(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsA, _ := CreateTestWorkspace(t, server, "Summaries A", "SUMA")
	wsB, _ := CreateTestWorkspace(t, server, "Summaries B", "SUMB")

	home := createPageViaAPI(t, server, wsA, map[string]any{"title": "Home", "content": "root body", "is_home": true})
	child := createPageViaAPI(t, server, wsA, map[string]any{"title": "Child", "content": "child body with needles", "parent_id": home})
	beta := createPageViaAPI(t, server, wsB, map[string]any{"title": "Beta", "content": "beta body"})

	t.Run("lists every visible page across workspaces", func(t *testing.T) {
		status, rows := fetchPageSummaries(t, server, fmt.Sprintf("?workspace_ids=%d,%d", wsA, wsB))
		if status != http.StatusOK {
			t.Fatalf("status = %d", status)
		}
		got := make(map[int]pageSummaryRow, len(rows))
		for _, row := range rows {
			got[row.PageID] = row
		}
		if len(got) != 3 {
			t.Fatalf("got %d rows, want 3: %+v", len(got), rows)
		}
		if row := got[home]; row.WorkspaceID != wsA || row.Title != "Home" || !row.IsHome || row.Depth != 0 || row.UpdatedAt == "" {
			t.Fatalf("home row = %+v", row)
		}
		if row := got[child]; row.WorkspaceID != wsA || row.Depth != 1 || row.ParentID == nil || *row.ParentID != home {
			t.Fatalf("child row = %+v", row)
		}
		if row := got[beta]; row.WorkspaceID != wsB || row.Title != "Beta" {
			t.Fatalf("beta row = %+v", row)
		}
	})

	t.Run("query searches titles and bodies", func(t *testing.T) {
		status, rows := fetchPageSummaries(t, server, fmt.Sprintf("?workspace_ids=%d,%d&q=needles", wsA, wsB))
		if status != http.StatusOK {
			t.Fatalf("status = %d", status)
		}
		if len(rows) != 1 || rows[0].PageID != child {
			t.Fatalf("search rows = %+v, want only the child page", rows)
		}
	})

	t.Run("limit caps search results", func(t *testing.T) {
		status, rows := fetchPageSummaries(t, server, fmt.Sprintf("?workspace_ids=%d,%d&q=body&limit=1", wsA, wsB))
		if status != http.StatusOK {
			t.Fatalf("status = %d", status)
		}
		if len(rows) != 1 {
			t.Fatalf("capped rows = %+v, want 1", rows)
		}
	})

	t.Run("empty workspace list returns no rows", func(t *testing.T) {
		status, rows := fetchPageSummaries(t, server, "")
		if status != http.StatusOK || len(rows) != 0 {
			t.Fatalf("status = %d rows = %+v, want 200/empty", status, rows)
		}
	})

	t.Run("invalid limit is rejected", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/pages/summaries?workspace_ids=%d&limit=0", wsA), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})
}

// TestPageSummariesAcrossWorkspacesSkipsInaccessibleWorkspaces asserts the
// endpoint contributes no rows for a workspace the caller cannot see while
// still returning the workspaces it can.
func TestPageSummariesAcrossWorkspacesSkipsInaccessibleWorkspaces(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsA, _ := CreateTestWorkspace(t, server, "Summaries Allowed", "SMA")
	wsB, _ := CreateTestWorkspace(t, server, "Summaries Denied", "SMD")
	LockDownWorkspace(t, server, wsA)
	LockDownWorkspace(t, server, wsB)

	createPageViaAPI(t, server, wsA, map[string]any{"title": "Allowed page", "content": "visible"})
	createPageViaAPI(t, server, wsB, map[string]any{"title": "Denied page", "content": "hidden"})

	viewerID, viewerUsername, viewerPassword := CreateTestUserWithCredentials(t, server, "page_summary_viewer", "page_summary_viewer@test.com")
	AssignWorkspaceRole(t, server, viewerID, wsA, "Viewer")
	viewerToken := CreateBearerTokenForUser(t, server, viewerUsername, viewerPassword)

	resp := MakeAuthRequestWithToken(t, server, viewerToken, http.MethodGet, fmt.Sprintf("/v2/pages/summaries?workspace_ids=%d,%d", wsA, wsB), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var rows []pageSummaryRow
	DecodeJSON(t, resp, &rows)
	if len(rows) != 1 || rows[0].WorkspaceID != wsA || rows[0].Title != "Allowed page" {
		t.Fatalf("rows = %+v, want only the allowed workspace page", rows)
	}
}
