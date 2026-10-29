//go:build test

package handlers

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/sso"
	"windshift/internal/testutils"
)

// These tests pin the OAuth callback redirect contract. Success and error
// share one outcome-redirect builder and, for a validated state row, the same
// landing page: workspace flows return to the workspace source-control
// settings with the numeric workspace ID (the SPA shell and workspace
// settings resolve only numeric IDs; the v2 API rejects workspace keys), and
// user-level flows return to the profile's connected accounts. Only failures
// that cannot be attributed to a flow (missing or expired state) fall back to
// the admin provider list, which must carry its tab in the path because the
// admin page force-navigates bare /admin to a default tab and drops query
// strings.

// gitlabCallbackFakeWithTokenStatus fakes the two GitLab endpoints the
// callback touches: POST /oauth/token (with a configurable status so callers
// can force an exchange failure) and GET /api/v4/user.
func gitlabCallbackFakeWithTokenStatus(t *testing.T, tokenStatus int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		if tokenStatus != http.StatusOK {
			w.WriteHeader(tokenStatus)
			_, _ = w.Write([]byte(`{"error":"server_error"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access-token","refresh_token":"new-refresh-token","expires_in":7200}`))
	})
	mux.HandleFunc("GET /api/v4/user", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":77,"username":"root","name":"Root","public_email":"root@example.com"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func gitlabCallbackFake(t *testing.T) *httptest.Server {
	t.Helper()
	return gitlabCallbackFakeWithTokenStatus(t, http.StatusOK)
}

// newOAuthCallbackFixture seeds an enabled GitLab OAuth provider whose
// base_url points at the fake GitLab endpoints, plus a workspace, a user, a
// workspace connection, and a fresh scm_oauth_state row. bindWorkspace
// stores the workspace ID on the state row (workspace flow); otherwise it
// stays NULL (user-level flow).
func newOAuthCallbackFixture(t *testing.T, bindWorkspace bool, state string, stateExpiresAt time.Time) (*SCMProviderHandler, database.Database, int, int) {
	t.Helper()
	return newOAuthCallbackFixtureWithTokenStatus(t, bindWorkspace, state, stateExpiresAt, http.StatusOK)
}

func newOAuthCallbackFixtureWithTokenStatus(t *testing.T, bindWorkspace bool, state string, stateExpiresAt time.Time, tokenStatus int) (*SCMProviderHandler, database.Database, int, int) { //nolint:gocritic // multi-value fixture
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	fake := gitlabCallbackFakeWithTokenStatus(t, tokenStatus)

	const sessionSecret = "test-session-secret"
	enc := sso.NewSecretEncryption(sessionSecret)
	clientSecretEnc, err := enc.Encrypt("client-secret")
	if err != nil {
		t.Fatalf("encrypt client secret: %v", err)
	}

	uid := seedTestUser(t, db, "callbacker")
	wsID := seedWorkspace(t, db, "WI")

	var providerID int
	err = db.QueryRow(`
		INSERT INTO scm_providers (slug, name, provider_type, auth_method, enabled, base_url, oauth_client_id, oauth_client_secret_encrypted)
		VALUES ('gitlab', 'GitLab', 'gitlab', 'oauth', true, ?, 'client-id', ?)
		RETURNING id
	`, fake.URL, clientSecretEnc).Scan(&providerID)
	if err != nil {
		t.Fatalf("seed provider: %v", err)
	}

	if bindWorkspace {
		if _, err := db.Exec(`
			INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id, enabled)
			VALUES (?, ?, true)
		`, wsID, providerID); err != nil {
			t.Fatalf("seed connection: %v", err)
		}
	}

	var wsIDArg any
	if bindWorkspace {
		wsIDArg = wsID
	}
	if _, err := db.Exec(`
		INSERT INTO scm_oauth_state (provider_id, state, redirect_uri, user_id, workspace_id, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, providerID, state, "http://test.local/api/scm/oauth/gitlab/callback", uid, wsIDArg, stateExpiresAt); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	h := NewSCMProviderHandler(db, sessionSecret, "http://test.local")
	return h, db, wsID, uid
}

// oauthCallbackRequest authenticates as the account that started the flow, as
// the browser completing an OAuth redirect would.
func oauthCallbackRequest(code, state string, uid int) *http.Request {
	req := httptest.NewRequest("GET", "/api/scm/oauth/gitlab/callback?code="+code+"&state="+state, nil)
	return testutils.WithAuthContext(req, &models.User{ID: uid, Username: "callbacker"})
}

func TestOAuthCallback_WorkspaceFlow_RedirectsToNumericWorkspaceSettings(t *testing.T) {
	h, db, wsID, uid := newOAuthCallbackFixture(t, true, "state-abc", time.Now().Add(5*time.Minute))

	rec := httptest.NewRecorder()
	h.OAuthCallback(rec, oauthCallbackRequest("auth-code", "state-abc", uid))

	if rec.Code != http.StatusFound {
		t.Fatalf("status: got %d want 302 — body: %s", rec.Code, rec.Body.String())
	}
	want := "/workspaces/" + strconv.Itoa(wsID) + "/settings/source-control?oauth=success&provider=gitlab"
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("Location: got %q want %q", got, want)
	}

	// The workspace connection must end up holding the exchanged token.
	var tokenEnc *string
	if err := db.QueryRow(`
		SELECT oauth_access_token_encrypted FROM workspace_scm_connections
		WHERE workspace_id = ? AND scm_provider_id = (SELECT id FROM scm_providers WHERE slug = 'gitlab')
	`, wsID).Scan(&tokenEnc); err != nil {
		t.Fatalf("query connection token: %v", err)
	}
	if tokenEnc == nil {
		t.Fatal("workspace connection token not stored by callback")
	}
}

func TestOAuthCallback_UserLevelFlow_RedirectsToConnectedAccounts(t *testing.T) {
	h, _, _, uid := newOAuthCallbackFixture(t, false, "state-abc", time.Now().Add(5*time.Minute))

	rec := httptest.NewRecorder()
	h.OAuthCallback(rec, oauthCallbackRequest("auth-code", "state-abc", uid))

	if rec.Code != http.StatusFound {
		t.Fatalf("status: got %d want 302 — body: %s", rec.Code, rec.Body.String())
	}
	want := "/profile?tab=connected-accounts&oauth=success&provider=gitlab"
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("Location: got %q want %q", got, want)
	}
}

func TestOAuthCallback_ExpiredState_RedirectsToAdminScmProvidersPath(t *testing.T) {
	h, _, _, uid := newOAuthCallbackFixture(t, false, "state-abc", time.Now().Add(-time.Minute))

	rec := httptest.NewRecorder()
	h.OAuthCallback(rec, oauthCallbackRequest("auth-code", "state-abc", uid))

	if rec.Code != http.StatusFound {
		t.Fatalf("status: got %d want 302 — body: %s", rec.Code, rec.Body.String())
	}
	want := "/admin/scm-providers?message=" + url.QueryEscape("Invalid or expired state") + "&oauth=error"
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("Location: got %q want %q", got, want)
	}
}

func TestOAuthCallback_WorkspaceFlow_ExchangeFailure_RedirectsToWorkspaceLanding(t *testing.T) {
	h, db, wsID, uid := newOAuthCallbackFixtureWithTokenStatus(t, true, "state-abc", time.Now().Add(5*time.Minute), http.StatusInternalServerError)

	rec := httptest.NewRecorder()
	h.OAuthCallback(rec, oauthCallbackRequest("auth-code", "state-abc", uid))

	if rec.Code != http.StatusFound {
		t.Fatalf("status: got %d want 302 — body: %s", rec.Code, rec.Body.String())
	}
	want := "/workspaces/" + strconv.Itoa(wsID) + "/settings/source-control?message=" + url.QueryEscape("Failed to exchange token") + "&oauth=error"
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("Location: got %q want %q", got, want)
	}

	// A failed exchange must not store anything on the connection.
	var tokenEnc *string
	if err := db.QueryRow(`
		SELECT oauth_access_token_encrypted FROM workspace_scm_connections
		WHERE workspace_id = ? AND scm_provider_id = (SELECT id FROM scm_providers WHERE slug = 'gitlab')
	`, wsID).Scan(&tokenEnc); err != nil {
		t.Fatalf("query connection token: %v", err)
	}
	if tokenEnc != nil {
		t.Fatal("failed exchange stored a workspace connection token")
	}
}

func TestOAuthCallback_UserLevelFlow_ExchangeFailure_RedirectsToProfileLanding(t *testing.T) {
	h, _, _, uid := newOAuthCallbackFixtureWithTokenStatus(t, false, "state-abc", time.Now().Add(5*time.Minute), http.StatusInternalServerError)

	rec := httptest.NewRecorder()
	h.OAuthCallback(rec, oauthCallbackRequest("auth-code", "state-abc", uid))

	if rec.Code != http.StatusFound {
		t.Fatalf("status: got %d want 302 — body: %s", rec.Code, rec.Body.String())
	}
	want := "/profile?tab=connected-accounts&message=" + url.QueryEscape("Failed to exchange token") + "&oauth=error"
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("Location: got %q want %q", got, want)
	}
}

// Success and error must land on the same page for a given flow, so the
// origin UI can acknowledge both outcomes. A workspace flow whose exchange
// fails must not be bounced to the admin provider list.
func TestOAuthCallback_ErrorSharesSuccessLanding(t *testing.T) {
	successRec := httptest.NewRecorder()
	successH, _, wsID, successUID := newOAuthCallbackFixture(t, true, "state-ok", time.Now().Add(5*time.Minute))
	successH.OAuthCallback(successRec, oauthCallbackRequest("auth-code", "state-ok", successUID))
	successLanding := strings.SplitN(successRec.Header().Get("Location"), "?", 2)[0]

	errorRec := httptest.NewRecorder()
	errorH, _, errorWSID, errorUID := newOAuthCallbackFixtureWithTokenStatus(t, true, "state-bad", time.Now().Add(5*time.Minute), http.StatusInternalServerError)
	errorH.OAuthCallback(errorRec, oauthCallbackRequest("auth-code", "state-bad", errorUID))
	errorLanding := strings.SplitN(errorRec.Header().Get("Location"), "?", 2)[0]

	if wsID != errorWSID {
		t.Fatalf("fixtures produced different workspaces: %d and %d", wsID, errorWSID)
	}
	wantLanding := "/workspaces/" + strconv.Itoa(wsID) + "/settings/source-control"
	if successLanding != wantLanding || errorLanding != wantLanding {
		t.Fatalf("landings: success %q error %q, want both %q", successLanding, errorLanding, wantLanding)
	}
}

func TestRedirectOAuthOutcome_SharesBuilderForSuccessAndError(t *testing.T) {
	build := func(landing oauthLanding, outcome, providerSlug, message string) string {
		rec := httptest.NewRecorder()
		redirectOAuthOutcome(rec, httptest.NewRequest(http.MethodGet, "/api/scm/oauth/gitlab/callback", nil), landing, outcome, providerSlug, message)
		if rec.Code != http.StatusFound {
			t.Fatalf("status: got %d want 302", rec.Code)
		}
		return rec.Header().Get("Location")
	}

	if got := build(oauthLandingFromState(sql.NullInt64{Int64: 9, Valid: true}), "success", "gitlab", ""); got != "/workspaces/9/settings/source-control?oauth=success&provider=gitlab" {
		t.Fatalf("workspace success: got %q", got)
	}
	if got := build(oauthLandingFromState(sql.NullInt64{}), "success", "gitlab", ""); got != "/profile?tab=connected-accounts&oauth=success&provider=gitlab" {
		t.Fatalf("profile success: got %q", got)
	}
	if got := build(oauthLandingProfile, "error", "gitlab", "token expired"); got != "/profile?tab=connected-accounts&message=token+expired&oauth=error&provider=gitlab" {
		t.Fatalf("profile error: got %q", got)
	}
	if got := build(oauthLandingAdmin, "error", "", "Invalid or expired state"); got != "/admin/scm-providers?message=Invalid+or+expired+state&oauth=error" {
		t.Fatalf("admin fallback: got %q", got)
	}
}
