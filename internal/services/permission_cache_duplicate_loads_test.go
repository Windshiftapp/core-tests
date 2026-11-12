//go:build test

package services

import (
	"slices"
	"testing"

	"windshift/internal/testutils"
)

// WI-1915: buildUserPermissionCache loaded group memberships and role
// assignments before the isAdmin early return and again after it, so every
// non-admin snapshot build paid two redundant queries.

const (
	groupMembershipsQuery  = "SELECT gm.group_id"
	roleAssignmentsQuery   = "SELECT workspace_id, role_id FROM user_workspace_roles WHERE user_id = ?"
	systemAdminGrantInsert = `INSERT INTO user_global_permissions (user_id, permission_id, granted_by, granted_at)
		VALUES (?, (SELECT id FROM permissions WHERE permission_key = 'system.admin'), ?, CURRENT_TIMESTAMP)`
)

// permissionBuildFixture seeds one user with a group membership and a workspace
// role, plus the service under test wired to a query-counting database.
type permissionBuildFixture struct {
	service   *PermissionService
	counting  *testutils.QueryCountingDB
	userID    int
	groupID   int
	roleID    int
	workspace int
}

func newPermissionBuildFixture(t *testing.T, makeAdmin bool) permissionBuildFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	counting := testutils.CountQueries(db)
	config := DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	service, err := NewPermissionService(counting, config)
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })

	userID := testutils.InsertID(t, db,
		`INSERT INTO users (email, username, first_name, last_name) VALUES (?, ?, ?, '')`,
		"build-loads@example.test", "build-loads", "Build")
	groupID := testutils.InsertID(t, db,
		`INSERT INTO groups (name, is_active) VALUES (?, true)`, "build-loads-group")
	if _, err := db.Exec(`INSERT INTO group_members (group_id, user_id) VALUES (?, ?)`, groupID, userID); err != nil {
		t.Fatalf("add group member: %v", err)
	}

	var roleID int
	if err := db.QueryRow(`SELECT id FROM workspace_roles WHERE name = 'Viewer' LIMIT 1`).Scan(&roleID); err != nil {
		t.Fatalf("find Viewer role: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`,
		userID, seed.WorkspaceID, roleID,
	); err != nil {
		t.Fatalf("assign workspace role: %v", err)
	}

	if makeAdmin {
		if _, err := db.Exec(systemAdminGrantInsert, userID, userID); err != nil {
			t.Fatalf("grant system admin: %v", err)
		}
	}

	return permissionBuildFixture{
		service:   service,
		counting:  counting,
		userID:    userID,
		groupID:   groupID,
		roleID:    roleID,
		workspace: seed.WorkspaceID,
	}
}

func TestBuildUserPermissionCacheLoadsMembershipsAndRolesOnceForNonAdmin(t *testing.T) {
	fixture := newPermissionBuildFixture(t, false)

	fixture.counting.ResetQueries()
	snapshot, err := fixture.service.GetUserEffectivePermissions(fixture.userID)
	if err != nil {
		t.Fatalf("GetUserEffectivePermissions: %v", err)
	}
	if snapshot.IsSystemAdmin {
		t.Fatal("fixture user must not be a system admin")
	}

	if got := fixture.counting.CountQueriesContaining(groupMembershipsQuery); got != 1 {
		t.Fatalf("group memberships loaded %d times in one non-admin build, want 1", got)
	}
	if got := fixture.counting.CountQueriesContaining(roleAssignmentsQuery); got != 1 {
		t.Fatalf("role assignments loaded %d times in one non-admin build, want 1", got)
	}
}

// The admin early return deliberately keeps memberships and role assignments so
// user_in_group and user_in_role conditions resolve against real data. Dropping
// the duplicate load must not empty them.
func TestBuildUserPermissionCacheKeepsMembershipsAndRolesForSystemAdmin(t *testing.T) {
	fixture := newPermissionBuildFixture(t, true)

	snapshot, err := fixture.service.GetUserEffectivePermissions(fixture.userID)
	if err != nil {
		t.Fatalf("GetUserEffectivePermissions: %v", err)
	}
	if !snapshot.IsSystemAdmin {
		t.Fatal("fixture user must be a system admin")
	}

	if !slices.Contains(snapshot.GroupMemberships, fixture.groupID) {
		t.Fatalf("admin snapshot group memberships = %v, want to contain %d",
			snapshot.GroupMemberships, fixture.groupID)
	}
	if !slices.Contains(snapshot.RoleAssignments[fixture.workspace], fixture.roleID) {
		t.Fatalf("admin snapshot roles for workspace %d = %v, want to contain %d",
			fixture.workspace, snapshot.RoleAssignments[fixture.workspace], fixture.roleID)
	}
}

// A non-admin snapshot must carry the same data as the admin one — the fix
// removes the second load, not the data.
func TestBuildUserPermissionCacheKeepsMembershipsAndRolesForNonAdmin(t *testing.T) {
	fixture := newPermissionBuildFixture(t, false)

	snapshot, err := fixture.service.GetUserEffectivePermissions(fixture.userID)
	if err != nil {
		t.Fatalf("GetUserEffectivePermissions: %v", err)
	}

	if !slices.Contains(snapshot.GroupMemberships, fixture.groupID) {
		t.Fatalf("non-admin snapshot group memberships = %v, want to contain %d",
			snapshot.GroupMemberships, fixture.groupID)
	}
	if !slices.Contains(snapshot.RoleAssignments[fixture.workspace], fixture.roleID) {
		t.Fatalf("non-admin snapshot roles for workspace %d = %v, want to contain %d",
			fixture.workspace, snapshot.RoleAssignments[fixture.workspace], fixture.roleID)
	}
}
