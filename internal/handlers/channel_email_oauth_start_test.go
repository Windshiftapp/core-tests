//go:build test

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// newInlineEmailOAuthHandler seeds a managed email channel with the given
// config JSON and returns a handler plus the channel and owner IDs. Encryption
// is left nil so the legacy-plaintext client-secret path is exercised.
func newInlineEmailOAuthHandler(t *testing.T, configJSON string) (*ChannelHandler, int, int) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	channelID := testutils.InsertID(t, db,
		`INSERT INTO channels (name, type, direction, status, config) VALUES ('Inline email', 'email', 'inbound', 'disabled', ?)`,
		configJSON)
	ownerID := testutils.InsertID(t, db,
		`INSERT INTO users (email, username, first_name, last_name, is_active) VALUES ('inline-start@example.test', 'inline-start', 'Inline', 'Start', true)`)
	if _, err := db.Exec(`INSERT INTO channel_managers (channel_id, manager_type, manager_id) VALUES (?, 'user', ?)`, channelID, ownerID); err != nil {
		t.Fatalf("assign channel manager: %v", err)
	}

	handler := &ChannelHandler{
		channelRepo: repository.NewChannelRepository(db),
		service:     services.NewChannelService(db, nil),
		baseURL:     "https://windshift.test",
	}
	return handler, channelID, ownerID
}

func startInlineEmailOAuth(t *testing.T, handler *ChannelHandler, channelID, userID int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/channels/"+strconv.Itoa(channelID)+"/email-oauth/start", nil)
	req.SetPathValue("id", strconv.Itoa(channelID))
	req = testutils.WithAuthContext(req, &models.User{ID: userID})
	rr := httptest.NewRecorder()
	handler.StartChannelEmailOAuth(rr, req)
	return rr
}

// TestStartChannelEmailOAuthRequiresMailbox pins that the inline flow refuses
// to start without the admin-entered mailbox. Without it the callback has no
// address to authenticate as, which is the GH #363 failure mode.
func TestStartChannelEmailOAuthRequiresMailbox(t *testing.T) {
	config, err := json.Marshal(models.ChannelConfig{
		EmailAuthMethod:        "oauth",
		EmailOAuthProviderType: "microsoft",
		EmailOAuthClientID:     "client",
		EmailOAuthClientSecret: "legacy-plaintext-secret",
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	handler, channelID, userID := newInlineEmailOAuthHandler(t, string(config))

	rr := startInlineEmailOAuth(t, handler, channelID, userID)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%q", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "login.microsoftonline.com") {
		t.Errorf("body leaked a Microsoft auth URL without a mailbox: %s", rr.Body.String())
	}
}

// TestStartChannelEmailOAuthRequestsAccountSelection pins the authorize URL
// contract: the admin picks the account, so the flow must send
// prompt=select_account instead of reusing whatever SSO session exists.
func TestStartChannelEmailOAuthRequestsAccountSelection(t *testing.T) {
	config, err := json.Marshal(models.ChannelConfig{
		EmailAuthMethod:        "oauth",
		EmailOAuthProviderType: "microsoft",
		EmailOAuthClientID:     "client",
		EmailOAuthClientSecret: "legacy-plaintext-secret",
		EmailOAuthMailbox:      "support@example.com",
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	handler, channelID, userID := newInlineEmailOAuthHandler(t, string(config))

	rr := startInlineEmailOAuth(t, handler, channelID, userID)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%q", err, rr.Body.String())
	}
	authURL, err := url.Parse(resp["auth_url"])
	if err != nil {
		t.Fatalf("parse auth_url: %v", err)
	}
	if got := authURL.Query().Get("prompt"); got != "select_account" {
		t.Fatalf("prompt = %q, want select_account", got)
	}
	if got := authURL.Query().Get("redirect_uri"); got != "https://windshift.test/api/channels/inline-oauth/callback" {
		t.Fatalf("redirect_uri = %q", got)
	}
}
