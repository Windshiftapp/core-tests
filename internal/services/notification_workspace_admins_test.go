//go:build test

package services

import (
	"sort"
	"testing"
)

// TestGetWorkspaceAdminsIncludesGroupAssignedAdmins covers the bug hunt finding
// that "Workspace Admins" notifications only reached directly-assigned admins,
// even though workspace permissions honor group-assigned roles.
func TestGetWorkspaceAdminsIncludesGroupAssignedAdmins(t *testing.T) {
	env := newPermTestEnv(t)
	workspaceID := env.insertWorkspace("ADMINS")
	directAdminID := env.insertUser("direct-admin@example.com")
	groupAdminID := env.insertUser("group-admin@example.com")
	viewerID := env.insertUser("viewer@example.com")

	adminRoleID := env.roleID("Administrator")
	env.assignUserWorkspaceRole(directAdminID, workspaceID, adminRoleID)
	groupID := env.insertGroup("workspace-admins", true)
	env.addGroupMember(groupID, groupAdminID)
	env.assignGroupWorkspaceRole(groupID, workspaceID, adminRoleID)
	env.assignUserWorkspaceRole(viewerID, workspaceID, env.roleID("Viewer"))

	service := NewNotificationService(env.db, nil, env.service, DefaultNotificationServiceConfig())
	t.Cleanup(func() { _ = service.Close() })

	got := service.getWorkspaceAdmins(workspaceID)
	sort.Ints(got)
	want := []int{directAdminID, groupAdminID}
	sort.Ints(want)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("workspace admins = %v, want %v", got, want)
	}
}
