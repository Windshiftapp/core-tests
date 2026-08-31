package handlers

import (
	"database/sql"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/database"
)

// newOAuthRevokeTestDB stands up just the tables cascadeRevokeTokensForClient
// touches: api_tokens (deleted by AdminRevokeToken) and oauth_refresh_tokens
// (queried for the access-token links, then marked revoked).
func newOAuthRevokeTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB("file:oauthrevoke?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, stmt := range []string{
		`CREATE TABLE api_tokens (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE oauth_refresh_tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			client_id TEXT NOT NULL,
			api_token_id INTEGER NOT NULL,
			revoked_at DATETIME
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	return db
}

func countRows(t *testing.T, db database.Database, query string, args ...interface{}) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

func TestCascadeRevokeTokensForClient_RevokesAccessAndRefreshTokens(t *testing.T) {
	db := newOAuthRevokeTestDB(t)

	// Two access tokens for client-1, one for an unrelated client-2.
	for _, id := range []int{10, 11, 20} {
		if _, err := db.Exec(`INSERT INTO api_tokens (id) VALUES (?)`, id); err != nil {
			t.Fatalf("seed api_tokens: %v", err)
		}
	}
	seed := []struct {
		clientID   string
		apiTokenID int
	}{
		{"client-1", 10},
		{"client-1", 11},
		{"client-2", 20},
	}
	for _, s := range seed {
		if _, err := db.Exec(
			`INSERT INTO oauth_refresh_tokens (client_id, api_token_id) VALUES (?, ?)`,
			s.clientID, s.apiTokenID,
		); err != nil {
			t.Fatalf("seed refresh tokens: %v", err)
		}
	}

	tm := auth.NewTokenManager(db, nil)
	h := NewAdminOAuthClientHandler(db, tm, nil)

	revoked, err := h.cascadeRevokeTokensForClient("client-1")
	if err != nil {
		t.Fatalf("cascadeRevokeTokensForClient: %v", err)
	}
	if revoked != 2 {
		t.Fatalf("revoked = %d, want 2", revoked)
	}

	// client-1 access tokens are gone; client-2's survives.
	if n := countRows(t, db, `SELECT COUNT(*) FROM api_tokens WHERE id IN (10, 11)`); n != 0 {
		t.Fatalf("client-1 api_tokens remaining = %d, want 0", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM api_tokens WHERE id = 20`); n != 1 {
		t.Fatalf("client-2 api_token should survive, count = %d", n)
	}

	// client-1 refresh rows are marked revoked so they can't mint new tokens
	// after a re-enable; client-2's stays active.
	if n := countRows(t, db, `SELECT COUNT(*) FROM oauth_refresh_tokens WHERE client_id = 'client-1' AND revoked_at IS NULL`); n != 0 {
		t.Fatalf("client-1 refresh rows still active = %d, want 0", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM oauth_refresh_tokens WHERE client_id = 'client-2' AND revoked_at IS NULL`); n != 1 {
		t.Fatalf("client-2 refresh row should stay active, count = %d", n)
	}
}

func TestCascadeRevokeTokensForClient_NoTokensIsNoop(t *testing.T) {
	db := newOAuthRevokeTestDB(t)
	tm := auth.NewTokenManager(db, nil)
	h := NewAdminOAuthClientHandler(db, tm, nil)

	revoked, err := h.cascadeRevokeTokensForClient("client-unknown")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if revoked != 0 {
		t.Fatalf("revoked = %d, want 0", revoked)
	}
}

// Ensure a refresh row whose api_token was already deleted (stale link) doesn't
// block the cascade — AdminRevokeToken returns "not found" but the action must
// still complete and mark the refresh row revoked.
func TestCascadeRevokeTokensForClient_StaleTokenDoesNotBlock(t *testing.T) {
	db := newOAuthRevokeTestDB(t)
	if _, err := db.Exec(`INSERT INTO oauth_refresh_tokens (client_id, api_token_id) VALUES ('client-1', 999)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tm := auth.NewTokenManager(db, nil)
	h := NewAdminOAuthClientHandler(db, tm, nil)

	revoked, err := h.cascadeRevokeTokensForClient("client-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if revoked != 0 {
		t.Fatalf("revoked = %d, want 0 (token was already gone)", revoked)
	}
	var revokedAt sql.NullString
	if err := db.QueryRow(`SELECT revoked_at FROM oauth_refresh_tokens WHERE client_id = 'client-1'`).Scan(&revokedAt); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !revokedAt.Valid {
		t.Fatal("stale refresh row should still be marked revoked")
	}
}
