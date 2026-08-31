package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"windshift/internal/contextkeys"
	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// These tests cover the provider-backed email OAuth start route from
// docs/bughunt6.md Run 10 #1. Invariant being defended: the path params,
// query reads, and generated redirect URI must stay aligned with the
// registered routes — otherwise the flow can't complete a round trip.

// newEmailProviderTestHandler returns an EmailProviderHandler against a fresh
// database initialized from the embedded production schema. Keeping handler
// tests on the canonical schema prevents authorization and migration changes
// from being hidden by hand-written table subsets.
func newEmailProviderTestHandler(t *testing.T) (*EmailProviderHandler, database.Database) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	h := NewEmailProviderHandler(db, nil, "http://test.local", services.NewChannelService(db, nil))
	return h, db
}

func seedEmailProvider(t *testing.T, db database.Database, slug, providerType string, enabled bool) int {
	t.Helper()
	var id int
	err := db.QueryRow(`
		INSERT INTO email_providers
		    (name, slug, type, is_enabled, oauth_client_id, oauth_client_secret_encrypted, oauth_scopes, oauth_tenant_id)
		VALUES (?, ?, ?, ?, 'client-abc', 'legacy-test-secret', 'openid email', 'common')
		RETURNING id
	`, slug, slug, providerType, enabled).Scan(&id)
	if err != nil {
		t.Fatalf("seed provider %s: %v", slug, err)
	}
	return id
}

func seedEmailChannel(t *testing.T, db database.Database, name string, managerID int) int {
	t.Helper()
	var id int
	err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config)
		VALUES (?, 'email', 'inbound', 'enabled', '{}')
		RETURNING id
	`, name).Scan(&id)
	if err != nil {
		t.Fatalf("seed channel %s: %v", name, err)
	}
	if managerID != 0 {
		if _, err := db.Exec(`
			INSERT INTO channel_managers (channel_id, manager_type, manager_id)
			VALUES (?, 'user', ?)
		`, id, managerID); err != nil {
			t.Fatalf("seed manager for channel %s: %v", name, err)
		}
	}
	return id
}

func seedEmailTestUser(t *testing.T, db database.Database, username string) int {
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

// emailStartReq builds a POST request shaped like the route
// `POST /api/channels/{channel_id}/email-providers/{slug}/oauth/start`,
// with PathValues bound and the authenticated user injected into context.
// userID = 0 means "no user in context" (exercises the 401 path).
func emailStartReq(channelID int, slug string, userID int) *http.Request {
	target := "/api/channels/" + strconv.Itoa(channelID) + "/email-providers/" + slug + "/oauth/start"
	req := httptest.NewRequest(http.MethodPost, target, nil)
	req.SetPathValue("channel_id", strconv.Itoa(channelID))
	req.SetPathValue("slug", slug)
	if userID != 0 {
		ctx := context.WithValue(req.Context(), contextkeys.User, &models.User{ID: userID})
		req = req.WithContext(ctx)
	}
	return req
}

func countEmailOAuthStates(t *testing.T, db database.Database, providerID int) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM email_oauth_state WHERE provider_id = ?`, providerID).Scan(&n); err != nil {
		t.Fatalf("count states: %v", err)
	}
	return n
}

func TestStartEmailOAuth_EmitsAuthURLAndPersistsState(t *testing.T) {
	h, db := newEmailProviderTestHandler(t)
	uid := seedEmailTestUser(t, db, "alice")
	chID := seedEmailChannel(t, db, "support", uid)
	pid := seedEmailProvider(t, db, "ms-main", "microsoft", true)

	rec := httptest.NewRecorder()
	h.StartEmailOAuth(rec, emailStartReq(chID, "ms-main", uid))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 — body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v — body: %s", err, rec.Body.String())
	}
	authURL := resp["auth_url"]
	if authURL == "" {
		t.Fatalf("auth_url empty: %v", resp)
	}

	// One scm_oauth_state row should have landed under the provider and channel.
	if n := countEmailOAuthStates(t, db, pid); n != 1 {
		t.Errorf("email_oauth_state rows for provider: got %d want 1", n)
	}
	var stateChannelID, stateUserID int
	if err := db.QueryRow(`
		SELECT channel_id, user_id FROM email_oauth_state WHERE provider_id = ?
	`, pid).Scan(&stateChannelID, &stateUserID); err != nil {
		t.Fatalf("read state row: %v", err)
	}
	if stateChannelID != chID {
		t.Errorf("state.channel_id: got %d want %d", stateChannelID, chID)
	}
	if stateUserID != uid {
		t.Errorf("state.user_id: got %d want %d", stateUserID, uid)
	}
}

func TestStartEmailOAuth_RedirectURIMatchesCallbackRoute(t *testing.T) {
	// The redirect_uri embedded in the OAuth auth URL must match
	// the registered callback route at /api/email/oauth/{slug}/callback.
	// If it drifts, the OAuth provider will reject the callback as
	// redirect_uri mismatch.
	h, db := newEmailProviderTestHandler(t)
	uid := seedEmailTestUser(t, db, "bob")
	chID := seedEmailChannel(t, db, "billing", uid)
	_ = seedEmailProvider(t, db, "ms-billing", "microsoft", true)

	rec := httptest.NewRecorder()
	h.StartEmailOAuth(rec, emailStartReq(chID, "ms-billing", uid))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 — body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	authURL, err := url.Parse(resp["auth_url"])
	if err != nil {
		t.Fatalf("parse auth_url: %v", err)
	}
	gotRedirect := authURL.Query().Get("redirect_uri")
	wantRedirect := "http://test.local/api/email/oauth/ms-billing/callback"
	if gotRedirect != wantRedirect {
		t.Errorf("redirect_uri: got %q want %q — handler and route are misaligned", gotRedirect, wantRedirect)
	}
}

func TestStartEmailOAuth_NonNumericChannelID_Returns400(t *testing.T) {
	h, _ := newEmailProviderTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/channels/notanumber/email-providers/ms-main/oauth/start", nil)
	req.SetPathValue("channel_id", "notanumber")
	req.SetPathValue("slug", "ms-main")
	ctx := context.WithValue(req.Context(), contextkeys.User, &models.User{ID: 1})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.StartEmailOAuth(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 — non-numeric channel_id must be rejected", rec.Code)
	}
}

func TestStartEmailOAuth_NoUserInContext_Returns401(t *testing.T) {
	h, db := newEmailProviderTestHandler(t)
	chID := seedEmailChannel(t, db, "anon", 0)
	_ = seedEmailProvider(t, db, "ms-anon", "microsoft", true)

	rec := httptest.NewRecorder()
	h.StartEmailOAuth(rec, emailStartReq(chID, "ms-anon", 0)) // userID=0 → no context user

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401 — missing user context must 401", rec.Code)
	}
}

func TestStartEmailOAuth_MissingSlug_Returns400(t *testing.T) {
	h, db := newEmailProviderTestHandler(t)
	uid := seedEmailTestUser(t, db, "carol")
	chID := seedEmailChannel(t, db, "ch-missing-slug", 0)

	req := httptest.NewRequest(http.MethodPost, "/api/channels/"+strconv.Itoa(chID)+"/email-providers//oauth/start", nil)
	req.SetPathValue("channel_id", strconv.Itoa(chID))
	req.SetPathValue("slug", "") // explicit empty slug
	ctx := context.WithValue(req.Context(), contextkeys.User, &models.User{ID: uid})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.StartEmailOAuth(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 — empty slug must be rejected", rec.Code)
	}
}

func TestStartEmailOAuth_DisabledProvider_Returns404(t *testing.T) {
	h, db := newEmailProviderTestHandler(t)
	uid := seedEmailTestUser(t, db, "dave")
	chID := seedEmailChannel(t, db, "ch-disabled", uid)
	_ = seedEmailProvider(t, db, "ms-disabled", "microsoft", false) // is_enabled = false

	rec := httptest.NewRecorder()
	h.StartEmailOAuth(rec, emailStartReq(chID, "ms-disabled", uid))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404 — disabled provider must be invisible", rec.Code)
	}
	// auth_url payload must not contain a Microsoft auth URL leak.
	if strings.Contains(rec.Body.String(), "login.microsoftonline.com") {
		t.Errorf("body leaked Microsoft auth URL despite disabled provider: %s", rec.Body.String())
	}
}
