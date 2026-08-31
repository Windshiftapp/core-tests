package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/contextkeys"
	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

// These tests cover the StartOAuth lookup tightening from docs/bughunt5.md
// Run 9 #1. Invariant being defended: a personal SCM OAuth flow can only be
// initiated against a provider that is (a) enabled, (b) actually uses OAuth,
// and (c) — when restricted — has at least one of the user's workspaces on
// its allowlist. Every other case must return 404 to avoid leaking slugs.

// newSCMProviderTestHandler wires an SCMProviderHandler to a fresh database
// initialized from the embedded production schema. The sessionSecret is fed a
// placeholder value because the OAuth start path does not decrypt credentials.
func newSCMProviderTestHandler(t *testing.T) (*SCMProviderHandler, database.Database) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	h := NewSCMProviderHandler(db, "test-session-secret", "http://test.local")
	return h, db
}

// seedOAuthProvider inserts an scm_providers row and returns the new ID.
// The provider is created with auth_method='oauth' and a non-empty
// oauth_client_id so we exercise the post-auth allowlist path; callers pass
// `enabled` and `restrictionMode` ('unrestricted' or 'restricted') to control
// which gate is being tested.
func seedOAuthProvider(t *testing.T, db database.Database, slug string, enabled bool, restrictionMode string) int {
	t.Helper()
	var id int
	err := db.QueryRow(`
		INSERT INTO scm_providers
		    (slug, name, provider_type, auth_method, enabled, oauth_client_id, workspace_restriction_mode)
		VALUES (?, ?, 'github', 'oauth', ?, 'client-abc', ?)
		RETURNING id
	`, slug, slug, enabled, restrictionMode).Scan(&id)
	if err != nil {
		t.Fatalf("seed provider %s: %v", slug, err)
	}
	return id
}

func seedPATProvider(t *testing.T, db database.Database, slug string) int {
	t.Helper()
	var id int
	err := db.QueryRow(`
		INSERT INTO scm_providers
		    (slug, name, provider_type, auth_method, enabled, workspace_restriction_mode)
		VALUES (?, ?, 'github', 'pat', true, 'unrestricted')
		RETURNING id
	`, slug, slug).Scan(&id)
	if err != nil {
		t.Fatalf("seed pat provider %s: %v", slug, err)
	}
	return id
}

func seedTestUser(t *testing.T, db database.Database, username string) int {
	t.Helper()
	var id int
	err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES (?, ?, 'OAuth', 'Tester')
		RETURNING id
	`, username+"@example.com", username).Scan(&id)
	if err != nil {
		t.Fatalf("seed user %s: %v", username, err)
	}
	return id
}

func seedWorkspace(t *testing.T, db database.Database, key string) int {
	t.Helper()
	var id int
	err := db.QueryRow(`INSERT INTO workspaces (key, name) VALUES (?, ?) RETURNING id`, key, key).Scan(&id)
	if err != nil {
		t.Fatalf("seed workspace %s: %v", key, err)
	}
	return id
}

func seedAllowlistEntry(t *testing.T, db database.Database, providerID, workspaceID int) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO scm_provider_workspace_allowlist (provider_id, workspace_id) VALUES (?, ?)
	`, providerID, workspaceID); err != nil {
		t.Fatalf("seed allowlist: %v", err)
	}
}

func seedWorkspaceRole(t *testing.T, db database.Database, name string) int {
	t.Helper()
	var id int
	err := db.QueryRow(`INSERT INTO workspace_roles (name) VALUES (?) RETURNING id`, name).Scan(&id)
	if err != nil {
		t.Fatalf("seed role: %v", err)
	}
	return id
}

func seedUserWorkspaceRole(t *testing.T, db database.Database, userID, workspaceID, roleID int) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id) VALUES (?, ?, ?)
	`, userID, workspaceID, roleID); err != nil {
		t.Fatalf("seed user_workspace_role: %v", err)
	}
}

// scmStartReq builds a request with the supplied user in context and the slug
// bound as a path value (as Go's ServeMux would).
func scmStartReq(t *testing.T, userID int, slug string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/scm/oauth/"+slug+"/start", nil)
	req.SetPathValue("slug", slug)
	ctx := context.WithValue(req.Context(), contextkeys.User, &models.User{ID: userID})
	return req.WithContext(ctx)
}

func countOAuthStates(t *testing.T, db database.Database, providerID int) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scm_oauth_state WHERE provider_id = ?`, providerID).Scan(&n); err != nil {
		t.Fatalf("count oauth states: %v", err)
	}
	return n
}

func TestStartOAuth_DisabledProvider_Returns404(t *testing.T) {
	h, db := newSCMProviderTestHandler(t)
	uid := seedTestUser(t, db, "alice")
	pid := seedOAuthProvider(t, db, "ghdisabled", false, "unrestricted")

	rec := httptest.NewRecorder()
	h.StartOAuth(rec, scmStartReq(t, uid, "ghdisabled"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404 — disabled provider leaked OAuth start", rec.Code)
	}
	if n := countOAuthStates(t, db, pid); n != 0 {
		t.Errorf("scm_oauth_state rows: got %d want 0 — state should not be inserted for disabled provider", n)
	}
}

func TestStartOAuth_NonOAuthProvider_Returns404(t *testing.T) {
	h, db := newSCMProviderTestHandler(t)
	uid := seedTestUser(t, db, "bob")
	pid := seedPATProvider(t, db, "ghpat")

	rec := httptest.NewRecorder()
	h.StartOAuth(rec, scmStartReq(t, uid, "ghpat"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404 — PAT provider should not accept OAuth start", rec.Code)
	}
	if n := countOAuthStates(t, db, pid); n != 0 {
		t.Errorf("scm_oauth_state rows: got %d want 0", n)
	}
}

func TestStartOAuth_RestrictedProvider_UserInAllowlistedWorkspace_Succeeds(t *testing.T) {
	h, db := newSCMProviderTestHandler(t)
	uid := seedTestUser(t, db, "carol")
	wsID := seedWorkspace(t, db, "ws-allowed")
	roleID := seedWorkspaceRole(t, db, "member")
	seedUserWorkspaceRole(t, db, uid, wsID, roleID)

	pid := seedOAuthProvider(t, db, "ghrestricted", true, "restricted")
	seedAllowlistEntry(t, db, pid, wsID)

	rec := httptest.NewRecorder()
	h.StartOAuth(rec, scmStartReq(t, uid, "ghrestricted"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 — body: %s", rec.Code, rec.Body.String())
	}
	if n := countOAuthStates(t, db, pid); n != 1 {
		t.Errorf("scm_oauth_state rows: got %d want 1 — state should be inserted on success", n)
	}
}

func TestStartOAuth_RestrictedProvider_UserOutsideAllowlist_Returns404(t *testing.T) {
	h, db := newSCMProviderTestHandler(t)
	uid := seedTestUser(t, db, "dave")
	// Seed a workspace the user IS in, but DON'T add it to the allowlist.
	wsOther := seedWorkspace(t, db, "ws-other")
	roleID := seedWorkspaceRole(t, db, "member")
	seedUserWorkspaceRole(t, db, uid, wsOther, roleID)

	// Allowlist a different workspace that the user isn't a member of.
	wsAllowed := seedWorkspace(t, db, "ws-allowed")
	pid := seedOAuthProvider(t, db, "ghscoped", true, "restricted")
	seedAllowlistEntry(t, db, pid, wsAllowed)

	rec := httptest.NewRecorder()
	h.StartOAuth(rec, scmStartReq(t, uid, "ghscoped"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404 — restricted provider leaked to non-allowlisted user", rec.Code)
	}
	if n := countOAuthStates(t, db, pid); n != 0 {
		t.Errorf("scm_oauth_state rows: got %d want 0 — no state should be inserted on denial", n)
	}
}

func TestStartOAuth_UnrestrictedProvider_AnyUserSucceeds(t *testing.T) {
	// Baseline: an enabled, unrestricted OAuth provider must still work for
	// authenticated users with no workspace memberships. This guards against
	// the allowlist gate over-firing on the default 'unrestricted' mode.
	h, db := newSCMProviderTestHandler(t)
	uid := seedTestUser(t, db, "erin")
	pid := seedOAuthProvider(t, db, "ghopen", true, "unrestricted")

	rec := httptest.NewRecorder()
	h.StartOAuth(rec, scmStartReq(t, uid, "ghopen"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 — body: %s", rec.Code, rec.Body.String())
	}
	if n := countOAuthStates(t, db, pid); n != 1 {
		t.Errorf("scm_oauth_state rows: got %d want 1", n)
	}
}
