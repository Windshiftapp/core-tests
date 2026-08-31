package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// createTokenWithScopesAsUser logs in as the named user and mints a token with
// an explicit scope list — bypassing the default-scope helper so we can test
// what happens when the scope list is more permissive than the user's actual
// workspace role.
func createTokenWithScopesAsUser(t *testing.T, ts *TestServer, username, password string, scopes []string) string {
	t.Helper()

	loginResp := makeRequest(t, http.MethodPost, ts.APIBase+"/auth/login", "",
		map[string]string{"email_or_username": username, "password": password}, nil)
	if loginResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("Login failed for %s: %d - %s", username, loginResp.StatusCode, string(body))
	}
	var sessionCookie string
	for _, c := range loginResp.Cookies() {
		if c.Name == "session" || c.Name == "windshift_session" {
			sessionCookie = c.String()
			break
		}
	}
	loginResp.Body.Close()
	if sessionCookie == "" {
		t.Fatalf("No session cookie for %s", username)
	}

	tokenResp := makeRequest(t, http.MethodPost, ts.APIBase+"/api-tokens", "",
		map[string]interface{}{
			"name":        fmt.Sprintf("scope-test-%s", username),
			"permissions": scopes,
		},
		map[string]string{"Cookie": sessionCookie},
	)
	defer tokenResp.Body.Close()

	if tokenResp.StatusCode != http.StatusOK && tokenResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(tokenResp.Body)
		t.Fatalf("Token create failed for %s with scopes %v: %d - %s",
			username, scopes, tokenResp.StatusCode, string(body))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&out); err != nil {
		t.Fatalf("Decode token response: %v", err)
	}
	if out.Token == "" {
		t.Fatalf("Empty token in response")
	}
	return out.Token
}

// tryCreateTokenWithScopes returns the raw HTTP status from the create-token
// call so the caller can assert on rejection paths (e.g. non-admin minting an
// `admin:*` scope should be 403 at creation time).
func tryCreateTokenWithScopes(t *testing.T, ts *TestServer, username, password string, scopes []string) int {
	t.Helper()

	loginResp := makeRequest(t, http.MethodPost, ts.APIBase+"/auth/login", "",
		map[string]string{"email_or_username": username, "password": password}, nil)
	if loginResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("Login failed for %s: %d - %s", username, loginResp.StatusCode, string(body))
	}
	var sessionCookie string
	for _, c := range loginResp.Cookies() {
		if c.Name == "session" || c.Name == "windshift_session" {
			sessionCookie = c.String()
			break
		}
	}
	loginResp.Body.Close()

	tokenResp := makeRequest(t, http.MethodPost, ts.APIBase+"/api-tokens", "",
		map[string]interface{}{
			"name":        fmt.Sprintf("scope-attempt-%s", username),
			"permissions": scopes,
		},
		map[string]string{"Cookie": sessionCookie},
	)
	defer tokenResp.Body.Close()
	return tokenResp.StatusCode
}

// TestAPITokenScopes_DoNotEscalateBeyondUserRole exercises the privilege-
// escalation guarantee: a token cannot grant the bearer more access than the
// underlying user has. A Viewer who mints a token with items:write/delete
// scopes still gets 403 on every write/delete in workspaces where they hold
// only the Viewer role.
//
// All scope assertions run against /rest/api/v1/* (the only surface that
// accepts API bearer tokens after the cookie-auth bypass was closed).
func TestAPITokenScopes_DoNotEscalateBeyondUserRole(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Scope Escalation WS", shortKey("SEW"))
	LockDownWorkspace(t, server, workspaceID)

	viewerID, viewerUsername, viewerPassword := CreateTestUserWithCredentials(t, server, "scope_viewer", "scope_viewer@test.com")
	AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")

	// Mint a token whose scopes intentionally exceed the Viewer role.
	overScopedToken := createTokenWithScopesAsUser(t, server, viewerUsername, viewerPassword, []string{
		"items:read",
		"items:write",
		"items:delete",
		"workspaces:write",
	})

	// A real item to attempt edits on, created out-of-band by the admin.
	existingItemID := CreateTestItem(t, server, workspaceID, "Pre-existing item")

	// Item type for create attempts.
	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")

	t.Run("read_succeeds_when_role_allows", func(t *testing.T) {
		resp := MakeBearerRequestWithToken(t, server, overScopedToken, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/items?workspace_id=%d", workspaceID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	// Item handlers return 404 on permission failures by design (see
	// MEMORY.md security policy: never 403 for item workspace permission
	// failures, to prevent existence leakage). The escalation guarantee
	// is "request does not succeed" — we accept any rejection code.
	assertRejected := func(t *testing.T, resp *http.Response) {
		t.Helper()
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated ||
			resp.StatusCode == http.StatusNoContent {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected rejection, got %d with body: %s", resp.StatusCode, string(body))
		}
	}

	t.Run("create_blocked_despite_items_write_scope", func(t *testing.T) {
		body := map[string]interface{}{
			"title":        "Should not be created",
			"workspace_id": workspaceID,
			"item_type_id": itemTypeID,
		}
		resp := MakeBearerRequestWithToken(t, server, overScopedToken, http.MethodPost, "/rest/api/v1/items", body)
		defer resp.Body.Close()
		assertRejected(t, resp)
	})

	t.Run("update_blocked_despite_items_write_scope", func(t *testing.T) {
		body := map[string]interface{}{"title": "Should not be updated"}
		resp := MakeBearerRequestWithToken(t, server, overScopedToken, http.MethodPut,
			fmt.Sprintf("/rest/api/v1/items/%d", existingItemID), body)
		defer resp.Body.Close()
		assertRejected(t, resp)
	})

	t.Run("delete_blocked_despite_items_delete_scope", func(t *testing.T) {
		resp := MakeBearerRequestWithToken(t, server, overScopedToken, http.MethodDelete,
			fmt.Sprintf("/rest/api/v1/items/%d", existingItemID), nil)
		defer resp.Body.Close()
		assertRejected(t, resp)
	})

	t.Run("workspace_admin_blocked_despite_workspaces_write_scope", func(t *testing.T) {
		body := map[string]interface{}{
			"name":        "Renamed by Viewer",
			"key":         shortKey("SEW"),
			"description": "Should fail",
		}
		resp := MakeBearerRequestWithToken(t, server, overScopedToken, http.MethodPut,
			fmt.Sprintf("/rest/api/v1/workspaces/%d", workspaceID), body)
		defer resp.Body.Close()
		assertRejected(t, resp)
	})

	t.Run("existing_item_still_unchanged", func(t *testing.T) {
		// Positive control: the over-scoped token's write/update/delete
		// attempts above must not have mutated anything, so the item is
		// still readable with its original title.
		resp := MakeBearerRequestWithToken(t, server, overScopedToken, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/items/%d", existingItemID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var item map[string]interface{}
		DecodeJSON(t, resp, &item)
		if item["title"] != "Pre-existing item" {
			t.Fatalf("item title mutated: got %v", item["title"])
		}
	})

	t.Run("bearer_rejected_on_cookie_auth_surface", func(t *testing.T) {
		// Closes the regression: API tokens used to authenticate against
		// /api/* and silently bypass the v1 scope check. Verify the
		// cookie-auth surface now refuses bearer tokens explicitly.
		resp := MakeBearerRequestWithToken(t, server, overScopedToken, http.MethodGet,
			fmt.Sprintf("/api/items?workspace_id=%d", workspaceID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusUnauthorized)
	})
}

// TestAPITokenScopes_NonAdminCannotMintAdminScope asserts the creation-time
// guard in api_tokens.go that refuses any `admin:*` scope on a token unless
// the requester is a system admin.
func TestAPITokenScopes_NonAdminCannotMintAdminScope(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)

	_, username, password := CreateTestUserWithCredentials(t, server, "non_admin_user", "non_admin@test.com")

	cases := []struct {
		name  string
		scope string
	}{
		{"admin_users_read", "admin:users:read"},
		{"admin_users_write", "admin:users:write"},
		{"admin_groups_write", "admin:groups:write"},
		{"admin_audit_logs_read", "admin:audit-logs:read"},
		{"admin_api_tokens_write", "admin:api-tokens:write"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status := tryCreateTokenWithScopes(t, server, username, password,
				[]string{"items:read", c.scope})
			if status != http.StatusForbidden {
				t.Fatalf("non-admin mint of %s: expected 403, got %d", c.scope, status)
			}
		})
	}
}

// TestAPITokenScopes_NonAdminCannotMintAdminScopedToken verifies a non-admin
// cannot obtain a token carrying admin authority by any route.
//
// This previously exercised a loophole: the legacy `"admin"` scope (no colon)
// slipped past the creation-time IsAdminScope("admin:") prefix filter and was
// then inflated to every admin:* scope at validation time, so the system-admin
// middleware was the only thing keeping the user out. Legacy scopes are no
// longer accepted at mint or expanded at validation (WI-959), which closes that
// path at the first gate rather than the last.
func TestAPITokenScopes_NonAdminCannotMintAdminScopedToken(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)

	_, username, password := CreateTestUserWithCredentials(t, server, "legacy_admin_attempt", "legacy_admin@test.com")

	// The legacy string is now simply not a scope, so it fails validation
	// before the admin filter is even consulted.
	if status := tryCreateTokenWithScopes(t, server, username, password, []string{"admin"}); status != http.StatusBadRequest {
		t.Errorf("legacy \"admin\" scope should be rejected as invalid, got %d", status)
	}

	// And the granular admin scopes it used to expand to are refused to a
	// non-admin caller.
	for _, scope := range []string{"admin:api-tokens:read", "admin:audit-logs:read", "admin:users:write"} {
		if status := tryCreateTokenWithScopes(t, server, username, password, []string{scope}); status != http.StatusForbidden {
			t.Errorf("non-admin minting %s: expected 403, got %d", scope, status)
		}
	}
}
