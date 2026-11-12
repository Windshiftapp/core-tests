//go:build test

package services

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/integrations/docmost"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type fakeDocmostClient struct {
	registerID  string
	registerErr error
	exchange    docmost.Token
	exchangeErr error
	refresh     docmost.Token
	refreshErr  error
	revoked     int
}

func (f *fakeDocmostClient) RegisterClient(context.Context, string, string, string) (string, error) {
	return f.registerID, f.registerErr
}

func (f *fakeDocmostClient) ExchangeCode(context.Context, string, string, string, string, string) (docmost.Token, error) {
	return f.exchange, f.exchangeErr
}

func (f *fakeDocmostClient) RefreshToken(context.Context, string, string, string) (docmost.Token, error) {
	return f.refresh, f.refreshErr
}

func (f *fakeDocmostClient) RevokeToken(context.Context, string, string, string) error {
	f.revoked++
	return nil
}

// newDocmostServiceEnv seeds a Docmost integration provider and a user on the
// production schema and returns the service under test.
func newDocmostServiceEnv(t *testing.T, client DocmostClientAPI) (*DocmostService, database.Database, string) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	db := tdb.DB

	providerID := "01a1aaff-0000-0000-0000-000000000001"
	if _, err := db.ExecWrite(`
		INSERT INTO integration_providers (id, slug, name, provider_type, enabled, provider_config)
		VALUES (?, 'docmost', 'Docmost', 'docmost', true, ?)
	`, providerID, `{"base_url":"https://wiki.example.com"}`); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	testutils.InsertID(t, db, `INSERT INTO users (email, username, first_name, last_name, password_hash, is_active) VALUES ('u2@test.com', 'u2', 'U', 'Two', 'x', true)`)

	svc := NewDocmostService(
		repository.NewIntegrationProviderRepository(db),
		repository.NewIntegrationTokenRepository(db),
		NewDocmostCredentialEncryption("test-server-secret-with-sufficient-length-for-derivation"),
		client,
	)
	return svc, db, providerID
}

func TestDocmostService_StartOAuthRegistersClientAndStoresPKCE(t *testing.T) {
	client := &fakeDocmostClient{registerID: "client-123"}
	svc, db, providerID := newDocmostServiceEnv(t, client)

	authorizeURL, verifier, err := svc.StartOAuth(context.Background(), providerID, "state-1", "http://windshift.test/api/integrations/oauth/docmost/callback")
	if err != nil {
		t.Fatalf("start oauth: %v", err)
	}
	parsed, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}
	if parsed.Path != "/oauth/consent" || parsed.Host != "wiki.example.com" {
		t.Fatalf("authorize url = %q", authorizeURL)
	}
	query := parsed.Query()
	if query.Get("client_id") != "client-123" || query.Get("state") != "state-1" || query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
		t.Fatalf("authorize query = %v", query)
	}
	if verifier == "" {
		t.Fatal("empty code verifier")
	}

	var storedClientID string
	if err := db.QueryRow(`SELECT oauth_client_id FROM integration_providers WHERE id = ?`, providerID).Scan(&storedClientID); err != nil {
		t.Fatalf("read client id: %v", err)
	}
	if storedClientID != "client-123" {
		t.Fatalf("stored client id = %q", storedClientID)
	}
}

func TestDocmostService_CompleteOAuthStoresEncryptedToken(t *testing.T) {
	client := &fakeDocmostClient{
		registerID: "client-123",
		exchange:   docmost.Token{AccessToken: "oauth-access", RefreshToken: "oauth-refresh", Scope: "read write", ExpiresAt: time.Now().Add(time.Hour)},
	}
	svc, db, providerID := newDocmostServiceEnv(t, client)

	if _, _, err := svc.StartOAuth(context.Background(), providerID, "state-1", "http://windshift.test/cb"); err != nil {
		t.Fatalf("start oauth: %v", err)
	}
	if err := svc.CompleteOAuth(context.Background(), providerID, 1, "verifier", "code", "http://windshift.test/cb"); err != nil {
		t.Fatalf("complete oauth: %v", err)
	}

	var encAccess, encRefresh, authMethod string
	var expiresAt time.Time
	if err := db.QueryRow(`
		SELECT oauth_access_token_encrypted, encrypted_refresh_token, auth_method, expires_at
		FROM user_integration_tokens WHERE integration_provider_id = ? AND user_id = ?
	`, providerID, "1").Scan(&encAccess, &encRefresh, &authMethod, &expiresAt); err != nil {
		t.Fatalf("read token: %v", err)
	}
	if encAccess == "oauth-access" || encRefresh == "oauth-refresh" {
		t.Fatal("tokens stored in plaintext")
	}
	if authMethod != "oauth" || !expiresAt.After(time.Now()) {
		t.Fatalf("auth_method = %q expires_at = %v", authMethod, expiresAt)
	}

	_, token, err := svc.Credential(context.Background(), 1)
	if err != nil {
		t.Fatalf("credential: %v", err)
	}
	if token != "oauth-access" {
		t.Fatalf("token = %q", token)
	}
}

func TestDocmostService_CredentialRefreshesExpiredToken(t *testing.T) {
	client := &fakeDocmostClient{
		registerID: "client-123",
		refresh:    docmost.Token{AccessToken: "fresh-access", RefreshToken: "fresh-refresh", ExpiresAt: time.Now().Add(time.Hour)},
	}
	svc, db, providerID := newDocmostServiceEnv(t, client)
	if err := svc.providers.Update(providerID, repository.IntegrationProviderUpdate{OAuthClientID: ptr("client-123")}); err != nil {
		t.Fatalf("set client id: %v", err)
	}
	seedExpiredToken(t, svc, db, providerID, 1, "stale-access", "stale-refresh")

	_, token, err := svc.Credential(context.Background(), 1)
	if err != nil {
		t.Fatalf("credential: %v", err)
	}
	if token != "fresh-access" {
		t.Fatalf("token = %q, want the refreshed token", token)
	}
}

func TestDocmostService_RefreshRejectedMarksReconnect(t *testing.T) {
	client := &fakeDocmostClient{registerID: "client-123", refreshErr: docmost.ErrUnauthorized}
	svc, db, providerID := newDocmostServiceEnv(t, client)
	if err := svc.providers.Update(providerID, repository.IntegrationProviderUpdate{OAuthClientID: ptr("client-123")}); err != nil {
		t.Fatalf("set client id: %v", err)
	}
	seedExpiredToken(t, svc, db, providerID, 1, "stale-access", "stale-refresh")

	if _, _, err := svc.Credential(context.Background(), 1); !errors.Is(err, ErrDocmostReconnect) {
		t.Fatalf("credential error = %v, want ErrDocmostReconnect", err)
	}
	var invalidated *time.Time
	if err := db.QueryRow(`SELECT invalidated_at FROM user_integration_tokens WHERE integration_provider_id = ? AND user_id = ?`, providerID, "1").Scan(&invalidated); err != nil {
		t.Fatalf("read invalidated_at: %v", err)
	}
	if invalidated == nil {
		t.Fatal("invalidated_at was not set after a rejected refresh")
	}
}

func TestDocmostService_ReconnectClearsInvalidated(t *testing.T) {
	client := &fakeDocmostClient{
		registerID: "client-123",
		exchange:   docmost.Token{AccessToken: "fresh-access", RefreshToken: "fresh-refresh", ExpiresAt: time.Now().Add(time.Hour)},
	}
	svc, db, providerID := newDocmostServiceEnv(t, client)
	if err := svc.providers.Update(providerID, repository.IntegrationProviderUpdate{OAuthClientID: ptr("client-123")}); err != nil {
		t.Fatalf("set client id: %v", err)
	}
	seedExpiredToken(t, svc, db, providerID, 1, "stale-access", "stale-refresh")
	svc.MarkInvalid(1)

	// Reconnecting runs the OAuth callback again, which upserts the token pair.
	if err := svc.CompleteOAuth(context.Background(), providerID, 1, "verifier", "code", "http://windshift.test/cb"); err != nil {
		t.Fatalf("complete oauth: %v", err)
	}

	var invalidated *time.Time
	if err := db.QueryRow(`SELECT invalidated_at FROM user_integration_tokens WHERE integration_provider_id = ? AND user_id = ?`, providerID, "1").Scan(&invalidated); err != nil {
		t.Fatalf("read invalidated_at: %v", err)
	}
	if invalidated != nil {
		t.Fatalf("invalidated_at = %v, want NULL after reconnect", invalidated)
	}
	if _, token, err := svc.Credential(context.Background(), 1); err != nil || token != "fresh-access" {
		t.Fatalf("credential after reconnect = %q, %v", token, err)
	}
}

func TestDocmostService_NotConfigured(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	svc := NewDocmostService(
		repository.NewIntegrationProviderRepository(tdb.DB),
		repository.NewIntegrationTokenRepository(tdb.DB),
		NewDocmostCredentialEncryption("test-server-secret-with-sufficient-length-for-derivation"),
		&fakeDocmostClient{},
	)
	if _, _, err := svc.Credential(context.Background(), 1); !errors.Is(err, ErrDocmostNotConfigured) {
		t.Fatalf("credential error = %v, want ErrDocmostNotConfigured", err)
	}
}

func seedExpiredToken(t *testing.T, svc *DocmostService, db database.Database, providerID string, userID int, access, refresh string) {
	t.Helper()
	encAccess, err := svc.encryption.Encrypt(access)
	if err != nil {
		t.Fatalf("encrypt access: %v", err)
	}
	encRefresh, err := svc.encryption.Encrypt(refresh)
	if err != nil {
		t.Fatalf("encrypt refresh: %v", err)
	}
	past := time.Now().Add(-time.Minute)
	if err := svc.tokens.UpsertOAuthToken(providerID, userID, encAccess, encRefresh, "read write", &past); err != nil {
		t.Fatalf("seed token: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
