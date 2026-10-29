package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// TestSCIM_CannotDeactivateNonSCIMUser locks in the guard in scim.go that
// refuses SCIM DELETE/PUT/PATCH on users the IdP did not provision. Without
// it, a SCIM credential could deactivate any user by internal ID — admins,
// local accounts, or any user not tracked by the IdP.
func TestSCIM_CannotDeactivateNonSCIMUser(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	scimToken := CreateSCIMToken(t, server, "Non-SCIM Guard Test")

	// Create a locally-managed user via the admin API. No SCIM provisioning
	// ever touches this user, so scim_managed stays false.
	localUserID, _, _ := CreateTestUserWithCredentials(t, server, "local_guard_user", "local_guard@test.com")

	assertStillActive := func(t *testing.T) {
		t.Helper()
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/users/%d", localUserID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var user map[string]interface{}
		DecodeJSON(t, resp, &user)
		active, ok := user["is_active"].(bool)
		if !ok || !active {
			body, _ := json.Marshal(user)
			t.Fatalf("local user was unexpectedly deactivated: %s", string(body))
		}
	}

	t.Run("delete_refused", func(t *testing.T) {
		resp := MakeSCIMRequest(t, server, scimToken, http.MethodDelete,
			fmt.Sprintf("/scim/v2/Users/%d", localUserID), nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 404 on SCIM DELETE of non-SCIM user, got %d: %s",
				resp.StatusCode, string(body))
		}
		assertStillActive(t)
	})

	t.Run("patch_active_false_refused", func(t *testing.T) {
		patch := map[string]interface{}{
			"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
			"Operations": []map[string]interface{}{
				{"op": "replace", "path": "active", "value": false},
			},
		}
		resp := MakeSCIMRequest(t, server, scimToken, http.MethodPatch,
			fmt.Sprintf("/scim/v2/Users/%d", localUserID), patch)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 404 on SCIM PATCH of non-SCIM user, got %d: %s",
				resp.StatusCode, string(body))
		}
		assertStillActive(t)
	})

	t.Run("put_active_false_refused", func(t *testing.T) {
		put := map[string]interface{}{
			"schemas":  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
			"userName": "local_guard_user",
			"name":     map[string]string{"givenName": "Local", "familyName": "Guard"},
			"active":   false,
		}
		resp := MakeSCIMRequest(t, server, scimToken, http.MethodPut,
			fmt.Sprintf("/scim/v2/Users/%d", localUserID), put)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 404 on SCIM PUT of non-SCIM user, got %d: %s",
				resp.StatusCode, string(body))
		}
		assertStillActive(t)
	})
}
