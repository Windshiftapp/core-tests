//go:build test

package services

import (
	"testing"

	"windshift/internal/models"
)

// TestPageEffectiveLevels_OpenPageMirrorsWorkspaceRole pins the level
// ladder for open (inheriting, ACL-less) pages: admins get "admin",
// page.edit callers get "edit", page.view-only callers get "view", and
// outsiders are omitted entirely.
func TestPageEffectiveLevels_OpenPageMirrorsWorkspaceRole(t *testing.T) {
	env := newPagePermTestEnv(t)
	page, err := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Open"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	levels, err := env.auth.EffectiveLevels(env.users["alice"], 1, []models.Page{*page})
	if err != nil {
		t.Fatalf("effective levels: %v", err)
	}

	if got := levels[page.ID]; got != PageOpEdit {
		t.Errorf("editor alice level = %q, want %q", got, PageOpEdit)
	}

	levels, err = env.auth.EffectiveLevels(env.users["bob"], 1, []models.Page{*page})
	if err != nil {
		t.Fatalf("effective levels: %v", err)
	}
	if got := levels[page.ID]; got != PageOpAdmin {
		t.Errorf("admin bob level = %q, want %q", got, PageOpAdmin)
	}

	levels, err = env.auth.EffectiveLevels(env.users["phantom"], 1, []models.Page{*page})
	if err != nil {
		t.Fatalf("effective levels: %v", err)
	}
	if got := levels[page.ID]; got != PageOpView {
		t.Errorf("viewer phantom level = %q, want %q", got, PageOpView)
	}

	levels, err = env.auth.EffectiveLevels(env.users["carol"], 1, []models.Page{*page})
	if err != nil {
		t.Fatalf("effective levels: %v", err)
	}
	if got, ok := levels[page.ID]; ok {
		t.Errorf("outsider carol must be omitted, got %q", got)
	}
}

// TestPageEffectiveLevels_ClosedPageWithoutACLIsOmittedForNonAdmins pins
// the admin-only fallback: inherit=false with no ACL rows must omit the
// page for editors and viewers while the administrator still reads "admin".
func TestPageEffectiveLevels_ClosedPageWithoutACLIsOmittedForNonAdmins(t *testing.T) {
	env := newPagePermTestEnv(t)
	page, err := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Locked"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	updated, err := env.pages.SetInheritPermissions(env.users["bob"], page.ID, false)
	if err != nil {
		t.Fatalf("set inherit=false: %v", err)
	}
	if updated.InheritPermissions {
		t.Fatalf("inherit flag still true after update")
	}

	levels, err := env.auth.EffectiveLevels(env.users["alice"], 1, []models.Page{*updated})
	if err != nil {
		t.Fatalf("effective levels: %v", err)
	}
	if got, ok := levels[page.ID]; ok {
		t.Errorf("editor alice must be omitted on a locked page, got %q", got)
	}

	levels, err = env.auth.EffectiveLevels(env.users["bob"], 1, []models.Page{*updated})
	if err != nil {
		t.Fatalf("effective levels: %v", err)
	}
	if got := levels[updated.ID]; got != PageOpAdmin {
		t.Errorf("admin bob level = %q, want %q", got, PageOpAdmin)
	}
}

// TestPageEffectiveLevels_RestrictedPageGrantsPinLevels pins restricted
// (ACL-carrying) pages: the strongest matching ACL row plus the page.view
// membership floor decide the level, mirroring Can per op.
func TestPageEffectiveLevels_RestrictedPageGrantsPinLevels(t *testing.T) {
	env := newPagePermTestEnv(t)
	viewOnly, err := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "View only"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	editGrant, err := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Edit grant"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	adminGrant, err := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Admin grant"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for id, created := range map[string]*models.Page{"view": viewOnly, "edit": editGrant, "admin": adminGrant} {
		if _, err := env.pages.SetInheritPermissions(env.users["bob"], created.ID, false); err != nil {
			t.Fatalf("break inheritance on %s page: %v", id, err)
		}
	}
	// Reload so the cleared inherit flags reflect committed state.
	viewOnly, err = env.pages.GetByID(viewOnly.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	editGrant, err = env.pages.GetByID(editGrant.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	adminGrant, err = env.pages.GetByID(adminGrant.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, err := env.pages.GrantPermission(env.users["bob"], viewOnly.ID, "user", env.users["phantom"], "view"); err != nil {
		t.Fatalf("grant view: %v", err)
	}
	if _, err := env.pages.GrantPermission(env.users["bob"], editGrant.ID, "user", env.users["phantom"], "edit"); err != nil {
		t.Fatalf("grant edit: %v", err)
	}
	if _, err := env.pages.GrantPermission(env.users["bob"], adminGrant.ID, "user", env.users["phantom"], "admin"); err != nil {
		t.Fatalf("grant admin: %v", err)
	}

	levels, err := env.auth.EffectiveLevels(env.users["phantom"], 1, []models.Page{*viewOnly, *editGrant, *adminGrant})
	if err != nil {
		t.Fatalf("effective levels: %v", err)
	}
	if got := levels[viewOnly.ID]; got != PageOpView {
		t.Errorf("view grant level = %q, want %q", got, PageOpView)
	}
	if got := levels[editGrant.ID]; got != PageOpEdit {
		t.Errorf("edit grant level = %q, want %q", got, PageOpEdit)
	}
	if got := levels[adminGrant.ID]; got != PageOpAdmin {
		t.Errorf("admin grant level = %q, want %q", got, PageOpAdmin)
	}
}

// TestPageEffectiveLevels_ArchivedAndCrossWorkspacePagesOmitted pins the
// exclusions: archived pages never appear (even for administrators) and
// pages from another workspace are filtered by the workspace argument.
func TestPageEffectiveLevels_ArchivedAndCrossWorkspacePagesOmitted(t *testing.T) {
	env := newPagePermTestEnv(t)
	page, err := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Doomed"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := env.pages.Archive(env.users["bob"], page.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	// Re-read so the archived flag reflects committed state, not the
	// pre-archive in-memory copy returned by Create.
	archived, err := env.pages.GetByID(page.ID)
	if err != nil {
		t.Fatalf("reload archived page: %v", err)
	}
	if archived.ArchivedAt == nil {
		t.Fatalf("archive did not persist archived_at")
	}

	foreignWorkspace := 2
	if _, err := env.db.Exec(`INSERT INTO workspaces (id, name, key, active) VALUES (2, 'WS2', 'WS2', true)`); err != nil {
		t.Fatalf("seed second workspace: %v", err)
	}
	foreign, err := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: foreignWorkspace, Title: "Foreign"})
	if err != nil {
		t.Fatalf("create foreign page: %v", err)
	}

	// Bob administrates workspace 1; the archived page must still be
	// omitted from a workspace-1 evaluation.
	levels, err := env.auth.EffectiveLevels(env.users["bob"], 1, []models.Page{*archived, *foreign})
	if err != nil {
		t.Fatalf("effective levels: %v", err)
	}
	if got, ok := levels[page.ID]; ok {
		t.Errorf("archived page must be omitted even for admins, got %q", got)
	}
	if got, ok := levels[foreign.ID]; ok {
		t.Errorf("cross-workspace page must be omitted, got %q", got)
	}

	// Workspace 2 is ungated, so any authenticated caller lands on the
	// open-page edit fallback there. Workspace 1's admin grant must not
	// elevate bob to page admin in workspace 2.
	levels, err = env.auth.EffectiveLevels(env.users["bob"], foreignWorkspace, []models.Page{*foreign})
	if err != nil {
		t.Fatalf("effective levels foreign workspace: %v", err)
	}
	if got := levels[foreign.ID]; got != PageOpEdit {
		t.Errorf("bob level in ungated workspace 2 = %q, want %q (ws1 admin must not leak)", got, PageOpEdit)
	}
}

// TestPageApplicationEffectiveLevelsMatchesCan is the contract test: for
// every live visible page, the batched level must equal the per-op Can
// evaluation (admin > edit > view), and Can-denied pages must be absent.
func TestPageApplicationEffectiveLevelsMatchesCan(t *testing.T) {
	env := newPagePermTestEnv(t)
	application := NewPageApplicationService(env.pages, env.auth)

	open, err := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Open"})
	if err != nil {
		t.Fatalf("create open: %v", err)
	}
	restricted, err := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Restricted"})
	if err != nil {
		t.Fatalf("create restricted: %v", err)
	}
	if _, err := env.pages.SetInheritPermissions(env.users["bob"], restricted.ID, false); err != nil {
		t.Fatalf("break inheritance: %v", err)
	}
	if _, err := env.pages.GrantPermission(env.users["bob"], restricted.ID, "user", env.users["phantom"], "view"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	archived, err := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Doomed"})
	if err != nil {
		t.Fatalf("create archived: %v", err)
	}
	if err := env.pages.Archive(env.users["bob"], archived.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	for _, user := range []string{"alice", "bob", "phantom", "carol"} {
		userLevels, err := application.EffectiveLevels(env.users[user], 1)
		if err != nil {
			t.Fatalf("effective levels for %s: %v", user, err)
		}
		for _, page := range []models.Page{*open, *restricted} {
			level := userLevels[page.ID]
			want := ""
			for _, op := range []string{PageOpAdmin, PageOpEdit, PageOpView} {
				can, err := env.auth.Can(env.users[user], 1, page.ID, op)
				if err != nil {
					t.Fatalf("Can(%s, page %d, %s): %v", user, page.ID, op, err)
				}
				if can {
					want = op
					break
				}
			}
			if level != want {
				t.Errorf("%s page %d (%s): level = %q, want %q", user, page.ID, page.Title, level, want)
			}
			if want == "" {
				if _, ok := userLevels[page.ID]; ok {
					t.Errorf("%s page %d (%s): Can denies all ops but level entry exists", user, page.ID, page.Title)
				}
			}
		}
		if _, ok := userLevels[archived.ID]; ok {
			t.Errorf("%s: archived page leaked into effective levels", user)
		}
	}
}
