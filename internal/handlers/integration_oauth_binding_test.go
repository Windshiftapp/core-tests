//go:build test

package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// TestIntegrationOAuthCallbackRejectsOtherAccounts pins the account binding on
// the user-integration OAuth callback: a state minted for one account cannot be
// completed by a different signed-in account or by an unauthenticated browser,
// and no token is stored.
func TestIntegrationOAuthCallbackRejectsOtherAccounts(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	if _, err := db.Exec(`
		INSERT INTO integration_providers (id, slug, name, provider_type, enabled, oauth_client_id)
		VALUES ('prov-1', 'todoist', 'Todoist', 'todoist', true, 'client-abc')
	`); err != nil {
		t.Fatalf("insert provider: %v", err)
	}

	insertState := func(t *testing.T, id, state, userID string) {
		t.Helper()
		if _, err := db.Exec(`
			INSERT INTO integration_oauth_state (id, provider_id, state, user_id, expires_at)
			VALUES (?, 'prov-1', ?, ?, ?)
		`, id, state, userID, time.Now().Add(5*time.Minute)); err != nil {
			t.Fatalf("insert oauth state %s: %v", id, err)
		}
	}

	handler := NewIntegrationOAuthHandler(db, nil, "http://test.local")
	callback := func(t *testing.T, state string, user *models.User) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/integrations/oauth/todoist/callback?code=xyz&state="+state, nil)
		if user != nil {
			req = testutils.WithAuthContext(req, user)
		}
		rr := httptest.NewRecorder()
		handler.OAuthCallback(rr, req)
		return rr
	}

	// The binding message is the denial contract. A callback that got past the
	// binding check would instead fail later at the (unconfigured) provider
	// secret with a different message, so pinning the message distinguishes the
	// two paths.
	const bindingDenial = "message=Sign+in+to+the+account+that+started+this+connection"

	assertRejected := func(t *testing.T, rr *httptest.ResponseRecorder, wantMessage string) {
		t.Helper()
		if rr.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302 redirect", rr.Code)
		}
		location := rr.Header().Get("Location")
		if !strings.Contains(location, "oauth=error") || strings.Contains(location, "oauth=success") {
			t.Fatalf("location = %q, want an oauth error redirect", location)
		}
		if wantMessage != "" && !strings.Contains(location, wantMessage) {
			t.Fatalf("location = %q, want message %q", location, wantMessage)
		}
	}

	t.Run("different account", func(t *testing.T) {
		insertState(t, "st-mismatch", "state-mismatch", "1")
		assertRejected(t, callback(t, "state-mismatch", &models.User{ID: 2, Username: "other"}), bindingDenial)
	})

	t.Run("unauthenticated", func(t *testing.T) {
		insertState(t, "st-anon", "state-anon", "1")
		assertRejected(t, callback(t, "state-anon", nil), bindingDenial)
	})

	var tokens int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_integration_tokens`).Scan(&tokens); err != nil {
		t.Fatalf("count integration tokens: %v", err)
	}
	if tokens != 0 {
		t.Fatalf("stored %d integration tokens, want 0 after rejected callbacks", tokens)
	}

	// The rejected states are single-use: a replay finds no row and is rejected.
	insertState(t, "st-replay", "state-replay", "1")
	assertRejected(t, callback(t, "state-replay", &models.User{ID: 1, Username: "owner"}), "")
	assertRejected(t, callback(t, "state-replay", &models.User{ID: 1, Username: "owner"}), "")
}
