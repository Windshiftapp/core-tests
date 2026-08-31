//go:build test

package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"windshift/internal/auth"
	"windshift/internal/database"
	"windshift/internal/testutils"
)

const (
	refreshTestClientID = "refresh-rotation-client"
	refreshTestResource = "https://windshift.example/mcp"
)

type refreshLookupBarrierDB struct {
	database.Database
	arrived chan struct{}
	release chan struct{}
}

type refreshLinkFailureDB struct {
	database.Database
}

func (db *refreshLinkFailureDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (database.Tx, error) {
	tx, err := db.Database.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &refreshLinkFailureTx{Tx: tx}, nil
}

type refreshLinkFailureTx struct {
	database.Tx
}

func (tx *refreshLinkFailureTx) ExecWriteContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	if strings.Contains(query, "SET rotated_to_id = ?") {
		return nil, errors.New("injected refresh-lineage write failure")
	}
	return tx.Tx.ExecWriteContext(ctx, query, args...)
}

func (db *refreshLookupBarrierDB) QueryRow(query string, args ...interface{}) *sql.Row {
	row := db.Database.QueryRow(query, args...)
	if strings.Contains(query, "FROM oauth_refresh_tokens") && strings.Contains(query, "WHERE token_hash = ?") {
		db.arrived <- struct{}{}
		<-db.release
	}
	return row
}

func newOAuthRefreshRotationTest(t *testing.T) (*OAuthHandler, *auth.TokenManager, database.Database, *oauthTokenResponse) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })

	allowed := `["mcp:access","items:read"]`
	redirects := `["http://127.0.0.1/callback"]`
	if _, err := tdb.ExecWrite(`
		INSERT INTO oauth_clients (
			slug, display_name, client_id, client_type, redirect_uris,
			allowed_scopes, resource_uri, enabled, created_by
		) VALUES (?, ?, ?, 'public', ?, ?, ?, true, ?)
	`, "refresh-rotation", "Refresh Rotation", refreshTestClientID,
		redirects, allowed, refreshTestResource, 1); err != nil {
		t.Fatalf("insert OAuth client: %v", err)
	}

	tm := auth.NewTokenManager(tdb.Database, nil)
	h := &OAuthHandler{db: tdb.Database, tokenManager: tm}
	client, err := h.lookupEnabledClientByClientID(refreshTestClientID)
	if err != nil {
		t.Fatalf("lookup OAuth client: %v", err)
	}
	initial, err := h.mintAccessAndRefresh(client, 1, 1,
		[]string{auth.ScopeMCPAccess, auth.ScopeItemsRead}, refreshTestResource)
	if err != nil {
		t.Fatalf("mint initial token pair: %v", err)
	}
	return h, tm, tdb.Database, initial
}

func redeemRefresh(h *OAuthHandler, refreshToken string) *httptest.ResponseRecorder {
	params := url.Values{
		"client_id":     {refreshTestClientID},
		"refresh_token": {refreshToken},
		"resource":      {refreshTestResource},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/token", strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.tokenRefreshToken(rr, req, params)
	return rr
}

func decodeOAuthTokenResponse(t *testing.T, rr *httptest.ResponseRecorder) oauthTokenResponse {
	t.Helper()
	var response oauthTokenResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode token response: %v; body=%s", err, rr.Body.String())
	}
	return response
}

func TestOAuthRefreshRotationRetainsLineageAndExpiresOldAccessToken(t *testing.T) {
	h, tm, db, initial := newOAuthRefreshRotationTest(t)

	// Prime the validation cache so the rotation must evict it after commit.
	if _, _, err := tm.ValidateToken(initial.AccessToken); err != nil {
		t.Fatalf("validate initial access token: %v", err)
	}

	var oldRefreshID, oldAccessID int
	if err := db.QueryRow(`
		SELECT id, api_token_id FROM oauth_refresh_tokens WHERE token_hash = ?
	`, hashRefreshToken(initial.RefreshToken)).Scan(&oldRefreshID, &oldAccessID); err != nil {
		t.Fatalf("load initial refresh row: %v", err)
	}

	rr := redeemRefresh(h, initial.RefreshToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	replacement := decodeOAuthTokenResponse(t, rr)

	var revokedAt sql.NullTime
	var rotatedToID sql.NullInt64
	if err := db.QueryRow(`
		SELECT revoked_at, rotated_to_id FROM oauth_refresh_tokens WHERE id = ?
	`, oldRefreshID).Scan(&revokedAt, &rotatedToID); err != nil {
		t.Fatalf("old refresh lineage row must be retained: %v", err)
	}
	if !revokedAt.Valid || !rotatedToID.Valid {
		t.Fatalf("old refresh row = revoked_at:%v rotated_to_id:%v, want both set", revokedAt, rotatedToID)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM oauth_refresh_tokens WHERE id = ?`, rotatedToID.Int64); n != 1 {
		t.Fatalf("replacement refresh row count = %d, want 1", n)
	}
	var replacementClient, replacementScopes, replacementResource string
	var replacementUser, replacementAgent int
	if err := db.QueryRow(`
		SELECT client_id, user_id, agent_id, scopes, resource_uri
		FROM oauth_refresh_tokens WHERE id = ?
	`, rotatedToID.Int64).Scan(&replacementClient, &replacementUser, &replacementAgent,
		&replacementScopes, &replacementResource); err != nil {
		t.Fatalf("load replacement refresh bindings: %v", err)
	}
	if replacementClient != refreshTestClientID || replacementUser != 1 || replacementAgent != 1 ||
		replacementScopes != `["mcp:access","items:read"]` || replacementResource != refreshTestResource {
		t.Fatalf("replacement bindings = client:%q user:%d agent:%d scopes:%s resource:%q",
			replacementClient, replacementUser, replacementAgent, replacementScopes, replacementResource)
	}

	var oldAccessExpiresAt time.Time
	if err := db.QueryRow(`SELECT expires_at FROM api_tokens WHERE id = ?`, oldAccessID).Scan(&oldAccessExpiresAt); err != nil {
		t.Fatalf("old access-token row must be retained but expired: %v", err)
	}
	if oldAccessExpiresAt.After(time.Now()) {
		t.Fatalf("old access token expires_at = %s, want expired", oldAccessExpiresAt)
	}
	if _, _, err := tm.ValidateToken(initial.AccessToken); err == nil {
		t.Fatal("old cached access token remained valid after refresh rotation")
	}
	if _, _, err := tm.ValidateToken(replacement.AccessToken); err != nil {
		t.Fatalf("replacement access token should be valid: %v", err)
	}
}

func TestOAuthRefreshRotationConcurrentRedemptionRevokesFamily(t *testing.T) {
	h, tm, baseDB, initial := newOAuthRefreshRotationTest(t)
	barrierDB := &refreshLookupBarrierDB{
		Database: baseDB,
		arrived:  make(chan struct{}, 2),
		release:  make(chan struct{}),
	}
	h.db = barrierDB

	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() {
			results <- redeemRefresh(h, initial.RefreshToken)
		}()
	}

	for range 2 {
		select {
		case <-barrierDB.arrived:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for concurrent refresh lookups")
		}
	}
	close(barrierDB.release)

	var successes, invalidGrants int
	var issued oauthTokenResponse
	for range 2 {
		rr := <-results
		switch rr.Code {
		case http.StatusOK:
			successes++
			issued = decodeOAuthTokenResponse(t, rr)
		case http.StatusBadRequest:
			var body map[string]string
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode OAuth error: %v; body=%s", err, rr.Body.String())
			}
			if body["error"] != oauthErrInvalidGrant {
				t.Fatalf("OAuth error = %q, want %q; body=%s", body["error"], oauthErrInvalidGrant, rr.Body.String())
			}
			invalidGrants++
		default:
			t.Fatalf("concurrent refresh status = %d; body=%s", rr.Code, rr.Body.String())
		}
	}
	if successes != 1 || invalidGrants != 1 {
		t.Fatalf("concurrent refresh results: successes=%d invalid_grants=%d, want 1 each", successes, invalidGrants)
	}
	if n := countRows(t, baseDB, `SELECT COUNT(*) FROM oauth_refresh_tokens WHERE revoked_at IS NULL`); n != 0 {
		t.Fatalf("active refresh tokens after replay = %d, want 0", n)
	}
	if _, _, err := tm.ValidateToken(issued.AccessToken); err == nil {
		t.Fatal("access token from compromised refresh family remained valid")
	}
}

func TestOAuthRefreshRotationRollsBackEveryCredentialOnWriteFailure(t *testing.T) {
	h, tm, baseDB, initial := newOAuthRefreshRotationTest(t)
	h.db = &refreshLinkFailureDB{Database: baseDB}

	rr := redeemRefresh(h, initial.RefreshToken)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("refresh status = %d, want 500; body=%s", rr.Code, rr.Body.String())
	}

	var revokedAt sql.NullTime
	var rotatedToID sql.NullInt64
	if err := baseDB.QueryRow(`
		SELECT revoked_at, rotated_to_id FROM oauth_refresh_tokens WHERE token_hash = ?
	`, hashRefreshToken(initial.RefreshToken)).Scan(&revokedAt, &rotatedToID); err != nil {
		t.Fatalf("load original refresh token after rollback: %v", err)
	}
	if revokedAt.Valid || rotatedToID.Valid {
		t.Fatalf("original refresh changed despite rollback: revoked_at=%v rotated_to_id=%v", revokedAt, rotatedToID)
	}
	if n := countRows(t, baseDB, `SELECT COUNT(*) FROM oauth_refresh_tokens`); n != 1 {
		t.Fatalf("refresh-token rows after rollback = %d, want 1", n)
	}
	if n := countRows(t, baseDB, `SELECT COUNT(*) FROM api_tokens`); n != 1 {
		t.Fatalf("access-token rows after rollback = %d, want 1", n)
	}
	if _, _, err := tm.ValidateToken(initial.AccessToken); err != nil {
		t.Fatalf("original access token should remain valid after rollback: %v", err)
	}
}
