package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestWorkspaceRolesV2Provisioning exercises the public v2 workspace-role
// definition surface (WI-1306): reads, admin-gated writes, name-conflict and
// system-role protection, and the non-admin denial contract.
func TestWorkspaceRolesV2Provisioning(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	t.Run("mutations_require_system_admin", func(t *testing.T) {
		// Non-admin users cannot request admin scopes at token creation, so
		// this token holds only non-admin scopes — exercising scope + role
		// denial together.
		_, _, editorPass := CreateTestUserWithCredentials(t, server, "wrv2editor", "wrv2editor@test.local")
		_, editorToken := CreateAuthCredentialsForUser(t, server, "wrv2editor", editorPass)

		resp := MakeBearerRequestWithToken(t, server, editorToken, http.MethodPost, "/rest/api/v2/workspace-roles", map[string]interface{}{
			"name": "Editor Role",
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	t.Run("create_list_update_delete_roundtrip", func(t *testing.T) {
		createResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/workspace-roles", map[string]interface{}{
			"name": "Provisioned Role", "description": "from v2",
		})
		defer createResp.Body.Close()
		AssertStatusCode(t, createResp, http.StatusCreated)
		var created struct {
			Data struct {
				ID       int    `json:"id"`
				Name     string `json:"name"`
				IsSystem bool   `json:"is_system"`
			} `json:"data"`
		}
		DecodeJSON(t, createResp, &created)
		roleID := created.Data.ID
		if roleID == 0 || created.Data.IsSystem {
			t.Fatalf("created = %#v", created.Data)
		}

		dupResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/workspace-roles", map[string]interface{}{
			"name": "Provisioned Role",
		})
		defer dupResp.Body.Close()
		AssertStatusCode(t, dupResp, http.StatusConflict)

		updateResp := MakeBearerRequest(t, server, http.MethodPut, fmt.Sprintf("/rest/api/v2/workspace-roles/%d", roleID), map[string]interface{}{
			"name": "Provisioned Role Renamed", "description": "",
		})
		defer updateResp.Body.Close()
		AssertStatusCode(t, updateResp, http.StatusOK)

		listResp := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/workspace-roles", nil)
		defer listResp.Body.Close()
		AssertStatusCode(t, listResp, http.StatusOK)

		deleteResp := MakeBearerRequest(t, server, http.MethodDelete, fmt.Sprintf("/rest/api/v2/workspace-roles/%d", roleID), nil)
		defer deleteResp.Body.Close()
		AssertStatusCode(t, deleteResp, http.StatusNoContent)
	})

	t.Run("system_roles_cannot_be_deleted", func(t *testing.T) {
		roles := GetWorkspaceRoles(t, server)
		adminRoleID, ok := roles["Administrator"]
		if !ok {
			t.Fatalf("no Administrator role in %v", roles)
		}
		resp := MakeBearerRequest(t, server, http.MethodDelete, fmt.Sprintf("/rest/api/v2/workspace-roles/%d", adminRoleID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})
}
