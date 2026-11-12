//go:build test

package services

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// permTestEnv is the minimal setup for exercising group-derived permissions.
// SQLite in-memory + initialized schema (which seeds the permissions table
// including 'system.admin' and the default workspace roles).
type permTestEnv struct {
	tb      testing.TB
	db      database.Database
	service *PermissionService
}

func newPermTestEnv(tb testing.TB) *permTestEnv {
	tb.Helper()

	dsn := fmt.Sprintf("file:permcache-%s?mode=memory&cache=shared", strings.ReplaceAll(tb.Name(), "/", "_"))
	db, err := database.NewSQLiteDB(dsn)
	if err != nil {
		tb.Fatalf("open sqlite: %v", err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		tb.Fatalf("init schema: %v", err)
	}
	svc, err := NewPermissionService(db, DefaultPermissionCacheConfig())
	if err != nil {
		tb.Fatalf("permission service: %v", err)
	}
	return &permTestEnv{tb: tb, db: db, service: svc}
}

func (e *permTestEnv) insertUser(email string) int {
	e.tb.Helper()
	uid := testutils.InsertID(e.tb, e.db, `INSERT INTO users (email, username, first_name, last_name) VALUES (?, ?, ?, '')`, email, email, email)
	return int(uid)
}

func (e *permTestEnv) insertGroup(name string, active bool) int {
	e.tb.Helper()
	gid := testutils.InsertID(e.tb, e.db, `INSERT INTO groups (name, is_active) VALUES (?, ?)`, name, active)
	return int(gid)
}

func (e *permTestEnv) addGroupMember(groupID, userID int) {
	e.tb.Helper()
	if _, err := e.db.Exec(`INSERT INTO group_members (group_id, user_id) VALUES (?, ?)`, groupID, userID); err != nil {
		e.tb.Fatalf("add group member: %v", err)
	}
}

func (e *permTestEnv) permissionID(key string) int {
	e.tb.Helper()
	var id int
	if err := e.db.QueryRow(`SELECT id FROM permissions WHERE permission_key = ?`, key).Scan(&id); err != nil {
		e.tb.Fatalf("permission %s: %v", key, err)
	}
	return id
}

func (e *permTestEnv) grantUserGlobal(userID int, permKey string) {
	e.tb.Helper()
	if _, err := e.db.Exec(`INSERT INTO user_global_permissions (user_id, permission_id) VALUES (?, ?)`, userID, e.permissionID(permKey)); err != nil {
		e.tb.Fatalf("grant user global %s: %v", permKey, err)
	}
}

func (e *permTestEnv) grantGroupGlobal(groupID int, permKey string) {
	e.tb.Helper()
	if _, err := e.db.Exec(`INSERT INTO group_global_permissions (group_id, permission_id) VALUES (?, ?)`, groupID, e.permissionID(permKey)); err != nil {
		e.tb.Fatalf("grant group global %s: %v", permKey, err)
	}
}

func (e *permTestEnv) insertWorkspace(name string) int {
	e.tb.Helper()
	wid := testutils.InsertID(e.tb, e.db, `INSERT INTO workspaces (name, key, active, is_personal) VALUES (?, ?, true, false)`, name, name)
	return int(wid)
}

func (e *permTestEnv) roleID(name string) int {
	e.tb.Helper()
	var id int
	if err := e.db.QueryRow(`SELECT id FROM workspace_roles WHERE name = ?`, name).Scan(&id); err != nil {
		e.tb.Fatalf("role %s: %v", name, err)
	}
	return id
}

func (e *permTestEnv) assignGroupWorkspaceRole(groupID, workspaceID, roleID int) {
	e.tb.Helper()
	if _, err := e.db.Exec(`INSERT INTO group_workspace_roles (group_id, workspace_id, role_id) VALUES (?, ?, ?)`, groupID, workspaceID, roleID); err != nil {
		e.tb.Fatalf("assign group workspace role: %v", err)
	}
}

func (e *permTestEnv) assignUserWorkspaceRole(userID, workspaceID, roleID int) {
	e.tb.Helper()
	if _, err := e.db.Exec(`INSERT INTO user_workspace_roles (user_id, workspace_id, role_id) VALUES (?, ?, ?)`, userID, workspaceID, roleID); err != nil {
		e.tb.Fatalf("assign user workspace role: %v", err)
	}
}

func TestPermissionCache_IsSystemAdmin_DirectGrant(t *testing.T) {
	env := newPermTestEnv(t)
	uid := env.insertUser("alice@example.com")
	env.grantUserGlobal(uid, "system.admin")

	got, err := env.service.IsSystemAdmin(uid)
	if err != nil {
		t.Fatalf("IsSystemAdmin: %v", err)
	}
	if !got {
		t.Fatal("expected direct grant to make user system admin")
	}
}

func TestPermissionCache_IsSystemAdmin_ViaActiveGroup(t *testing.T) {
	env := newPermTestEnv(t)
	uid := env.insertUser("bob@example.com")
	gid := env.insertGroup("admins", true)
	env.addGroupMember(gid, uid)
	env.grantGroupGlobal(gid, "system.admin")

	got, err := env.service.IsSystemAdmin(uid)
	if err != nil {
		t.Fatalf("IsSystemAdmin: %v", err)
	}
	if !got {
		t.Fatal("expected group-granted system.admin (active group) to make user system admin")
	}
}

func TestPermissionCache_IsSystemAdmin_ViaInactiveGroup(t *testing.T) {
	env := newPermTestEnv(t)
	uid := env.insertUser("carol@example.com")
	gid := env.insertGroup("disabled-admins", false)
	env.addGroupMember(gid, uid)
	env.grantGroupGlobal(gid, "system.admin")

	got, err := env.service.IsSystemAdmin(uid)
	if err != nil {
		t.Fatalf("IsSystemAdmin: %v", err)
	}
	if got {
		t.Fatal("expected inactive group not to confer system admin")
	}
}

// Regression for issue #290: system admins used to short-circuit the permission
// snapshot build, leaving GroupMemberships empty so a user_in_group condition on
// the very group that grants System Administrator could never pass.
func TestPermissionCache_SystemAdminGroupGrant_RetainsGroupMembership(t *testing.T) {
	env := newPermTestEnv(t)
	uid := env.insertUser("admin-via-group@example.com")
	gid := env.insertGroup("admin-granting", true)
	env.addGroupMember(gid, uid)
	env.grantGroupGlobal(gid, "system.admin")

	cache, err := env.service.GetUserEffectivePermissions(uid)
	if err != nil {
		t.Fatalf("GetUserEffectivePermissions: %v", err)
	}
	if !cache.IsSystemAdmin {
		t.Fatal("expected group grant to make user system admin")
	}
	if !slices.Contains(cache.GroupMemberships, gid) {
		t.Fatalf("expected group %d in admin memberships; got %v", gid, cache.GroupMemberships)
	}
}

// Regression for issue #290: admins must still expose direct workspace role
// assignments so a user_in_role condition resolves for them.
func TestPermissionCache_SystemAdmin_RetainsRoleAssignments(t *testing.T) {
	env := newPermTestEnv(t)
	uid := env.insertUser("admin-with-role@example.com")
	env.grantUserGlobal(uid, "system.admin")
	gid := env.insertGroup("admin-extra", true)
	env.addGroupMember(gid, uid)
	wsID := env.insertWorkspace("ws-admin-role")
	roleID := env.roleID("Viewer")
	env.assignUserWorkspaceRole(uid, wsID, roleID)

	cache, err := env.service.GetUserEffectivePermissions(uid)
	if err != nil {
		t.Fatalf("GetUserEffectivePermissions: %v", err)
	}
	if !cache.IsSystemAdmin {
		t.Fatal("expected direct grant to make user system admin")
	}
	if !slices.Contains(cache.GroupMemberships, gid) {
		t.Fatalf("expected group %d in admin memberships; got %v", gid, cache.GroupMemberships)
	}
	if !slices.Contains(cache.RoleAssignments[wsID], roleID) {
		t.Fatalf("expected role %d in admin role assignments for workspace %d; got %v", roleID, wsID, cache.RoleAssignments)
	}

	hasRole, err := env.service.HasWorkspaceRole(uid, wsID, roleID)
	if err != nil {
		t.Fatalf("HasWorkspaceRole: %v", err)
	}
	if !hasRole {
		t.Fatal("expected user_in_role check to pass for admin with direct role assignment")
	}
}

func TestPermissionCache_GroupGlobalPermission_LoadedIntoCache(t *testing.T) {
	env := newPermTestEnv(t)
	uid := env.insertUser("dave@example.com")
	gid := env.insertGroup("creators", true)
	env.addGroupMember(gid, uid)
	env.grantGroupGlobal(gid, "workspace.create")

	cache, err := env.service.GetUserEffectivePermissions(uid)
	if err != nil {
		t.Fatalf("GetUserPermissionCache: %v", err)
	}
	if !cache.GlobalPermissions["workspace.create"] {
		t.Fatalf("expected workspace.create in cache; got %v", cache.GlobalPermissions)
	}
}

func TestPermissionCache_GroupWorkspaceRole_InactiveGroupDenies(t *testing.T) {
	env := newPermTestEnv(t)
	uid := env.insertUser("eve@example.com")
	wsID := env.insertWorkspace("ws-eve")
	gid := env.insertGroup("ws-viewers", false)
	env.addGroupMember(gid, uid)
	env.assignGroupWorkspaceRole(gid, wsID, env.roleID("Viewer"))

	cache, err := env.service.GetUserEffectivePermissions(uid)
	if err != nil {
		t.Fatalf("GetUserPermissionCache: %v", err)
	}
	if cache.WorkspacePermissions[wsID]["item.view"] {
		t.Fatalf("inactive group must not grant item.view; got %v", cache.WorkspacePermissions[wsID])
	}
	for _, m := range cache.GroupMemberships {
		if m == gid {
			t.Fatalf("inactive group %d should not appear in cached memberships", gid)
		}
	}
}

func TestPermissionCache_GroupWorkspaceRole_ActiveGroupGrants(t *testing.T) {
	env := newPermTestEnv(t)
	uid := env.insertUser("frank@example.com")
	wsID := env.insertWorkspace("ws-frank")
	gid := env.insertGroup("ws-viewers", true)
	env.addGroupMember(gid, uid)
	env.assignGroupWorkspaceRole(gid, wsID, env.roleID("Viewer"))

	cache, err := env.service.GetUserEffectivePermissions(uid)
	if err != nil {
		t.Fatalf("GetUserPermissionCache: %v", err)
	}
	if !cache.WorkspacePermissions[wsID]["item.view"] {
		t.Fatalf("active group should grant item.view; got %v", cache.WorkspacePermissions[wsID])
	}
}

// Regression: getGroupMembers used to query a non-existent `user_groups` table.
// OnGroupPermissionChanged calls it via InvalidateGroupMemberCaches; if the
// helper raises a SQL error, no caches get invalidated and stale grants leak.
func TestPermissionCache_OnGroupPermissionChanged_InvalidatesMembers(t *testing.T) {
	env := newPermTestEnv(t)
	uid := env.insertUser("grace@example.com")
	gid := env.insertGroup("globals", true)
	env.addGroupMember(gid, uid)

	// Prime the cache so we can observe invalidation. getUserPermissionCache
	// returns an error on cache miss; nil error == cached.
	if _, err := env.service.GetUserEffectivePermissions(uid); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	if _, err := env.service.getUserPermissionCache(uid); err != nil {
		t.Fatalf("expected cache primed for user, got %v", err)
	}

	if err := env.service.OnGroupPermissionChanged(gid); err != nil {
		t.Fatalf("OnGroupPermissionChanged: %v", err)
	}
	if _, err := env.service.getUserPermissionCache(uid); err == nil {
		t.Fatal("expected user cache invalidated after OnGroupPermissionChanged")
	}
}
