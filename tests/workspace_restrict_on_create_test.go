package tests

import (
	"net/http"
	"testing"
)

// TestWorkspaceRestrictOnCreate exercises the restricted_to_creator creation
// flag: the creator's Viewer grant lands inside the creation transaction, so
// the workspace is gated from the first committed moment — it never lists for
// unassigned users, unlike a plain create which is open by default.
func TestWorkspaceRestrictOnCreate(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	readerID, readerName, readerPass := CreateTestUserWithCredentials(t, server, "rocreader", "rocreader@test.local")
	_, readerToken := CreateAuthCredentialsForUser(t, server, readerName, readerPass)

	directoryContains := func(token, key string) bool {
		t.Helper()
		resp := MakeBearerRequestWithToken(t, server, token, http.MethodGet, "/rest/api/v2/workspaces", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var page struct {
			Data []map[string]any `json:"data"`
		}
		DecodeJSON(t, resp, &page)
		for _, ws := range page.Data {
			if ws["key"] == key {
				return true
			}
		}
		return false
	}

	createWorkspace := func(name, key string, restricted bool) (int, map[string]any) {
		t.Helper()
		body := map[string]any{"name": name, "key": key}
		if restricted {
			body["restricted_to_creator"] = true
		}
		resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/workspaces", body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var workspace map[string]any
		DecodeJSON(t, resp, &workspace)
		return ExtractIDFromResponse(t, workspace), workspace
	}

	// Both workspaces exist before the reader's first directory read, so the
	// reader's permission snapshot is built once against final state and the
	// assertions carry no cache-timing sensitivity.
	restrictedKey := shortKey("RST")
	restrictedID, restrictedCreated := createWorkspace("Restricted at birth", restrictedKey, true)
	openKey := shortKey("OPN")
	createWorkspace("Open by default", openKey, false)

	t.Run("create_response_marks_workspace_restricted", func(t *testing.T) {
		AssertJSONField(t, restrictedCreated, "is_restricted", true)
	})

	t.Run("unassigned_user_never_sees_restricted_workspace", func(t *testing.T) {
		if directoryContains(readerToken, restrictedKey) {
			t.Fatalf("restricted workspace %s listed for unassigned user", restrictedKey)
		}
	})

	t.Run("plain_create_stays_open_to_everyone", func(t *testing.T) {
		if !directoryContains(readerToken, openKey) {
			t.Fatalf("open workspace %s missing from unassigned user directory", openKey)
		}
	})

	t.Run("viewer_assignment_makes_restricted_workspace_visible", func(t *testing.T) {
		AssignWorkspaceRole(t, server, readerID, restrictedID, "Viewer")
		if !directoryContains(readerToken, restrictedKey) {
			t.Fatalf("restricted workspace %s still hidden after Viewer assignment", restrictedKey)
		}
	})
}
