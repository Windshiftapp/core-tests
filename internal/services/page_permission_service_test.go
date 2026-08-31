//go:build test

package services

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/repository"
)

// pagePermTestEnv spins up a fully-initialized DB with gated workspace 1
// (Viewer/Editor/Admin all assigned) so the evaluator can be exercised
// without the open-by-default mode masking permission bugs.
type pagePermTestEnv struct {
	db     database.Database
	perm   *PermissionService
	pages  *PageService
	auth   *PagePermissionService
	users  map[string]int
	roleID map[string]int
}

func newPagePermTestEnv(t *testing.T) *pagePermTestEnv {
	t.Helper()
	dsn := "file:permtest-" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := database.NewSQLiteDB(dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg := DefaultPermissionCacheConfig()
	cfg.WarmupOnStartup = false
	cfg.TTL = 1 * time.Minute
	perm, err := NewPermissionService(db, cfg)
	if err != nil {
		t.Fatalf("perm: %v", err)
	}
	t.Cleanup(func() { perm.Close() })

	users := map[string]int{}
	for _, name := range []string{"alice", "bob", "carol", "phantom"} {
		var id int
		if err := db.QueryRow(
			`INSERT INTO users (email, username, first_name, last_name, password_hash, is_active)
			 VALUES (?, ?, ?, ?, 'h', TRUE) RETURNING id`,
			name+"@x", name, name, name,
		).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		users[name] = id
	}
	if _, err := db.Exec(`INSERT INTO workspaces (id, name, key, active) VALUES (1, 'WS', 'WS1', true)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}

	roleID := map[string]int{}
	for _, role := range []string{"Viewer", "Editor", "Administrator"} {
		var id int
		if err := db.QueryRow(`SELECT id FROM workspace_roles WHERE name = ?`, role).Scan(&id); err != nil {
			t.Fatalf("look up %s: %v", role, err)
		}
		roleID[role] = id
	}

	// Gate the workspace: alice→Editor, bob→Administrator, phantom→Viewer.
	// carol stays unassigned (true outsider).
	if _, err := db.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at)
		VALUES (?, 1, ?, CURRENT_TIMESTAMP),
		       (?, 1, ?, CURRENT_TIMESTAMP),
		       (?, 1, ?, CURRENT_TIMESTAMP)
	`,
		users["alice"], roleID["Editor"],
		users["bob"], roleID["Administrator"],
		users["phantom"], roleID["Viewer"],
	); err != nil {
		t.Fatalf("seed user roles: %v", err)
	}

	return &pagePermTestEnv{
		db:     db,
		perm:   perm,
		pages:  NewPageService(db),
		auth:   NewPagePermissionService(db, perm),
		users:  users,
		roleID: roleID,
	}
}

func TestPagePermission_OpenPageFallsBackToWorkspaceRole(t *testing.T) {
	env := newPagePermTestEnv(t)
	page, err := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Open"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	can, err := env.auth.Can(env.users["alice"], 1, page.ID, PageOpEdit)
	if err != nil || !can {
		t.Errorf("editor alice should be able to edit: can=%v err=%v", can, err)
	}
	can, err = env.auth.Can(env.users["carol"], 1, page.ID, PageOpView)
	if err != nil || can {
		t.Errorf("outsider carol should NOT view the open page: can=%v err=%v", can, err)
	}
}

func TestPagePermission_InheritFalse_EmptyACL_IsAdminOnly(t *testing.T) {
	env := newPagePermTestEnv(t)
	page, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Locked"})

	// Break inheritance with no ACL rows → admin-only fallback.
	if _, err := env.pages.SetInheritPermissions(env.users["bob"], page.ID, false); err != nil {
		t.Fatalf("set inherit=false: %v", err)
	}

	// Even alice (Editor with workspace page.view) must be denied now.
	can, err := env.auth.Can(env.users["alice"], 1, page.ID, PageOpView)
	if err != nil || can {
		t.Errorf("editor alice should be denied on locked page: can=%v err=%v", can, err)
	}
	// Admin still sees everything.
	can, err = env.auth.Can(env.users["bob"], 1, page.ID, PageOpView)
	if err != nil || !can {
		t.Errorf("admin bob must still view a locked page: can=%v err=%v", can, err)
	}
}

func TestPagePermission_InheritFalse_WithACL_GrantsAccess(t *testing.T) {
	env := newPagePermTestEnv(t)
	page, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Restricted"})
	if _, err := env.pages.SetInheritPermissions(env.users["bob"], page.ID, false); err != nil {
		t.Fatalf("break inheritance: %v", err)
	}

	// phantom is a workspace Viewer (has workspace.page.view via role). On
	// a restricted page they'd be denied without an explicit ACL row, so
	// granting them direct view should unlock view-only access.
	if _, err := env.pages.GrantPermission(env.users["bob"], page.ID, "user", env.users["phantom"], "view"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	can, err := env.auth.Can(env.users["phantom"], 1, page.ID, PageOpView)
	if err != nil || !can {
		t.Errorf("phantom with explicit user grant must view: can=%v err=%v", can, err)
	}

	// Edit still denied — only view granted.
	can, err = env.auth.Can(env.users["phantom"], 1, page.ID, PageOpEdit)
	if err != nil || can {
		t.Errorf("phantom with view-only grant must NOT edit: can=%v err=%v", can, err)
	}
}

func TestPagePermission_OwnedAgentUsesOwnerACLPrincipal(t *testing.T) {
	env := newPagePermTestEnv(t)
	page, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Owner restricted"})
	if _, err := env.pages.SetInheritPermissions(env.users["bob"], page.ID, false); err != nil {
		t.Fatalf("break inheritance: %v", err)
	}
	if _, err := env.pages.GrantPermission(env.users["bob"], page.ID, "user", env.users["alice"], "view"); err != nil {
		t.Fatalf("grant owner: %v", err)
	}

	var agentID int
	if err := env.db.QueryRow(
		`INSERT INTO users (email, username, first_name, last_name, is_active, is_agent, agent_owner_user_id, password_hash)
		 VALUES ('alice-agent@x', 'alice-agent', 'Alice', 'Agent', TRUE, TRUE, ?, NULL) RETURNING id`,
		env.users["alice"],
	).Scan(&agentID); err != nil {
		t.Fatalf("seed owned agent: %v", err)
	}

	can, err := env.auth.Can(agentID, 1, page.ID, PageOpView)
	if err != nil || !can {
		t.Fatalf("owned agent should view via owner's direct ACL: can=%v err=%v", can, err)
	}
	visible, err := env.auth.ListVisiblePageIDs(agentID, 1, []int{page.ID})
	if err != nil {
		t.Fatalf("list visible: %v", err)
	}
	if !visible[page.ID] {
		t.Fatalf("owned agent should see restricted page in batched listing via owner's ACL")
	}
}

// Bug-hunt-2 #2: an ACL match must require workspace membership (i.e.
// workspace.page.view) — granting an explicit role on a page to a user
// who isn't even a workspace member must not give them access.
func TestPagePermission_ACLGrantRequiresWorkspaceMembership(t *testing.T) {
	env := newPagePermTestEnv(t)
	page, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Restricted"})
	if _, err := env.pages.SetInheritPermissions(env.users["bob"], page.ID, false); err != nil {
		t.Fatalf("break inheritance: %v", err)
	}
	// carol has NO workspace role assigned (true outsider). An explicit
	// ACL grant on the page must not synthesize workspace membership.
	if _, err := env.pages.GrantPermission(env.users["bob"], page.ID, "user", env.users["carol"], "edit"); err != nil {
		t.Fatalf("grant: %v", err)
	}

	for _, op := range []string{PageOpView, PageOpEdit} {
		can, err := env.auth.Can(env.users["carol"], 1, page.ID, op)
		if err != nil {
			t.Fatalf("carol can %s: %v", op, err)
		}
		if can {
			t.Errorf("carol (no workspace role) must NOT %s a restricted page even with an ACL row", op)
		}
	}
}

// Bug-hunt-2 #3 (group is_active): a grant targeting a now-inactive
// group must not match its members. Mirrors how
// PermissionService.buildUserPermissionCache filters group_members by
// groups.is_active.
func TestPagePermission_InactiveGroupGrantDoesNotMatch(t *testing.T) {
	env := newPagePermTestEnv(t)
	page, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Restricted"})
	if _, err := env.pages.SetInheritPermissions(env.users["bob"], page.ID, false); err != nil {
		t.Fatalf("break inheritance: %v", err)
	}

	// Create an ACTIVE group, add alice, grant the group view on the page.
	var groupID int
	if err := env.db.QueryRow(
		`INSERT INTO groups (name, is_active, created_by) VALUES ('eng', TRUE, ?) RETURNING id`,
		env.users["bob"],
	).Scan(&groupID); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if _, err := env.db.Exec(
		`INSERT INTO group_members (group_id, user_id, added_by) VALUES (?, ?, ?)`,
		groupID, env.users["alice"], env.users["bob"],
	); err != nil {
		t.Fatalf("seed group member: %v", err)
	}
	if _, err := env.pages.GrantPermission(env.users["bob"], page.ID, "group", groupID, "view"); err != nil {
		t.Fatalf("grant group: %v", err)
	}

	// Pre-condition: alice can see the page via her group membership.
	can, err := env.auth.Can(env.users["alice"], 1, page.ID, PageOpView)
	if err != nil || !can {
		t.Fatalf("alice should view via active group: can=%v err=%v", can, err)
	}

	// Deactivate the group. Alice's grant must NO LONGER match.
	if _, err := env.db.Exec(`UPDATE groups SET is_active = FALSE WHERE id = ?`, groupID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	can, err = env.auth.Can(env.users["alice"], 1, page.ID, PageOpView)
	if err != nil {
		t.Fatalf("alice can after deactivate: %v", err)
	}
	if can {
		t.Error("inactive group should not satisfy a group ACL grant")
	}
}

// Bug-hunt-2 #3 (group→role): a user who reaches a workspace role only
// via a group grant must still match a role-typed ACL row referencing
// that role. Previously userWorkspaceRoleIDs only consulted direct
// user_workspace_roles and missed indirect role membership.
func TestPagePermission_RoleGrantViaGroupMatches(t *testing.T) {
	env := newPagePermTestEnv(t)
	page, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Restricted"})
	if _, err := env.pages.SetInheritPermissions(env.users["bob"], page.ID, false); err != nil {
		t.Fatalf("break inheritance: %v", err)
	}

	// Create a group, put carol in it, and grant that group the Viewer
	// workspace role. carol now reaches the Viewer role via the group
	// rather than via user_workspace_roles.
	var groupID int
	if err := env.db.QueryRow(
		`INSERT INTO groups (name, is_active, created_by) VALUES ('docs', TRUE, ?) RETURNING id`,
		env.users["bob"],
	).Scan(&groupID); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if _, err := env.db.Exec(
		`INSERT INTO group_members (group_id, user_id, added_by) VALUES (?, ?, ?)`,
		groupID, env.users["carol"], env.users["bob"],
	); err != nil {
		t.Fatalf("seed group member: %v", err)
	}
	if _, err := env.db.Exec(
		`INSERT INTO group_workspace_roles (group_id, workspace_id, role_id, granted_by) VALUES (?, 1, ?, ?)`,
		groupID, env.roleID["Viewer"], env.users["bob"],
	); err != nil {
		t.Fatalf("seed group role: %v", err)
	}

	// Grant the Viewer role view on the page.
	if _, err := env.pages.GrantPermission(env.users["bob"], page.ID, "role", env.roleID["Viewer"], "view"); err != nil {
		t.Fatalf("grant role: %v", err)
	}

	// carol must match the role-typed ACL grant via her group→role link.
	can, err := env.auth.Can(env.users["carol"], 1, page.ID, PageOpView)
	if err != nil || !can {
		t.Errorf("carol should match role-typed ACL via group→role: can=%v err=%v", can, err)
	}
}

// Bug-hunt-2 #2b: GrantPermission validates principal existence at write
// time so stale grants don't sit in the audit log forever.
func TestPagePermission_GrantPermissionRejectsUnknownPrincipal(t *testing.T) {
	env := newPagePermTestEnv(t)
	page, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Doc"})

	if _, err := env.pages.GrantPermission(env.users["bob"], page.ID, "user", 999999, "view"); !errors.Is(err, ErrPageGrantPrincipalNotFound) {
		t.Errorf("unknown user: want ErrPageGrantPrincipalNotFound, got %v", err)
	}
	if _, err := env.pages.GrantPermission(env.users["bob"], page.ID, "group", 999999, "view"); !errors.Is(err, ErrPageGrantPrincipalNotFound) {
		t.Errorf("unknown group: want ErrPageGrantPrincipalNotFound, got %v", err)
	}
	if _, err := env.pages.GrantPermission(env.users["bob"], page.ID, "role", 999999, "view"); !errors.Is(err, ErrPageGrantPrincipalNotFound) {
		t.Errorf("unknown role: want ErrPageGrantPrincipalNotFound, got %v", err)
	}
}

func TestPagePermission_RoleGrant_OnRestrictedPage(t *testing.T) {
	env := newPagePermTestEnv(t)
	page, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "RoleGated"})
	if _, err := env.pages.SetInheritPermissions(env.users["bob"], page.ID, false); err != nil {
		t.Fatalf("break: %v", err)
	}
	// Grant the Editor role view access on this page.
	if _, err := env.pages.GrantPermission(env.users["bob"], page.ID, "role", env.roleID["Editor"], "view"); err != nil {
		t.Fatalf("grant role: %v", err)
	}
	// Alice (Editor) gets in via the role.
	can, err := env.auth.Can(env.users["alice"], 1, page.ID, PageOpView)
	if err != nil || !can {
		t.Errorf("editor alice should view via role grant: can=%v err=%v", can, err)
	}
	// Phantom (Viewer) does not.
	can, err = env.auth.Can(env.users["phantom"], 1, page.ID, PageOpView)
	if err != nil || can {
		t.Errorf("viewer phantom must NOT see role-gated-to-editors page: can=%v err=%v", can, err)
	}
}

func TestPagePermission_ArchivedPage_MutationsDeniedForEveryone(t *testing.T) {
	env := newPagePermTestEnv(t)
	page, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Doomed"})
	if err := env.pages.Archive(env.users["bob"], page.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	for _, caller := range []string{"alice", "bob"} {
		for _, op := range []string{PageOpEdit, PageOpAdmin} {
			can, err := env.auth.Can(env.users[caller], 1, page.ID, op)
			if err != nil {
				t.Fatalf("can %s %s: %v", caller, op, err)
			}
			if can {
				t.Errorf("%s should NOT be able to %s an archived page (caller has workspace role)", caller, op)
			}
		}
	}
}

func TestPagePermission_ArchivedPage_ViewAllowedForWorkspaceAdminOnly(t *testing.T) {
	env := newPagePermTestEnv(t)
	page, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Frozen"})
	if err := env.pages.Archive(env.users["bob"], page.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Workspace admin (bob) can still view archived pages.
	can, err := env.auth.Can(env.users["bob"], 1, page.ID, PageOpView)
	if err != nil || !can {
		t.Errorf("workspace admin must still view archived page: can=%v err=%v", can, err)
	}

	// Editor (alice) gets 404.
	can, err = env.auth.Can(env.users["alice"], 1, page.ID, PageOpView)
	if err != nil {
		t.Fatalf("alice can: %v", err)
	}
	if can {
		t.Error("editor must NOT view archived page")
	}

	// Phantom (viewer) gets 404.
	can, err = env.auth.Can(env.users["phantom"], 1, page.ID, PageOpView)
	if err != nil {
		t.Fatalf("phantom can: %v", err)
	}
	if can {
		t.Error("viewer must NOT view archived page")
	}
}

func TestPagePermission_ArchivedPage_AclGrantDoesNotOverride(t *testing.T) {
	env := newPagePermTestEnv(t)
	page, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Sealed"})
	// Pre-archive: grant alice explicit view on the page.
	if _, err := env.pages.GrantPermission(env.users["bob"], page.ID, "user", env.users["alice"], "view"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := env.pages.Archive(env.users["bob"], page.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Alice's explicit grant must NOT let her see an archived page —
	// archive policy supersedes per-page ACL for non-admins.
	can, err := env.auth.Can(env.users["alice"], 1, page.ID, PageOpView)
	if err != nil {
		t.Fatalf("can: %v", err)
	}
	if can {
		t.Error("explicit ACL grant must not override archive policy")
	}
}

func TestPagePermission_AncestorBreaksInheritance_HidesRootGrantFromChild(t *testing.T) {
	// Bug-hunt finding #2: previously the walk included every ancestor
	// when the current page had inherit_permissions=true, regardless of
	// whether an intermediate ancestor broke inheritance.
	//
	// Setup: root grants phantom (a workspace Viewer) view access. Middle
	// breaks inheritance AND grants alice view. Child inherits — its
	// effective ACL with the chain-breaker working is [alice→view]
	// (middle only), so phantom is denied on child. Without the fix, the
	// walk would also include root's [phantom→view], and phantom would
	// leak through.
	//
	// All three subjects have workspace page.view (alice=Editor,
	// phantom=Viewer) so they pass the ACL-membership floor; the test
	// turns on whether the ACL set contains them.
	env := newPagePermTestEnv(t)
	root, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Root"})
	if _, err := env.pages.SetInheritPermissions(env.users["bob"], root.ID, false); err != nil {
		t.Fatalf("break root inheritance: %v", err)
	}
	if _, err := env.pages.GrantPermission(env.users["bob"], root.ID, "user", env.users["phantom"], "view"); err != nil {
		t.Fatalf("grant phantom on root: %v", err)
	}
	middle, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, ParentID: &root.ID, Title: "Middle"})
	if _, err := env.pages.SetInheritPermissions(env.users["bob"], middle.ID, false); err != nil {
		t.Fatalf("break middle inheritance: %v", err)
	}
	if _, err := env.pages.GrantPermission(env.users["bob"], middle.ID, "user", env.users["alice"], "view"); err != nil {
		t.Fatalf("grant alice on middle: %v", err)
	}
	child, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, ParentID: &middle.ID, Title: "Child"})

	// Pre-condition: phantom can see root via the direct grant + Viewer
	// membership.
	can, err := env.auth.Can(env.users["phantom"], 1, root.ID, PageOpView)
	if err != nil || !can {
		t.Fatalf("phantom should see root via direct grant: can=%v err=%v", can, err)
	}
	// Pre-condition: alice can see middle via the direct grant.
	can, err = env.auth.Can(env.users["alice"], 1, middle.ID, PageOpView)
	if err != nil || !can {
		t.Fatalf("alice should see middle via direct grant: can=%v err=%v", can, err)
	}

	// Bug: without the chain-breaker fix, the walk includes root's ACL
	// when evaluating child and phantom's root grant leaks through.
	// Post-fix: the walk stops at middle (inherit=false), so child's
	// effective ACL is middle's only ([alice→view]); phantom is denied.
	can, err = env.auth.Can(env.users["phantom"], 1, child.ID, PageOpView)
	if err != nil {
		t.Fatalf("phantom can: %v", err)
	}
	if can {
		t.Error("inheritance break at middle must hide root grant from child")
	}

	// Sanity: alice still sees child because middle's grant carries
	// through to child via inheritance.
	can, err = env.auth.Can(env.users["alice"], 1, child.ID, PageOpView)
	if err != nil || !can {
		t.Errorf("alice should inherit middle's grant on child: can=%v err=%v", can, err)
	}
}

// TestPageRepository_SlugsAreNotUnique is the inverse of the old
// TestPageRepository_RootSlugUniqueness. That test pinned bug-hunt finding
// #4: UNIQUE(workspace_id, parent_id, slug) is bypassed for root pages
// because NULL parent_id never equals itself, so a partial index had to
// reject duplicate root slugs. Both rules are gone — nothing resolves a
// page by slug — so the repository must now accept duplicates at every
// level. Driven through the repository directly, since the service no
// longer has a disambiguation step that could mask a surviving constraint.
func TestPageRepository_SlugsAreNotUnique(t *testing.T) {
	env := newPagePermTestEnv(t)
	repo := repository.NewPageRepository(env.db)

	insert := func(parentID *int, slug, title, path string, depth int) error {
		return database.WithTx(env.db, func(tx database.Tx) error {
			_, err := repo.CreateTx(tx, repository.CreateInput{
				WorkspaceID:        1,
				ParentID:           parentID,
				Title:              title,
				Slug:               slug,
				CreatedBy:          env.users["alice"],
				InheritPermissions: true,
				Path:               path,
				Depth:              depth,
			})
			return err
		})
	}

	if err := insert(nil, "guide", "Guide", "/", 0); err != nil {
		t.Fatalf("first root insert: %v", err)
	}
	// Root pages: previously rejected by idx_pages_workspace_root_slug.
	if err := insert(nil, "guide", "Guide v2", "/", 0); err != nil {
		t.Errorf("duplicate root slug should be accepted, got %v", err)
	}

	// Siblings under a shared parent: previously rejected by the composite
	// UNIQUE. Reuse the first root as the parent.
	var parentID int
	if err := env.db.QueryRow(`SELECT id FROM pages WHERE slug = 'guide' ORDER BY id LIMIT 1`).Scan(&parentID); err != nil {
		t.Fatalf("look up parent: %v", err)
	}
	childPath := fmt.Sprintf("/%d/", parentID)
	if err := insert(&parentID, "notes", "Notes", childPath, 1); err != nil {
		t.Fatalf("first child insert: %v", err)
	}
	if err := insert(&parentID, "notes", "Notes again", childPath, 1); err != nil {
		t.Errorf("duplicate sibling slug should be accepted, got %v", err)
	}
}

func TestPagePermission_InheritedACLFromAncestor(t *testing.T) {
	env := newPagePermTestEnv(t)
	parent, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "P"})
	if _, err := env.pages.SetInheritPermissions(env.users["bob"], parent.ID, false); err != nil {
		t.Fatalf("break parent inheritance: %v", err)
	}
	// Parent restricted but grants alice view.
	if _, err := env.pages.GrantPermission(env.users["bob"], parent.ID, "user", env.users["alice"], "view"); err != nil {
		t.Fatalf("grant on parent: %v", err)
	}
	// Child inherits.
	child, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, ParentID: &parent.ID, Title: "C"})

	can, err := env.auth.Can(env.users["alice"], 1, child.ID, PageOpView)
	if err != nil || !can {
		t.Errorf("alice should inherit parent grant for child: can=%v err=%v", can, err)
	}
	can, err = env.auth.Can(env.users["carol"], 1, child.ID, PageOpView)
	if err != nil || can {
		t.Errorf("carol (no grant) should NOT inherit parent restriction-passthrough: can=%v err=%v", can, err)
	}
}

// TestPagePermission_ListVisiblePageIDs_MatchesCan pins the batched
// evaluator against a one-at-a-time Can(PageOpView) loop across the parity
// scenarios from the fix plan: open inherited page, page with direct ACL,
// child inheriting from parent, child under inherit_permissions=false,
// archived page, and a cross-workspace id. Both flows must agree for every
// (user, page) pair — the whole point of the batch optimization is to keep
// security semantics identical while cutting query count.
func TestPagePermission_ListVisiblePageIDs_MatchesCan(t *testing.T) {
	env := newPagePermTestEnv(t)

	// Seed a second workspace so we can verify cross-workspace ids resolve
	// to false in the batched path.
	if _, err := env.db.Exec(`INSERT INTO workspaces (id, name, key, active) VALUES (2, 'WS2', 'WS2K', true)`); err != nil {
		t.Fatalf("seed workspace 2: %v", err)
	}

	// Open root page (inherits, no ACL).
	openPage, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Open"})

	// Restricted parent with a direct ACL granting carol view.
	parent, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Parent"})
	if _, err := env.pages.SetInheritPermissions(env.users["bob"], parent.ID, false); err != nil {
		t.Fatalf("break parent inheritance: %v", err)
	}
	if _, err := env.pages.GrantPermission(env.users["bob"], parent.ID, "user", env.users["alice"], "view"); err != nil {
		t.Fatalf("grant alice on parent: %v", err)
	}

	// Child of the restricted parent — inherits ACL via the chain.
	inheritingChild, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, ParentID: &parent.ID, Title: "ChildInherits"})

	// Child under a broken-inheritance branch with no ACL → admin-only.
	lockedChild, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, ParentID: &parent.ID, Title: "Locked"})
	if _, err := env.pages.SetInheritPermissions(env.users["bob"], lockedChild.ID, false); err != nil {
		t.Fatalf("break locked child inheritance: %v", err)
	}

	// Archived page (rule: admin-only, ACL doesn't apply).
	archived, _ := env.pages.Create(env.users["alice"], CreatePageInput{WorkspaceID: 1, Title: "Archived"})
	if err := env.pages.Archive(env.users["bob"], archived.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Page in a different workspace — must come back false from the batch
	// when queried against workspace 1.
	crossWS, _ := env.pages.Create(env.users["bob"], CreatePageInput{WorkspaceID: 2, Title: "Elsewhere"})

	pageIDs := []int{openPage.ID, parent.ID, inheritingChild.ID, lockedChild.ID, archived.ID, crossWS.ID, 999999}

	users := []string{"alice", "bob", "phantom", "carol"}
	for _, name := range users {
		t.Run(name, func(t *testing.T) {
			uid := env.users[name]
			batch, err := env.auth.ListVisiblePageIDs(uid, 1, pageIDs)
			if err != nil {
				t.Fatalf("ListVisiblePageIDs: %v", err)
			}
			for _, pid := range pageIDs {
				want, err := env.auth.Can(uid, 1, pid, PageOpView)
				if err != nil {
					t.Fatalf("Can(%d): %v", pid, err)
				}
				got, ok := batch[pid]
				if !ok {
					t.Errorf("page %d missing from batch result", pid)
					continue
				}
				if got != want {
					t.Errorf("page %d: batch=%v, Can=%v", pid, got, want)
				}
			}
		})
	}
}

// TestPagePermission_ListVisiblePageIDs_EmptyInput covers the fast-return
// path so future refactors don't accidentally regress to issuing queries
// with an empty IN (...) list.
func TestPagePermission_ListVisiblePageIDs_EmptyInput(t *testing.T) {
	env := newPagePermTestEnv(t)
	got, err := env.auth.ListVisiblePageIDs(env.users["alice"], 1, nil)
	if err != nil {
		t.Fatalf("empty input: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty input: want empty map, got %v", got)
	}
}
