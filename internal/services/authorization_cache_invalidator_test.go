package services

import (
	"path/filepath"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
)

func TestGroupDeletePlanUsesPreDeleteMembersAndInvalidatesAfterCommit(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "group-delete-invalidation.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	userResult, err := db.ExecWrite(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('group-member@example.test', 'group-member', 'Group', 'Member', true)
	`)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID64, err := userResult.LastInsertId()
	if err != nil {
		t.Fatalf("user LastInsertId: %v", err)
	}
	userID := int(userID64)
	workspaceResult, err := db.ExecWrite(`
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Group Delete', 'GRPDEL', '', true, false)
	`)
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	workspaceID64, err := workspaceResult.LastInsertId()
	if err != nil {
		t.Fatalf("workspace LastInsertId: %v", err)
	}
	workspaceID := int(workspaceID64)
	groupResult, err := db.ExecWrite(`
		INSERT INTO groups (name, description, is_active) VALUES ('Delete Me', '', true)
	`)
	if err != nil {
		t.Fatalf("insert group: %v", err)
	}
	groupID64, err := groupResult.LastInsertId()
	if err != nil {
		t.Fatalf("group LastInsertId: %v", err)
	}
	groupID := int(groupID64)
	if _, err := db.ExecWrite(`INSERT INTO group_members (group_id, user_id) VALUES (?, ?)`, groupID, userID); err != nil {
		t.Fatalf("insert group member: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO group_workspace_roles (group_id, workspace_id, role_id)
		SELECT ?, ?, id FROM workspace_roles WHERE builtin_key = ?
	`, groupID, workspaceID, models.RoleBuiltinAdministrator); err != nil {
		t.Fatalf("assign group Administrator: %v", err)
	}

	config := DefaultPermissionCacheConfig()
	config.TTL = time.Minute
	config.WarmupOnStartup = false
	permissionService, err := NewPermissionService(db, config)
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = permissionService.Close() })
	invalidator := NewAuthorizationCacheInvalidator(permissionService, nil)

	allowed, err := permissionService.HasWorkspacePermission(userID, workspaceID, models.PermissionWorkspaceAdmin)
	if err != nil {
		t.Fatalf("warm group permission: %v", err)
	}
	if !allowed {
		t.Fatal("group Administrator permission was not granted")
	}
	plan, err := invalidator.GroupDeletePlan(groupID)
	if err != nil {
		t.Fatalf("GroupDeletePlan: %v", err)
	}
	if plan.ResetPermissions {
		t.Fatal("non-default Administrator deletion requested a global reset")
	}
	if len(plan.UserIDs) != 1 || plan.UserIDs[0] != userID {
		t.Fatalf("captured users = %v, want [%d]", plan.UserIDs, userID)
	}
	if _, err := db.ExecWrite(`DELETE FROM groups WHERE id = ?`, groupID); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	if err := invalidator.Apply(plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	allowed, err = permissionService.HasWorkspacePermission(userID, workspaceID, models.PermissionWorkspaceAdmin)
	if err != nil {
		t.Fatalf("permission after group delete: %v", err)
	}
	if allowed {
		t.Fatal("pre-delete group permission remained usable after invalidation")
	}
}

func TestGroupDeletePlanDetectsLastDefaultRoleAssignment(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "group-everyone-plan.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	workspaceResult, err := db.ExecWrite(`INSERT INTO workspaces (name, key, active) VALUES ('Plan', 'PLAN', true)`)
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	workspaceID, _ := workspaceResult.LastInsertId()
	groupResult, err := db.ExecWrite(`INSERT INTO groups (name, description, is_active) VALUES ('Gate', '', true)`)
	if err != nil {
		t.Fatalf("insert group: %v", err)
	}
	groupID, _ := groupResult.LastInsertId()
	if _, err := db.ExecWrite(`
		INSERT INTO group_workspace_roles (group_id, workspace_id, role_id)
		SELECT ?, ?, id FROM workspace_roles WHERE builtin_key = ?
	`, groupID, workspaceID, models.RoleBuiltinViewer); err != nil {
		t.Fatalf("assign Viewer: %v", err)
	}
	permissionService, err := NewPermissionService(db, PermissionCacheConfig{TTL: time.Minute, MaxCacheSize: 16, BatchSize: 10})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = permissionService.Close() })
	plan, err := NewAuthorizationCacheInvalidator(permissionService, nil).GroupDeletePlan(int(groupID))
	if err != nil {
		t.Fatalf("GroupDeletePlan: %v", err)
	}
	if !plan.ResetPermissions {
		t.Fatal("deleting the last Viewer assignment did not request a global reset")
	}
}
