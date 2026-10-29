package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestWorkspaceRestrictedVisibilityFlag exercises the is_restricted marker on
// the v2 workspace directory: assigning the built-in Viewer role flips a
// workspace into restricted visibility, and revoking the last Viewer
// assignment reopens it. The flag rides the directory list only.
func TestWorkspaceRestrictedVisibilityFlag(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	created := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/workspaces", map[string]any{
		"name": "Restricted flag", "key": shortKey("RFL"),
	})
	defer created.Body.Close()
	AssertStatusCode(t, created, http.StatusCreated)
	var workspace map[string]any
	DecodeJSON(t, created, &workspace)
	workspaceID := ExtractIDFromResponse(t, workspace)

	findInDirectory := func() map[string]any {
		t.Helper()
		resp := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/workspaces", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var page struct {
			Data []map[string]any `json:"data"`
		}
		DecodeJSON(t, resp, &page)
		for _, ws := range page.Data {
			if int(ws["id"].(float64)) == workspaceID {
				return ws
			}
		}
		t.Fatalf("workspace %d missing from directory: %#v", workspaceID, page.Data)
		return nil
	}

	t.Run("fresh_workspace_is_not_restricted", func(t *testing.T) {
		open := findInDirectory()
		if _, present := open["is_restricted"]; present {
			t.Fatalf("fresh workspace should not be restricted: %#v", open)
		}
	})

	t.Run("viewer_assignment_restricts_and_revoke_reopens", func(t *testing.T) {
		assigneeID, _, _ := CreateTestUserWithCredentials(t, server, "rflviewer", "rflviewer@test.local")
		AssignWorkspaceRole(t, server, assigneeID, workspaceID, "Viewer")

		restricted := findInDirectory()
		AssertJSONField(t, restricted, "is_restricted", true)

		// The single GET keeps directory-only computed fields out of its
		// contract, matching time_project_name.
		single := MakeBearerRequest(t, server, http.MethodGet,
			fmt.Sprintf("/rest/api/v2/workspaces/%d", workspaceID), nil)
		defer single.Body.Close()
		AssertStatusCode(t, single, http.StatusOK)
		var singleBody map[string]any
		DecodeJSON(t, single, &singleBody)
		if _, present := singleBody["is_restricted"]; present {
			t.Fatalf("single GET should not carry the directory-only flag: %#v", singleBody)
		}

		roles := GetWorkspaceRoles(t, server)
		RevokeWorkspaceRole(t, server, assigneeID, workspaceID, roles["Viewer"])

		reopened := findInDirectory()
		if _, present := reopened["is_restricted"]; present {
			t.Fatalf("workspace should be unrestricted after the last Viewer assignment is revoked: %#v", reopened)
		}
	})
}
