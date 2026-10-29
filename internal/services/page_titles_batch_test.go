//go:build test

package services

import (
	"encoding/json"
	"strings"
	"testing"

	"windshift/internal/testutils"
)

// The cross-workspace titles lookup backs the portal customize panel's label
// rendering (WI-1447): it must return lightweight id+title rows, skip
// workspaces the caller cannot see, and deduplicate repeated ids.
func TestPageApplicationService_ListTitlesAcrossWorkspaces(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	// Second workspace with an explicit Administrator role for the seeded user.
	var wsB int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, description, active)
		VALUES ('Titles Workspace B', 'TWSB', 'titles batch fixture', true)
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

	// Inactive workspace: no implicit Everyone access, so its pages must not
	// appear even though a page exists.
	var wsInactive int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, description, active)
		VALUES ('Titles Workspace Inactive', 'TWSD', 'titles batch fixture', false)
		RETURNING id
	`).Scan(&wsInactive); err != nil {
		t.Fatalf("create inactive workspace: %v", err)
	}

	pageService := NewPageService(tdb.GetDatabase())
	alpha, err := pageService.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "Alpha home",
		Content:     "alpha body",
		IsHome:      true,
	})
	if err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	beta, err := pageService.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "Beta child",
		Content:     "beta body",
	})
	if err != nil {
		t.Fatalf("create beta: %v", err)
	}
	gamma, err := pageService.Create(data.UserID, CreatePageInput{
		WorkspaceID: wsB,
		Title:       "Gamma in B",
		Content:     "gamma body",
	})
	if err != nil {
		t.Fatalf("create gamma: %v", err)
	}
	if _, err := pageService.Create(data.UserID, CreatePageInput{
		WorkspaceID: wsInactive,
		Title:       "Hidden in inactive",
		Content:     "hidden body",
	}); err != nil {
		t.Fatalf("create hidden page: %v", err)
	}

	permissions, err := NewPermissionService(tdb.GetDatabase(), DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	application := NewPageApplicationService(pageService, NewPagePermissionService(tdb.GetDatabase(), permissions))

	rows, err := application.ListTitlesAcrossWorkspaces(data.UserID, []int{
		data.WorkspaceID, wsB, data.WorkspaceID, wsInactive, 999999,
	})
	if err != nil {
		t.Fatalf("ListTitlesAcrossWorkspaces: %v", err)
	}

	type row struct {
		WorkspaceID int    `json:"workspace_id"`
		PageID      int    `json:"page_id"`
		Title       string `json:"title"`
	}
	want := map[row]bool{
		{data.WorkspaceID, alpha.ID, "Alpha home"}: true,
		{data.WorkspaceID, beta.ID, "Beta child"}:  true,
		{wsB, gamma.ID, "Gamma in B"}:              true,
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	for _, r := range rows {
		got := row{r.WorkspaceID, r.PageID, r.Title}
		if !want[got] {
			t.Fatalf("unexpected row %+v", got)
		}
		delete(want, got)
	}

	// Compactness contract: rows carry id/title only — no content, metadata,
	// or labels may leak into the serialized shape.
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal rows: %v", err)
	}
	for _, banned := range []string{`"content"`, `"metadata"`, `"labels"`, `"slug"`} {
		if strings.Contains(string(encoded), banned) {
			t.Fatalf("titles response contains %s: %s", banned, encoded)
		}
	}

	// The permission cache must not be poisoned by the batched read.
	if _, err := permissions.AccessibleWorkspaceIDKeys(data.UserID); err != nil {
		t.Fatalf("post-read permission lookup: %v", err)
	}
}
