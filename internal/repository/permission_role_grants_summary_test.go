//go:build test

package repository

import (
	"testing"

	"windshift/internal/testutils"
)

// TestListUserWorkspaceRoleGrantsHonorsPermissionsEnabled covers WI-1362: the
// /users/{id}/permissions summary source must ignore roles with
// permissions_enabled = false, exactly like the permission cache does.
//
// Fixture exception: the state under test — a permissions_enabled=false role
// WITH attached permission rows — cannot be created through the API (role
// creation forces permissions_enabled=false and accepts no permission rows),
// so the fixture writes the join rows directly.
func TestListUserWorkspaceRoleGrantsHonorsPermissionsEnabled(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	repo := NewPermissionRepository(tdb.Database)

	insertRole := func(t *testing.T, name string, permissionsEnabled bool) int {
		t.Helper()
		roleID := testutils.InsertID(t, tdb.Database, `
			INSERT INTO workspace_roles (name, description, is_system, permissions_enabled)
			VALUES (?, ?, false, ?)
		`, name, "WI-1362 fixture role", permissionsEnabled)
		return roleID
	}

	attachPermission := func(t *testing.T, roleID int, permissionKey string) {
		t.Helper()
		if _, err := tdb.ExecWrite(`
			INSERT INTO role_permissions (role_id, permission_id)
			SELECT ?, id FROM permissions WHERE permission_key = ?
		`, roleID, permissionKey); err != nil {
			t.Fatalf("attach permission %s to role %d: %v", permissionKey, roleID, err)
		}
	}

	workspaceID := testutils.InsertID(t, tdb.Database, `
		INSERT INTO workspaces (name, key, description, active)
		VALUES ('WI-1362 WS', 'W1362', '', true)
	`)
	userID := testutils.InsertID(t, tdb.Database, `
		INSERT INTO users (username, email, password_hash, first_name, last_name, is_active)
		VALUES ('wi1362user', 'wi1362user@test.local', 'x', 'WI', '1362', true)
	`)

	labelOnlyRoleID := insertRole(t, "WI-1362 Label-only", false)
	attachPermission(t, labelOnlyRoleID, "item.view")
	if _, err := tdb.ExecWrite(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id)
		VALUES (?, ?, ?)
	`, userID, workspaceID, labelOnlyRoleID); err != nil {
		t.Fatalf("assign label-only role: %v", err)
	}

	enabledRoleID := insertRole(t, "WI-1362 Enabled", true)
	attachPermission(t, enabledRoleID, "item.comment")
	if _, err := tdb.ExecWrite(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id)
		VALUES (?, ?, ?)
	`, userID, workspaceID, enabledRoleID); err != nil {
		t.Fatalf("assign enabled role: %v", err)
	}

	grants, err := repo.ListUserWorkspaceRoleGrants(userID)
	if err != nil {
		t.Fatalf("ListUserWorkspaceRoleGrants: %v", err)
	}

	got := map[string]bool{}
	for _, g := range grants {
		if g.WorkspaceID == workspaceID && g.Permission != nil {
			got[g.Permission.PermissionKey] = true
		}
	}

	if got["item.view"] {
		t.Errorf("label-only role (permissions_enabled=false) leaked item.view into the summary")
	}
	if !got["item.comment"] {
		t.Errorf("enabled role's item.comment missing from summary grants; got %v", got)
	}
}
