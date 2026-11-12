//go:build test

package services

import (
	"encoding/json"
	"strings"
	"testing"

	"windshift/internal/testutils"
)

// The cross-workspace summaries lookup backs the mobile pages list and search
// (WI-1918). It must list every visible page with the row fields the phone
// renders, search title+body when a query is supplied, cap search results,
// skip workspaces the caller cannot see, and never leak page bodies.
func TestPageApplicationService_ListSummariesAcrossWorkspaces(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	var wsB int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, description, active)
		VALUES ('Summaries Workspace B', 'SWSB', 'summaries batch fixture', true)
		RETURNING id
	`).Scan(&wsB); err != nil {
		t.Fatalf("create workspace B: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at)
		SELECT ?, ?, id, CURRENT_TIMESTAMP FROM workspace_roles WHERE name = 'Administrator'
	`, data.UserID, wsB); err != nil {
		t.Fatalf("grant workspace B role: %v", err)
	}

	var wsInactive int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, description, active)
		VALUES ('Summaries Workspace Inactive', 'SWSD', 'summaries batch fixture', false)
		RETURNING id
	`).Scan(&wsInactive); err != nil {
		t.Fatalf("create inactive workspace: %v", err)
	}

	pageService := NewPageService(tdb.GetDatabase())
	home, err := pageService.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "Home page",
		Content:     "home body",
		IsHome:      true,
	})
	if err != nil {
		t.Fatalf("create home: %v", err)
	}
	child, err := pageService.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "Child page",
		Content:     "child body mentions needles",
		ParentID:    &home.ID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if child.Depth != 1 {
		t.Fatalf("child depth = %d, want 1", child.Depth)
	}
	beta, err := pageService.Create(data.UserID, CreatePageInput{
		WorkspaceID: wsB,
		Title:       "Beta page",
		Content:     "beta body",
	})
	if err != nil {
		t.Fatalf("create beta: %v", err)
	}
	if _, err := pageService.Create(data.UserID, CreatePageInput{
		WorkspaceID: wsInactive,
		Title:       "Hidden inactive",
		Content:     "hidden body",
	}); err != nil {
		t.Fatalf("create hidden page: %v", err)
	}

	permissions, err := NewPermissionService(tdb.GetDatabase(), DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	application := NewPageApplicationService(pageService, NewPagePermissionService(tdb.GetDatabase(), permissions))

	t.Run("empty query lists every visible page", func(t *testing.T) {
		rows, err := application.ListSummariesAcrossWorkspaces(data.UserID, []int{
			data.WorkspaceID, wsB, data.WorkspaceID, wsInactive, 999999,
		}, "", 0)
		if err != nil {
			t.Fatalf("ListSummariesAcrossWorkspaces: %v", err)
		}
		got := make(map[int]PageSummaryRow, len(rows))
		for _, row := range rows {
			got[row.PageID] = row
		}
		if len(got) != 3 {
			t.Fatalf("got %d rows, want 3: %+v", len(got), rows)
		}
		homeRow, ok := got[home.ID]
		if !ok || homeRow.WorkspaceID != data.WorkspaceID || homeRow.Title != "Home page" || !homeRow.IsHome || homeRow.Depth != 0 {
			t.Fatalf("home row = %+v", homeRow)
		}
		childRow, ok := got[child.ID]
		if !ok || childRow.ParentID == nil || *childRow.ParentID != home.ID || childRow.Depth != 1 || childRow.IsHome {
			t.Fatalf("child row = %+v", childRow)
		}
		if childRow.UpdatedAt.IsZero() {
			t.Fatalf("child row updated_at is zero")
		}
		betaRow, ok := got[beta.ID]
		if !ok || betaRow.WorkspaceID != wsB || betaRow.Title != "Beta page" {
			t.Fatalf("beta row = %+v", betaRow)
		}

		encoded, err := json.Marshal(rows)
		if err != nil {
			t.Fatalf("marshal rows: %v", err)
		}
		for _, banned := range []string{`"content"`, `"metadata"`, `"labels"`, `"slug"`} {
			if strings.Contains(string(encoded), banned) {
				t.Fatalf("summaries response contains %s: %s", banned, encoded)
			}
		}
	})

	t.Run("query searches title and body and caps results", func(t *testing.T) {
		// "needles" appears only in the child body, proving the search still
		// matches page bodies rather than titles alone.
		rows, err := application.ListSummariesAcrossWorkspaces(data.UserID, []int{data.WorkspaceID, wsB}, "needles", 5)
		if err != nil {
			t.Fatalf("search summaries: %v", err)
		}
		if len(rows) != 1 || rows[0].PageID != child.ID {
			t.Fatalf("search rows = %+v, want only the child page", rows)
		}

		rows, err = application.ListSummariesAcrossWorkspaces(data.UserID, []int{data.WorkspaceID, wsB}, "page", 1)
		if err != nil {
			t.Fatalf("capped search: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("capped search returned %d rows, want 1", len(rows))
		}
	})

	if _, err := permissions.AccessibleWorkspaceIDKeys(data.UserID); err != nil {
		t.Fatalf("post-read permission lookup: %v", err)
	}
}
