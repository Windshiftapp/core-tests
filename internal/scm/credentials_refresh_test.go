//go:build test

package scm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/sso"
	"windshift/internal/testutils"
)

// These tests pin the WI-274 invariant: credential resolution itself
// refreshes expiring workspace OAuth tokens, so consumers that resolve
// by connection ID (the run broker's git proxy, agent PR creation,
// repo/issue sync) all share one refresh path instead of each deciding
// whether to bother.

// newCredentialsRefreshFixture seeds a Gitea OAuth workspace connection
// whose token endpoint is the given httptest server. expiresAt controls
// the stored token expiry; nil stores a non-expiring token.
func newCredentialsRefreshFixture(t *testing.T, baseURL string, expiresAt *time.Time) (*CredentialResolver, *testutils.TestDB) {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}

	enc := sso.NewSecretEncryption("credentials-refresh-test-secret-with-enough-length")
	accessEnc, err := enc.Encrypt("old-access-token")
	if err != nil {
		t.Fatalf("encrypt access token: %v", err)
	}
	refreshEnc, err := enc.Encrypt("old-refresh-token")
	if err != nil {
		t.Fatalf("encrypt refresh token: %v", err)
	}
	clientSecretEnc, err := enc.Encrypt("client-secret")
	if err != nil {
		t.Fatalf("encrypt client secret: %v", err)
	}

	stmts := []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO workspaces(id, name, key) VALUES (?, ?, ?)`,
			args:  []any{1, "Windshift", "WI"},
		},
		{
			query: `INSERT INTO scm_providers(id, slug, name, provider_type, auth_method, enabled, base_url, oauth_client_id, oauth_client_secret_encrypted)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			args: []any{1, "creds-refresh-test", "Gitea", string(models.SCMProviderTypeGitea), string(models.SCMAuthMethodOAuth), true, baseURL, "client-id", clientSecretEnc},
		},
		{
			query: `INSERT INTO workspace_scm_connections(id, workspace_id, scm_provider_id, enabled,
				oauth_access_token_encrypted, oauth_refresh_token_encrypted, oauth_token_expires_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
			args: []any{7, 1, 1, true, accessEnc, refreshEnc, expiresAt},
		},
	}
	for _, s := range stmts {
		if _, err := tdb.Exec(s.query, s.args...); err != nil {
			t.Fatalf("seed %q: %v", s.query, err)
		}
	}

	return NewCredentialResolver(tdb, enc), tdb
}

// giteaTokenEndpoint fakes <base>/login/oauth/access_token and counts hits.
func giteaTokenEndpoint(t *testing.T, hits *atomic.Int32, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/oauth/access_token" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGetCredentialsByConnectionID_RefreshesExpiringWorkspaceOAuthToken(t *testing.T) {
	var hits atomic.Int32
	srv := giteaTokenEndpoint(t, &hits, http.StatusOK,
		`{"access_token":"new-access-token","token_type":"bearer","refresh_token":"new-refresh-token","expires_in":3600}`)

	soon := time.Now().Add(2 * time.Minute) // inside the 5-minute refresh window
	resolver, tdb := newCredentialsRefreshFixture(t, srv.URL, &soon)

	creds, err := resolver.GetCredentialsByConnectionID(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetCredentialsByConnectionID: %v", err)
	}
	if creds.OAuthAccessToken != "new-access-token" {
		t.Errorf("expected resolved access token to be refreshed, got %q", creds.OAuthAccessToken)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("expected exactly one refresh call, got %d", got)
	}

	// The rotated tokens must be persisted so the next resolution doesn't
	// burn another refresh.
	var storedAccessEnc string
	if err := tdb.QueryRow(`SELECT oauth_access_token_encrypted FROM workspace_scm_connections WHERE id = 7`).Scan(&storedAccessEnc); err != nil {
		t.Fatalf("read stored token: %v", err)
	}
	enc := sso.NewSecretEncryption("credentials-refresh-test-secret-with-enough-length")
	stored, err := enc.Decrypt(storedAccessEnc)
	if err != nil {
		t.Fatalf("decrypt stored token: %v", err)
	}
	if stored != "new-access-token" {
		t.Errorf("expected refreshed token persisted, got %q", stored)
	}
}

func TestGetCredentialsByConnectionID_NonExpiringTokenSkipsRefresh(t *testing.T) {
	var hits atomic.Int32
	srv := giteaTokenEndpoint(t, &hits, http.StatusOK, `{}`)

	resolver, _ := newCredentialsRefreshFixture(t, srv.URL, nil)

	creds, err := resolver.GetCredentialsByConnectionID(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetCredentialsByConnectionID: %v", err)
	}
	if creds.OAuthAccessToken != "old-access-token" {
		t.Errorf("expected stored token untouched, got %q", creds.OAuthAccessToken)
	}
	if got := hits.Load(); got != 0 {
		t.Errorf("expected no refresh call for non-expiring token, got %d", got)
	}
}

func TestGetCredentialsByConnectionID_TokenNotYetExpiringSkipsRefresh(t *testing.T) {
	var hits atomic.Int32
	srv := giteaTokenEndpoint(t, &hits, http.StatusOK, `{}`)

	later := time.Now().Add(2 * time.Hour) // outside the 5-minute refresh window
	resolver, _ := newCredentialsRefreshFixture(t, srv.URL, &later)

	creds, err := resolver.GetCredentialsByConnectionID(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetCredentialsByConnectionID: %v", err)
	}
	if creds.OAuthAccessToken != "old-access-token" {
		t.Errorf("expected stored token untouched, got %q", creds.OAuthAccessToken)
	}
	if got := hits.Load(); got != 0 {
		t.Errorf("expected no refresh call for fresh token, got %d", got)
	}
}

func TestGetCredentialsByConnectionID_DeadRefreshTokenFailsResolution(t *testing.T) {
	var hits atomic.Int32
	srv := giteaTokenEndpoint(t, &hits, http.StatusBadRequest, `{"error":"invalid_grant"}`)

	soon := time.Now().Add(2 * time.Minute)
	resolver, tdb := newCredentialsRefreshFixture(t, srv.URL, &soon)

	_, err := resolver.GetCredentialsByConnectionID(context.Background(), 7)
	if !errors.Is(err, ErrRefreshTokenInvalid) {
		t.Fatalf("expected ErrRefreshTokenInvalid in chain, got %v", err)
	}

	// Dead refresh token must wipe the stored workspace credential so the
	// connection reads as disconnected instead of wedging on a dead token.
	var accessEnc, refreshEnc any
	if err := tdb.QueryRow(`SELECT oauth_access_token_encrypted, oauth_refresh_token_encrypted FROM workspace_scm_connections WHERE id = 7`).Scan(&accessEnc, &refreshEnc); err != nil {
		t.Fatalf("read stored tokens: %v", err)
	}
	if accessEnc != nil || refreshEnc != nil {
		t.Errorf("expected stored OAuth tokens wiped after invalid_grant, got access=%v refresh=%v", accessEnc, refreshEnc)
	}
}

func TestGetCredentialsByConnectionID_TransientRefreshFailureKeepsExistingToken(t *testing.T) {
	var hits atomic.Int32
	srv := giteaTokenEndpoint(t, &hits, http.StatusBadGateway, `upstream down`)

	soon := time.Now().Add(2 * time.Minute)
	resolver, _ := newCredentialsRefreshFixture(t, srv.URL, &soon)

	creds, err := resolver.GetCredentialsByConnectionID(context.Background(), 7)
	if err != nil {
		t.Fatalf("transient refresh failure should not fail resolution: %v", err)
	}
	if creds.OAuthAccessToken != "old-access-token" {
		t.Errorf("expected existing token kept on transient failure, got %q", creds.OAuthAccessToken)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("expected one refresh attempt, got %d", got)
	}
}

// testEncryption returns the same secret the refresh fixture uses, so tests
// can decrypt persisted tokens.
func testEncryption() *sso.SecretEncryption {
	return sso.NewSecretEncryption("credentials-refresh-test-secret-with-enough-length")
}

// seedUserTokenFromWorkspace copies the workspace connection's encrypted token
// pair into a user row, mirroring the OAuth callback which stores one grant at
// both levels.
func seedUserTokenFromWorkspace(t *testing.T, tdb *testutils.TestDB, userID, connectionID int, expiresAt *time.Time) {
	t.Helper()
	var providerID int
	var accessEnc, refreshEnc string
	if err := tdb.QueryRow(`
		SELECT scm_provider_id, oauth_access_token_encrypted, oauth_refresh_token_encrypted
		FROM workspace_scm_connections WHERE id = ?
	`, connectionID).Scan(&providerID, &accessEnc, &refreshEnc); err != nil {
		t.Fatalf("read workspace token: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO user_scm_oauth_tokens
			(user_id, scm_provider_id, oauth_access_token_encrypted, oauth_refresh_token_encrypted, oauth_token_expires_at)
		VALUES (?, ?, ?, ?, ?)
	`, userID, providerID, accessEnc, refreshEnc, expiresAt); err != nil {
		t.Fatalf("seed user token: %v", err)
	}
}

func decryptToken(t *testing.T, value string) string {
	t.Helper()
	plain, err := testEncryption().Decrypt(value)
	if err != nil {
		t.Fatalf("decrypt token: %v", err)
	}
	return plain
}

// TestGetCredentialsByConnectionID_RefreshMirrorsOntoUserToken pins the
// invariant that a workspace-level refresh keeps the user-level copy of the
// same OAuth grant usable. Gitea/Forgejo rotate the refresh token and
// invalidate the previous access token, so a diverged user copy would fail
// the next user-initiated action.
func TestGetCredentialsByConnectionID_RefreshMirrorsOntoUserToken(t *testing.T) {
	var hits atomic.Int32
	srv := giteaTokenEndpoint(t, &hits, http.StatusOK,
		`{"access_token":"new-access-token","token_type":"bearer","refresh_token":"new-refresh-token","expires_in":3600}`)

	soon := time.Now().Add(2 * time.Minute)
	resolver, tdb := newCredentialsRefreshFixture(t, srv.URL, &soon)
	seedUserTokenFromWorkspace(t, tdb, 1, 7, &soon)

	creds, err := resolver.GetCredentialsByConnectionID(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetCredentialsByConnectionID: %v", err)
	}
	if creds.OAuthAccessToken != "new-access-token" {
		t.Fatalf("resolved access token = %q, want new-access-token", creds.OAuthAccessToken)
	}

	var userAccessEnc, userRefreshEnc string
	if err := tdb.QueryRow(`
		SELECT oauth_access_token_encrypted, oauth_refresh_token_encrypted
		FROM user_scm_oauth_tokens WHERE user_id = 1
	`).Scan(&userAccessEnc, &userRefreshEnc); err != nil {
		t.Fatalf("read user token: %v", err)
	}
	if got := decryptToken(t, userAccessEnc); got != "new-access-token" {
		t.Errorf("user access token = %q, want new-access-token", got)
	}
	if got := decryptToken(t, userRefreshEnc); got != "new-refresh-token" {
		t.Errorf("user refresh token = %q, want new-refresh-token", got)
	}
}

// TestGetCredentialsForUser_RefreshMirrorsOntoWorkspaceToken is the reverse:
// a user-initiated refresh must keep the workspace connection usable for the
// background sync.
func TestGetCredentialsForUser_RefreshMirrorsOntoWorkspaceToken(t *testing.T) {
	var hits atomic.Int32
	srv := giteaTokenEndpoint(t, &hits, http.StatusOK,
		`{"access_token":"new-access-token","token_type":"bearer","refresh_token":"new-refresh-token","expires_in":3600}`)

	soon := time.Now().Add(2 * time.Minute)
	resolver, tdb := newCredentialsRefreshFixture(t, srv.URL, &soon)
	seedUserTokenFromWorkspace(t, tdb, 1, 7, &soon)

	creds, err := resolver.GetCredentialsForUser(context.Background(), 7, 1)
	if err != nil {
		t.Fatalf("GetCredentialsForUser: %v", err)
	}
	if creds.OAuthAccessToken != "new-access-token" {
		t.Fatalf("resolved access token = %q, want new-access-token", creds.OAuthAccessToken)
	}

	var wsAccessEnc, wsRefreshEnc string
	if err := tdb.QueryRow(`
		SELECT oauth_access_token_encrypted, oauth_refresh_token_encrypted
		FROM workspace_scm_connections WHERE id = 7
	`).Scan(&wsAccessEnc, &wsRefreshEnc); err != nil {
		t.Fatalf("read workspace token: %v", err)
	}
	if got := decryptToken(t, wsAccessEnc); got != "new-access-token" {
		t.Errorf("workspace access token = %q, want new-access-token", got)
	}
	if got := decryptToken(t, wsRefreshEnc); got != "new-refresh-token" {
		t.Errorf("workspace refresh token = %q, want new-refresh-token", got)
	}
}

// TestGetCredentialsByConnectionID_RefreshDoesNotClobberDifferentUserToken
// guards the comparison: a user row connected through a separate authorization
// must keep its own refresh token.
func TestGetCredentialsByConnectionID_RefreshDoesNotClobberDifferentUserToken(t *testing.T) {
	var hits atomic.Int32
	srv := giteaTokenEndpoint(t, &hits, http.StatusOK,
		`{"access_token":"new-access-token","token_type":"bearer","refresh_token":"new-refresh-token","expires_in":3600}`)

	soon := time.Now().Add(2 * time.Minute)
	resolver, tdb := newCredentialsRefreshFixture(t, srv.URL, &soon)

	enc := testEncryption()
	accessEnc, err := enc.Encrypt("independent-access-token")
	if err != nil {
		t.Fatalf("encrypt access: %v", err)
	}
	refreshEnc, err := enc.Encrypt("independent-refresh-token")
	if err != nil {
		t.Fatalf("encrypt refresh: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO user_scm_oauth_tokens
			(user_id, scm_provider_id, oauth_access_token_encrypted, oauth_refresh_token_encrypted, oauth_token_expires_at)
		VALUES (?, ?, ?, ?, ?)
	`, 1, 1, accessEnc, refreshEnc, soon); err != nil {
		t.Fatalf("seed independent user token: %v", err)
	}

	if _, err := resolver.GetCredentialsByConnectionID(context.Background(), 7); err != nil {
		t.Fatalf("GetCredentialsByConnectionID: %v", err)
	}

	var storedAccessEnc, storedRefreshEnc string
	if err := tdb.QueryRow(`
		SELECT oauth_access_token_encrypted, oauth_refresh_token_encrypted
		FROM user_scm_oauth_tokens WHERE user_id = 1
	`).Scan(&storedAccessEnc, &storedRefreshEnc); err != nil {
		t.Fatalf("read user token: %v", err)
	}
	if got := decryptToken(t, storedAccessEnc); got != "independent-access-token" {
		t.Errorf("independent user access token = %q, want independent-access-token", got)
	}
	if got := decryptToken(t, storedRefreshEnc); got != "independent-refresh-token" {
		t.Errorf("independent user refresh token = %q, want independent-refresh-token", got)
	}
}

// TestGetCredentialsByConnectionID_KeepsRefreshTokenWhenResponseOmitsIt
// covers providers that do not echo a new refresh token on every refresh.
func TestGetCredentialsByConnectionID_KeepsRefreshTokenWhenResponseOmitsIt(t *testing.T) {
	var hits atomic.Int32
	srv := giteaTokenEndpoint(t, &hits, http.StatusOK,
		`{"access_token":"new-access-token","token_type":"bearer","expires_in":3600}`)

	soon := time.Now().Add(2 * time.Minute)
	resolver, tdb := newCredentialsRefreshFixture(t, srv.URL, &soon)

	if _, err := resolver.GetCredentialsByConnectionID(context.Background(), 7); err != nil {
		t.Fatalf("GetCredentialsByConnectionID: %v", err)
	}

	var refreshEnc string
	if err := tdb.QueryRow(`
		SELECT oauth_refresh_token_encrypted FROM workspace_scm_connections WHERE id = 7
	`).Scan(&refreshEnc); err != nil {
		t.Fatalf("read workspace refresh token: %v", err)
	}
	if got := decryptToken(t, refreshEnc); got != "old-refresh-token" {
		t.Fatalf("stored refresh token = %q, want old-refresh-token", got)
	}
}
