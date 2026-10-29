//go:build test

package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func assertEmailOAuthBindingDenied(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 redirect; body=%q", rr.Code, rr.Body.String())
	}
	location := rr.Header().Get("Location")
	if !strings.Contains(location, "oauth_error=authorization_failed") {
		t.Fatalf("location = %q, want an authorization_failed redirect", location)
	}
}

// TestChannelEmailOAuthCallbackRejectsOtherAccounts pins the account binding on
// the channel-inline email OAuth callback: only the session that started the
// flow may complete it, so a forwarded authorization URL cannot attach a
// victim's mailbox to another identity.
func TestChannelEmailOAuthCallbackRejectsOtherAccounts(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	channelID := testutils.InsertID(t, db, `INSERT INTO channels (name, type, direction, status) VALUES ('Inline email', 'email', 'inbound', 'enabled')`)
	ownerID := testutils.InsertID(t, db, `INSERT INTO users (email, username, first_name, last_name, is_active) VALUES ('inline-owner@example.test', 'inline-owner', 'Inline', 'Owner', true)`)
	if _, err := db.Exec(`INSERT INTO channel_managers (channel_id, manager_type, manager_id) VALUES (?, 'user', ?)`, channelID, ownerID); err != nil {
		t.Fatalf("assign channel manager: %v", err)
	}

	insertState := func(t *testing.T, state string) {
		t.Helper()
		if _, err := db.Exec(`
			INSERT INTO email_oauth_state (provider_id, channel_id, state, user_id, restore_channel_enabled, expires_at)
			VALUES (NULL, ?, ?, ?, false, ?)
		`, channelID, state, ownerID, time.Now().Add(5*time.Minute)); err != nil {
			t.Fatalf("insert oauth state %s: %v", state, err)
		}
	}

	handler := &ChannelHandler{
		channelRepo: repository.NewChannelRepository(db),
		service:     services.NewChannelService(db, nil),
	}
	callback := func(t *testing.T, state string, user *models.User) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/channels/inline-oauth/callback?code=xyz&state="+state, nil)
		if user != nil {
			req = testutils.WithAuthContext(req, user)
		}
		rr := httptest.NewRecorder()
		handler.ChannelEmailOAuthCallback(rr, req)
		return rr
	}

	t.Run("unauthenticated", func(t *testing.T) {
		insertState(t, "state-inline-anon")
		assertEmailOAuthBindingDenied(t, callback(t, "state-inline-anon", nil))
	})

	t.Run("different account", func(t *testing.T) {
		insertState(t, "state-inline-mismatch")
		assertEmailOAuthBindingDenied(t, callback(t, "state-inline-mismatch", &models.User{ID: ownerID + 1, Username: "other"}))
	})
}

// TestEmailOAuthCallbackRejectsOtherAccounts pins the same binding on the
// provider-based email OAuth callback.
func TestEmailOAuthCallbackRejectsOtherAccounts(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	providerID := testutils.InsertID(t, db, `INSERT INTO email_providers (name, slug, type, is_enabled) VALUES ('Microsoft', 'ms-test', 'microsoft', true)`)
	channelID := testutils.InsertID(t, db, `INSERT INTO channels (name, type, direction, status) VALUES ('Provider email', 'email', 'inbound', 'enabled')`)
	ownerID := testutils.InsertID(t, db, `INSERT INTO users (email, username, first_name, last_name, is_active) VALUES ('provider-owner@example.test', 'provider-owner', 'Provider', 'Owner', true)`)
	if _, err := db.Exec(`INSERT INTO channel_managers (channel_id, manager_type, manager_id) VALUES (?, 'user', ?)`, channelID, ownerID); err != nil {
		t.Fatalf("assign channel manager: %v", err)
	}

	insertState := func(t *testing.T, state string) {
		t.Helper()
		if _, err := db.Exec(`
			INSERT INTO email_oauth_state (provider_id, channel_id, state, user_id, restore_channel_enabled, expires_at)
			VALUES (?, ?, ?, ?, false, ?)
		`, providerID, channelID, state, ownerID, time.Now().Add(5*time.Minute)); err != nil {
			t.Fatalf("insert oauth state %s: %v", state, err)
		}
	}

	handler := &EmailProviderHandler{
		db:       db,
		channels: services.NewChannelService(db, nil),
	}
	callback := func(t *testing.T, state string, user *models.User) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/email/oauth/ms-test/callback?code=xyz&state="+state, nil)
		req.SetPathValue("slug", "ms-test")
		if user != nil {
			req = testutils.WithAuthContext(req, user)
		}
		rr := httptest.NewRecorder()
		handler.EmailOAuthCallback(rr, req)
		return rr
	}

	t.Run("unauthenticated", func(t *testing.T) {
		insertState(t, "state-provider-anon")
		assertEmailOAuthBindingDenied(t, callback(t, "state-provider-anon", nil))
	})

	t.Run("different account", func(t *testing.T) {
		insertState(t, "state-provider-mismatch")
		assertEmailOAuthBindingDenied(t, callback(t, "state-provider-mismatch", &models.User{ID: ownerID + 1, Username: "other"}))
	})
}
