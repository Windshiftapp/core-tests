package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// TestSCIM_Disconnect covers the admin-driven disconnect flow: provision a
// user + group via SCIM (so they carry scim_managed = true), then call the
// preview endpoint, then commit the disconnect. After the disconnect, the
// released users must be manageable through the normal admin surfaces that
// previously refused them.
func TestSCIM_Disconnect(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	scimToken := CreateSCIMToken(t, server, "Disconnect Test Token")

	// Provision one user via SCIM so there's something to release.
	createUser := map[string]interface{}{
		"schemas":  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"userName": "disconnect.user@example.com",
		"name":     map[string]string{"givenName": "Disco", "familyName": "Nect"},
		"emails": []map[string]interface{}{
			{"value": "disconnect.user@example.com", "primary": true},
		},
		"active": true,
	}
	userResp := MakeSCIMRequest(t, server, scimToken, http.MethodPost, "/scim/v2/Users", createUser)
	defer userResp.Body.Close()
	AssertStatusCode(t, userResp, http.StatusCreated)
	var userCreated map[string]interface{}
	DecodeJSON(t, userResp, &userCreated)
	scimUserID, _ := userCreated["id"].(string)
	if scimUserID == "" {
		t.Fatal("SCIM-provisioned user returned no id")
	}

	// Provision one group too, so the preview exercises the group path.
	createGroup := map[string]interface{}{
		"schemas":     []string{"urn:ietf:params:scim:schemas:core:2.0:Group"},
		"displayName": "Disconnect Group",
		"members": []map[string]interface{}{
			{"value": scimUserID, "type": "User"},
		},
	}
	groupResp := MakeSCIMRequest(t, server, scimToken, http.MethodPost, "/scim/v2/Groups", createGroup)
	defer groupResp.Body.Close()
	AssertStatusCode(t, groupResp, http.StatusCreated)

	t.Run("preview_shows_nonzero_counts", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, "/admin/scim/disconnect-preview", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var preview struct {
			ActiveTokens     int `json:"active_tokens"`
			Users            int `json:"users"`
			Groups           int `json:"groups"`
			GroupMemberships int `json:"group_memberships"`
		}
		DecodeJSON(t, resp, &preview)
		if preview.ActiveTokens < 1 {
			t.Fatalf("preview active_tokens = %d, want >= 1", preview.ActiveTokens)
		}
		if preview.Users < 1 {
			t.Fatalf("preview users = %d, want >= 1", preview.Users)
		}
		if preview.Groups < 1 {
			t.Fatalf("preview groups = %d, want >= 1", preview.Groups)
		}
	})

	// A validated, complete payload so we hit the SCIMManaged guard rather
	// than bouncing off input validation.
	updatePayload := map[string]interface{}{
		"email":      "disconnect.user@example.com",
		"username":   "disconnect.user@example.com",
		"first_name": "Disco",
		"last_name":  "Nect",
	}

	// Before disconnect, admin PUT on the SCIM user is blocked (scim_managed guard).
	t.Run("admin_edit_blocked_before_disconnect", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPatch,
			fmt.Sprintf("/v2/admin/users/%s", scimUserID), updatePayload)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 403 on admin PUT of SCIM-managed user, got %d: %s",
				resp.StatusCode, string(b))
		}
	})

	var summaryAfterDisconnect struct {
		ActiveTokens     int `json:"active_tokens"`
		Users            int `json:"users"`
		Groups           int `json:"groups"`
		GroupMemberships int `json:"group_memberships"`
	}

	t.Run("disconnect_returns_counts", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/admin/scim/disconnect", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		DecodeJSON(t, resp, &summaryAfterDisconnect)

		if summaryAfterDisconnect.ActiveTokens < 1 {
			t.Errorf("disconnect summary active_tokens = %d, want >= 1", summaryAfterDisconnect.ActiveTokens)
		}
		if summaryAfterDisconnect.Users < 1 {
			t.Errorf("disconnect summary users = %d, want >= 1", summaryAfterDisconnect.Users)
		}
		if summaryAfterDisconnect.Groups < 1 {
			t.Errorf("disconnect summary groups = %d, want >= 1", summaryAfterDisconnect.Groups)
		}
	})

	t.Run("preview_empty_after_disconnect", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, "/admin/scim/disconnect-preview", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var preview struct {
			ActiveTokens     int `json:"active_tokens"`
			Users            int `json:"users"`
			Groups           int `json:"groups"`
			GroupMemberships int `json:"group_memberships"`
		}
		DecodeJSON(t, resp, &preview)
		// All flags should be cleared; nothing left to release.
		if preview.ActiveTokens != 0 || preview.Users != 0 ||
			preview.Groups != 0 || preview.GroupMemberships != 0 {
			b, _ := json.Marshal(preview)
			t.Fatalf("preview after disconnect should be all zero, got %s", string(b))
		}
	})

	t.Run("scim_token_stops_working", func(t *testing.T) {
		// All SCIM tokens were revoked transactionally; further SCIM
		// calls must now fail auth rather than succeed quietly.
		resp := MakeSCIMRequest(t, server, scimToken, http.MethodGet, "/scim/v2/Users", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("SCIM call with revoked token: expected 401, got %d: %s",
				resp.StatusCode, string(b))
		}
	})

	t.Run("admin_edit_allowed_after_disconnect", func(t *testing.T) {
		// Released user: the scim_managed guard no longer fires, so the
		// admin API can edit them again.
		resp := MakeAuthRequest(t, server, http.MethodPatch,
			fmt.Sprintf("/v2/admin/users/%s", scimUserID), updatePayload)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected admin PUT on released user to succeed, got %d: %s",
				resp.StatusCode, string(b))
		}
	})
}
