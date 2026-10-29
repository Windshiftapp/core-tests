//go:build test

package services

import (
	"fmt"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
)

// permissionServiceAccess adapts PermissionService to CatalogAccess the same
// way authz.Authz delegates when a permission service is present, without the
// import cycle that importing authz from this package would create.
type permissionServiceAccess struct {
	service *PermissionService
}

func (a permissionServiceAccess) CanViewWorkspace(userID, workspaceID int) (bool, error) {
	return a.service.HasWorkspacePermission(userID, workspaceID, models.PermissionItemView)
}

func (a permissionServiceAccess) CanAdminWorkspace(userID, workspaceID int) (bool, error) {
	return a.service.HasWorkspacePermission(userID, workspaceID, models.PermissionWorkspaceAdmin)
}

func (a permissionServiceAccess) HasGlobalPermission(userID int, permission string) (bool, error) {
	return a.service.HasGlobalPermission(userID, permission)
}

func (a permissionServiceAccess) WorkspaceVisibility(userID int) (WorkspaceVisibility, error) {
	return a.service.WorkspaceVisibility(userID)
}

// newCatalogPermissionEnv wires a CatalogReadService to the real permission
// service through the production CatalogAccess delegation, so the tests
// exercise the actual snapshot decode path instead of a stub.
func newCatalogPermissionEnv(t *testing.T) (*permTestEnv, *CatalogReadService) {
	t.Helper()
	env := newPermTestEnv(t)
	catalog := NewCatalogReadService(env.db, env.service, permissionServiceAccess{service: env.service}, nil)
	return env, catalog
}

func (e *permTestEnv) insertActiveWorkspace(name string) int {
	e.tb.Helper()
	res, err := e.db.Exec(
		`INSERT INTO workspaces (name, key, description, active, is_personal, internal_comments_enabled) VALUES (?, ?, ?, true, false, false)`,
		name, name, "description of "+name)
	if err != nil {
		e.tb.Fatalf("insert workspace %s: %v", name, err)
	}
	wid, _ := res.LastInsertId()
	return int(wid)
}

func (e *permTestEnv) insertInactiveWorkspace(name string) int {
	e.tb.Helper()
	res, err := e.db.Exec(
		`INSERT INTO workspaces (name, key, description, active, is_personal, internal_comments_enabled) VALUES (?, ?, ?, false, false, false)`,
		name, name, "description of "+name)
	if err != nil {
		e.tb.Fatalf("insert inactive workspace %s: %v", name, err)
	}
	wid, _ := res.LastInsertId()
	return int(wid)
}

func (e *permTestEnv) assignWorkspaceRole(userID, workspaceID, roleID int) {
	e.tb.Helper()
	if err := repository.NewWorkspaceRoleRepository(e.db).AssignToUser(userID, workspaceID, roleID, userID); err != nil {
		e.tb.Fatalf("assign role %d to user %d on workspace %d: %v", roleID, userID, workspaceID, err)
	}
}

func (e *permTestEnv) gateWorkspace(workspaceID, gateUserID, roleID int) {
	e.tb.Helper()
	e.assignWorkspaceRole(gateUserID, workspaceID, roleID)
}

// listVisibleWorkspaceIDs returns the directory page as an ID set plus the
// reported total, so tests can assert exact membership.
func listVisibleWorkspaceIDs(t *testing.T, catalog *CatalogReadService, userID int, limit int) (map[int]bool, int) {
	t.Helper()
	rows, total, err := catalog.ListWorkspaces(userID, CatalogPageParams{Limit: limit})
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	ids := make(map[int]bool, len(rows))
	for _, ws := range rows {
		if ids[ws.ID] {
			t.Fatalf("workspace %d appeared twice in one page", ws.ID)
		}
		ids[ws.ID] = true
	}
	return ids, total
}

func TestCatalogListWorkspacesDecodesSnapshotOncePerRequest(t *testing.T) {
	env, catalog := newCatalogPermissionEnv(t)
	readerID := env.insertUser("directory-reader@example.test")
	gaterID := env.insertUser("directory-gater@example.test")
	viewerRoleID := env.roleID("Viewer")

	const seeded = 40
	ids := make([]int, 0, seeded)
	for i := 0; i < seeded; i++ {
		ids = append(ids, env.insertActiveWorkspace(fmt.Sprintf("Dir workspace %02d", i)))
	}
	// Gate the last workspace to another user: it becomes restricted and the
	// reader, holding no grant, must not see it.
	env.gateWorkspace(ids[seeded-1], gaterID, viewerRoleID)
	// Gate workspace 0 as well, but grant the reader an explicit Viewer role:
	// visible now, and hidden again once that grant is revoked below.
	env.gateWorkspace(ids[0], gaterID, viewerRoleID)
	// Explicit grants on three more workspaces: still visible, but exercised
	// through the explicit-assignment branch of the snapshot rather than the
	// everyone fallback alone.
	for _, id := range ids[:3] {
		env.assignWorkspaceRole(readerID, id, viewerRoleID)
	}

	wantTotal := seeded - 1
	wantVisible := make(map[int]bool, wantTotal)
	for _, id := range ids[:seeded-1] {
		wantVisible[id] = true
	}
	hiddenID := ids[seeded-1]

	// Warm the user's permission byte cache: one build plus one decode.
	if _, err := env.service.AccessibleWorkspaceIDs(readerID); err != nil {
		t.Fatalf("warm permission snapshot: %v", err)
	}

	before := env.service.GetWorkspaceAccessStats().PermissionSnapshotDecodes
	visible, total := listVisibleWorkspaceIDs(t, catalog, readerID, 1000)
	if decodes := env.service.GetWorkspaceAccessStats().PermissionSnapshotDecodes - before; decodes != 1 {
		t.Fatalf("permission snapshot decodes for one warm directory request = %d, want 1", decodes)
	}
	if total != wantTotal {
		t.Fatalf("total = %d, want %d", total, wantTotal)
	}
	for id := range wantVisible {
		if !visible[id] {
			t.Fatalf("open or granted workspace %d missing from directory", id)
		}
	}
	if visible[hiddenID] {
		t.Fatalf("gated workspace %d without a reader grant leaked into directory", hiddenID)
	}

	// Revocation must stay effective through the request-scoped snapshot.
	// After invalidation the next request rebuilds the byte cache (a build
	// returns the snapshot directly, with no decode), and the request after
	// that decodes the rebuilt snapshot exactly once.
	if _, err := repository.NewWorkspaceRoleRepository(env.db).RevokeFromUser(readerID, ids[0], viewerRoleID); err != nil {
		t.Fatalf("revoke reader role: %v", err)
	}
	if err := env.service.InvalidateUserCache(readerID); err != nil {
		t.Fatalf("invalidate permission cache: %v", err)
	}
	visible, total = listVisibleWorkspaceIDs(t, catalog, readerID, 1000)
	if visible[ids[0]] {
		t.Fatalf("revoked workspace %d still listed after invalidation", ids[0])
	}
	if total != wantTotal-1 {
		t.Fatalf("total after revocation = %d, want %d", total, wantTotal-1)
	}
	before = env.service.GetWorkspaceAccessStats().PermissionSnapshotDecodes
	if _, total = listVisibleWorkspaceIDs(t, catalog, readerID, 1000); total != wantTotal-1 {
		t.Fatalf("total on steady-state request = %d, want %d", total, wantTotal-1)
	}
	if decodes := env.service.GetWorkspaceAccessStats().PermissionSnapshotDecodes - before; decodes != 1 {
		t.Fatalf("permission snapshot decodes for steady-state directory request = %d, want 1", decodes)
	}
}

func TestCatalogListWorkspacesPermissionVisibilityContract(t *testing.T) {
	cases := []struct {
		name string
		// setup seeds workspaces for a fresh env and returns the workspace
		// under test plus the expected visible total for the reader.
		setup      func(env *permTestEnv, readerID int) (workspaceID int, wantTotal int)
		wantInList bool
	}{
		{
			name: "openWorkspaceVisibleWithoutGrant",
			setup: func(env *permTestEnv, readerID int) (int, int) {
				return env.insertActiveWorkspace("Open workspace"), 1
			},
			wantInList: true,
		},
		{
			name: "gatedWorkspaceHiddenWithoutGrant",
			setup: func(env *permTestEnv, readerID int) (int, int) {
				gaterID := env.insertUser("gate-holder@example.test")
				id := env.insertActiveWorkspace("Gated hidden workspace")
				env.gateWorkspace(id, gaterID, env.roleID("Viewer"))
				return id, 0
			},
			wantInList: false,
		},
		{
			name: "gatedWorkspaceVisibleWithViewerGrant",
			setup: func(env *permTestEnv, readerID int) (int, int) {
				gaterID := env.insertUser("gate-holder@example.test")
				id := env.insertActiveWorkspace("Gated granted workspace")
				env.gateWorkspace(id, gaterID, env.roleID("Viewer"))
				env.assignWorkspaceRole(readerID, id, env.roleID("Viewer"))
				return id, 1
			},
			wantInList: true,
		},
		{
			name: "inactiveWorkspaceHiddenWithoutGrant",
			setup: func(env *permTestEnv, readerID int) (int, int) {
				return env.insertInactiveWorkspace("Inactive bare workspace"), 0
			},
			wantInList: false,
		},
		{
			name: "inactiveWorkspaceHiddenWithViewerGrant",
			setup: func(env *permTestEnv, readerID int) (int, int) {
				id := env.insertInactiveWorkspace("Inactive viewed workspace")
				env.assignWorkspaceRole(readerID, id, env.roleID("Viewer"))
				return id, 0
			},
			wantInList: false,
		},
		{
			name: "inactiveWorkspaceVisibleWithAdministratorGrant",
			setup: func(env *permTestEnv, readerID int) (int, int) {
				id := env.insertInactiveWorkspace("Inactive administered workspace")
				env.assignWorkspaceRole(readerID, id, env.roleID("Administrator"))
				return id, 1
			},
			wantInList: true,
		},
		{
			name: "systemAdminSeesGatedWorkspaceWithoutGrant",
			setup: func(env *permTestEnv, readerID int) (int, int) {
				env.grantUserGlobal(readerID, "system.admin")
				gaterID := env.insertUser("gate-holder@example.test")
				id := env.insertActiveWorkspace("Gated admin workspace")
				env.gateWorkspace(id, gaterID, env.roleID("Viewer"))
				return id, 1
			},
			wantInList: true,
		},
		{
			name: "systemAdminSeesInactiveWorkspaceWithoutGrant",
			setup: func(env *permTestEnv, readerID int) (int, int) {
				env.grantUserGlobal(readerID, "system.admin")
				return env.insertInactiveWorkspace("Inactive admin workspace"), 1
			},
			wantInList: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, catalog := newCatalogPermissionEnv(t)
			readerID := env.insertUser("contract-reader@example.test")
			workspaceID, wantTotal := tc.setup(env, readerID)

			visible, total := listVisibleWorkspaceIDs(t, catalog, readerID, 100)

			if total != wantTotal {
				t.Fatalf("total = %d, want %d", total, wantTotal)
			}
			if visible[workspaceID] != tc.wantInList {
				t.Fatalf("workspace %d in list = %v, want %v (visible=%v)", workspaceID, visible[workspaceID], tc.wantInList, visible)
			}
		})
	}
}

// BenchmarkCatalogListWorkspaces10kWorkspaces measures the paged directory
// request against 10,000 candidate workspaces. The decode budget is part of
// the contract: every request must decode the user's permission snapshot
// exactly once, independent of candidate count.
func BenchmarkCatalogListWorkspaces10kWorkspaces(b *testing.B) {
	env := newPermTestEnv(b)
	readerID := env.insertUser("directory-bench@example.test")

	const workspaces = 10000
	tx, err := env.db.Begin()
	if err != nil {
		b.Fatalf("begin seed transaction: %v", err)
	}
	for i := 0; i < workspaces; i++ {
		name := fmt.Sprintf("Bench workspace %05d", i)
		if _, err := tx.Exec(
			`INSERT INTO workspaces (name, key, description, active, is_personal, internal_comments_enabled) VALUES (?, ?, ?, true, false, false)`,
			name, fmt.Sprintf("BENCH%05d", i), "description of "+name,
		); err != nil {
			b.Fatalf("seed workspace %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatalf("commit seed: %v", err)
	}

	catalog := NewCatalogReadService(env.db, env.service, permissionServiceAccess{service: env.service}, nil)
	page := CatalogPageParams{Limit: 200, Sort: "name"}

	if _, err := env.service.AccessibleWorkspaceIDs(readerID); err != nil {
		b.Fatalf("warm permission snapshot: %v", err)
	}
	if _, _, err := catalog.ListWorkspaces(readerID, page); err != nil {
		b.Fatalf("warm directory request: %v", err)
	}

	before := env.service.GetWorkspaceAccessStats().PermissionSnapshotDecodes
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, total, err := catalog.ListWorkspaces(readerID, page)
		if err != nil {
			b.Fatalf("directory request: %v", err)
		}
		if total != workspaces || len(rows) != page.Limit {
			b.Fatalf("total = %d rows = %d, want %d/%d", total, len(rows), workspaces, page.Limit)
		}
	}
	if decodes := env.service.GetWorkspaceAccessStats().PermissionSnapshotDecodes - before; decodes != uint64(b.N) {
		b.Fatalf("permission snapshot decodes = %d for %d requests, want one decode per request", decodes, b.N)
	}
}
