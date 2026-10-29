package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestWorkspaceRoleAssignment_WorkspaceAdminAccess covers WI-1358: workspace
// Administrators (without system admin) must be able to manage workspace
// members through the role-assignment endpoints, while non-admins and
// cross-workspace admins are denied.
//
// Denial contracts are intentionally different by surface:
//   - Routes with workspaceId in the path use the permission middleware and
//     respond 403 (insufficient permission).
//   - POST /workspace-roles/assign* carry the workspace in the body; their
//     handlers respond 404 so workspace existence is not disclosed.
func TestWorkspaceRoleAssignment_WorkspaceAdminAccess(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()

	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Role Admin Workspace", shortKey("RAW"))
	LockDownWorkspace(t, server, workspaceID)
	otherWorkspaceID, _ := CreateTestWorkspace(t, server, "Role Admin Other Workspace", shortKey("ROV"))
	LockDownWorkspace(t, server, otherWorkspaceID)

	wsAdminID, wsAdminUser, wsAdminPass := CreateTestUserWithCredentials(t, server, "role_ws_admin", "role-ws-admin@test.com")
	AssignWorkspaceRole(t, server, wsAdminID, workspaceID, "Administrator")
	wsAdminToken := CreateBearerTokenForUser(t, server, wsAdminUser, wsAdminPass)

	editorID, editorUser, editorPass := CreateTestUserWithCredentials(t, server, "role_editor", "role-editor@test.com")
	AssignWorkspaceRole(t, server, editorID, workspaceID, "Editor")
	editorToken := CreateBearerTokenForUser(t, server, editorUser, editorPass)

	memberID, _, _ := CreateTestUserWithCredentials(t, server, "role_member", "role-member@test.com")
	AssignWorkspaceRole(t, server, memberID, workspaceID, "Viewer")

	groupResp := MakeAuthRequest(t, server, http.MethodPost, "/v2/admin/groups", map[string]interface{}{
		"name":        "Role admin contract group",
		"description": "WI-1358 group assignment coverage",
	})
	AssertStatusCode(t, groupResp, http.StatusCreated)
	var groupResult map[string]interface{}
	DecodeJSON(t, groupResp, &groupResult)
	groupResp.Body.Close()
	groupID := ExtractIDFromResponse(t, groupResult)

	roles := GetWorkspaceRoles(t, server)

	t.Run("workspace_admin_lists_role_assignments", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, wsAdminToken, http.MethodGet,
			fmt.Sprintf("/workspaces/%d/role-assignments", workspaceID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("workspace_admin_lists_group_role_assignments", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, wsAdminToken, http.MethodGet,
			fmt.Sprintf("/workspaces/%d/group-role-assignments", workspaceID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("workspace_admin_assigns_role_to_user", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, wsAdminToken, http.MethodPost, "/workspace-roles/assign", map[string]interface{}{
			"user_id":      memberID,
			"workspace_id": workspaceID,
			"role_id":      roles["Editor"],
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)

		// The assignment must be visible in the workspace member list.
		listResp := MakeAuthRequestWithToken(t, server, wsAdminToken, http.MethodGet,
			fmt.Sprintf("/workspaces/%d/role-assignments", workspaceID), nil)
		defer listResp.Body.Close()
		AssertStatusCode(t, listResp, http.StatusOK)

		var members []map[string]interface{}
		DecodeJSON(t, listResp, &members)
		found := false
		for _, m := range members {
			if int(m["user_id"].(float64)) == memberID {
				for _, r := range m["roles"].([]interface{}) {
					if int(r.(map[string]interface{})["role_id"].(float64)) == roles["Editor"] {
						found = true
					}
				}
			}
		}
		if !found {
			t.Fatalf("expected user %d to hold Editor role %d after assignment", memberID, roles["Editor"])
		}
	})

	t.Run("workspace_admin_revokes_role_from_user", func(t *testing.T) {
		endpoint := fmt.Sprintf("/users/%d/workspaces/%d/roles/%d", memberID, workspaceID, roles["Viewer"])
		resp := MakeAuthRequestWithToken(t, server, wsAdminToken, http.MethodDelete, endpoint, nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNoContent)

		listResp := MakeAuthRequestWithToken(t, server, wsAdminToken, http.MethodGet,
			fmt.Sprintf("/workspaces/%d/role-assignments", workspaceID), nil)
		defer listResp.Body.Close()
		AssertStatusCode(t, listResp, http.StatusOK)

		var members []map[string]interface{}
		DecodeJSON(t, listResp, &members)
		for _, m := range members {
			if int(m["user_id"].(float64)) != memberID {
				continue
			}
			for _, r := range m["roles"].([]interface{}) {
				if int(r.(map[string]interface{})["role_id"].(float64)) == roles["Viewer"] {
					t.Fatalf("expected Viewer role %d to be revoked from user %d", roles["Viewer"], memberID)
				}
			}
		}
	})

	t.Run("workspace_admin_assigns_role_to_group", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, wsAdminToken, http.MethodPost, "/workspace-roles/assign-group", map[string]interface{}{
			"group_id":     groupID,
			"workspace_id": workspaceID,
			"role_id":      roles["Viewer"],
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)

		listResp := MakeAuthRequestWithToken(t, server, wsAdminToken, http.MethodGet,
			fmt.Sprintf("/workspaces/%d/group-role-assignments", workspaceID), nil)
		defer listResp.Body.Close()
		AssertStatusCode(t, listResp, http.StatusOK)

		var groups []map[string]interface{}
		DecodeJSON(t, listResp, &groups)
		found := false
		for _, g := range groups {
			if int(g["group_id"].(float64)) == groupID {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected group %d to appear in group role assignments after assignment", groupID)
		}
	})

	t.Run("editor_cannot_list_role_assignments", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, editorToken, http.MethodGet,
			fmt.Sprintf("/workspaces/%d/role-assignments", workspaceID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	t.Run("editor_cannot_assign_roles", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, editorToken, http.MethodPost, "/workspace-roles/assign", map[string]interface{}{
			"user_id":      memberID,
			"workspace_id": workspaceID,
			"role_id":      roles["Viewer"],
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})

	t.Run("workspace_admin_of_other_workspace_cannot_list_role_assignments", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, wsAdminToken, http.MethodGet,
			fmt.Sprintf("/workspaces/%d/role-assignments", otherWorkspaceID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	t.Run("workspace_admin_of_other_workspace_cannot_assign_roles", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, wsAdminToken, http.MethodPost, "/workspace-roles/assign", map[string]interface{}{
			"user_id":      memberID,
			"workspace_id": otherWorkspaceID,
			"role_id":      roles["Viewer"],
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})

	t.Run("system_admin_retains_access", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, server.SessionCookie, http.MethodGet,
			fmt.Sprintf("/workspaces/%d/role-assignments", workspaceID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})
}
