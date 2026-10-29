package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestWorkspaceConfigurationSetAssignment covers WI-1359: workspace
// Administrators can assign or clear their workspace's configuration set
// through PUT /workspaces/{id}/configuration-set, while non-admins and
// cross-workspace admins are denied and the default set stays unassignable.
func TestWorkspaceConfigurationSetAssignment(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()

	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Config Assign WS", shortKey("CAS"))
	otherWSID, _ := CreateTestWorkspace(t, server, "Config Assign Other WS", shortKey("CAO"))

	wsAdminID, wsAdminUser, wsAdminPass := CreateTestUserWithCredentials(t, server, "config_ws_admin", "config-ws-admin@test.local")
	AssignWorkspaceRole(t, server, wsAdminID, wsID, "Administrator")
	wsAdminToken := CreateBearerTokenForUser(t, server, wsAdminUser, wsAdminPass)

	editorID, editorUser, editorPass := CreateTestUserWithCredentials(t, server, "config_editor", "config-editor@test.local")
	AssignWorkspaceRole(t, server, editorID, wsID, "Editor")
	editorToken := CreateBearerTokenForUser(t, server, editorUser, editorPass)

	createSet := func(name string) int {
		resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/configuration-sets", map[string]interface{}{
			"name": name,
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var created struct {
			Data struct {
				ConfigurationSet struct {
					ID int `json:"id"`
				} `json:"configuration_set"`
			} `json:"data"`
		}
		DecodeJSON(t, resp, &created)
		return created.Data.ConfigurationSet.ID
	}

	setA := createSet("Assign Set A")
	setB := createSet("Assign Set B")

	// assignedSetID returns the configuration set currently attached to the
	// workspace (0 when none) by reading the auth-only v1 list.
	assignedSetID := func() int {
		resp := MakeAuthRequest(t, server, http.MethodGet, "/configuration-sets", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var body struct {
			ConfigurationSets []struct {
				ID           int    `json:"id"`
				IsDefault    bool   `json:"is_default"`
				WorkspaceIDs []int  `json:"workspace_ids"`
				Workspaces   string `json:"workspace_name"`
			} `json:"configuration_sets"`
		}
		DecodeJSON(t, resp, &body)
		for _, cs := range body.ConfigurationSets {
			for _, id := range cs.WorkspaceIDs {
				if id == wsID {
					return cs.ID
				}
			}
		}
		return 0
	}

	t.Run("workspace_admin_assigns_configuration_set", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, wsAdminToken, http.MethodPut,
			fmt.Sprintf("/workspaces/%d/configuration-set", wsID), map[string]interface{}{"configuration_set_id": setA})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		if got := assignedSetID(); got != setA {
			t.Fatalf("workspace %d assigned set = %d, want %d", wsID, got, setA)
		}
	})

	t.Run("assignment_switch_detaches_previous_set", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, wsAdminToken, http.MethodPut,
			fmt.Sprintf("/workspaces/%d/configuration-set", wsID), map[string]interface{}{"configuration_set_id": setB})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		if got := assignedSetID(); got != setB {
			t.Fatalf("workspace %d assigned set = %d, want %d", wsID, got, setB)
		}
	})

	t.Run("workspace_admin_unassigns_configuration_set", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, wsAdminToken, http.MethodPut,
			fmt.Sprintf("/workspaces/%d/configuration-set", wsID), map[string]interface{}{"configuration_set_id": nil})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		if got := assignedSetID(); got != 0 {
			t.Fatalf("workspace %d assigned set = %d, want 0 after unassign", wsID, got)
		}
	})

	t.Run("editor_is_denied", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, editorToken, http.MethodPut,
			fmt.Sprintf("/workspaces/%d/configuration-set", wsID), map[string]interface{}{"configuration_set_id": setA})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
		if got := assignedSetID(); got != 0 {
			t.Fatalf("denied assignment must not persist, workspace %d assigned set = %d", wsID, got)
		}
	})

	t.Run("admin_of_other_workspace_is_denied", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, wsAdminToken, http.MethodPut,
			fmt.Sprintf("/workspaces/%d/configuration-set", otherWSID), map[string]interface{}{"configuration_set_id": setA})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	t.Run("default_set_is_rejected", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, "/configuration-sets", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var body struct {
			ConfigurationSets []struct {
				ID        int  `json:"id"`
				IsDefault bool `json:"is_default"`
			} `json:"configuration_sets"`
		}
		DecodeJSON(t, resp, &body)
		defaultID := 0
		for _, cs := range body.ConfigurationSets {
			if cs.IsDefault {
				defaultID = cs.ID
			}
		}
		if defaultID == 0 {
			t.Fatal("no default configuration set found")
		}

		denyResp := MakeAuthRequestWithToken(t, server, wsAdminToken, http.MethodPut,
			fmt.Sprintf("/workspaces/%d/configuration-set", wsID), map[string]interface{}{"configuration_set_id": defaultID})
		defer denyResp.Body.Close()
		AssertStatusCode(t, denyResp, http.StatusBadRequest)
	})

	t.Run("system_admin_retains_access", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, server.SessionCookie, http.MethodPut,
			fmt.Sprintf("/workspaces/%d/configuration-set", wsID), map[string]interface{}{"configuration_set_id": setA})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		if got := assignedSetID(); got != setA {
			t.Fatalf("workspace %d assigned set = %d, want %d", wsID, got, setA)
		}
	})
}
