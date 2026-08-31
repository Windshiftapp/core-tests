//go:build test

package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/auth"
	"windshift/internal/restapi"
	"windshift/internal/testutils"
	"windshift/internal/utils"
)

const refreshSessionTestIP = "198.51.100.30"

func TestRefreshSessionRejectsExpiredSession(t *testing.T) {
	db, _, handler, session, cookie := newRefreshSessionFixture(t)

	// The API cannot create an expired active session, so set the timestamp directly.
	expiredAt := time.Now().UTC().Add(-time.Hour)
	if _, err := db.ExecWrite(`UPDATE user_sessions SET expires_at = ? WHERE id = ?`, expiredAt, session.ID); err != nil {
		t.Fatalf("expire session: %v", err)
	}

	response := performSessionRefresh(t, handler, cookie, false)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusUnauthorized, response.Body.String())
	}
	var apiError restapi.ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if apiError.Code != restapi.ErrCodeUnauthorized || apiError.Error != "Authentication required" {
		t.Fatalf("error = %+v, want UNAUTHORIZED authentication error", apiError)
	}

	var persistedExpiry time.Time
	var persistedActive bool
	if err := db.QueryRow(`SELECT expires_at, is_active FROM user_sessions WHERE id = ?`, session.ID).Scan(&persistedExpiry, &persistedActive); err != nil {
		t.Fatalf("read session expiry: %v", err)
	}
	if persistedExpiry.After(expiredAt.Add(time.Second)) {
		t.Fatalf("expired session was extended from %v to %v", expiredAt, persistedExpiry)
	}
	if persistedActive {
		t.Fatal("expired session remained active")
	}

	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != auth.SessionCookieName || cookies[0].MaxAge != -1 {
		t.Fatalf("response cookies = %+v, want cleared session cookie", cookies)
	}
}

func TestRefreshSessionExtendsActiveSession(t *testing.T) {
	db, _, handler, session, cookie := newRefreshSessionFixture(t)

	before := time.Now().UTC()
	response := performSessionRefresh(t, handler, cookie, false)
	after := time.Now().UTC()
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}

	var persistedExpiry time.Time
	if err := db.QueryRow(`SELECT expires_at FROM user_sessions WHERE id = ?`, session.ID).Scan(&persistedExpiry); err != nil {
		t.Fatalf("read session expiry: %v", err)
	}
	if persistedExpiry.Before(before.Add(auth.DefaultSessionDuration)) || persistedExpiry.After(after.Add(auth.DefaultSessionDuration)) {
		t.Fatalf("expiry = %v, want refresh time + %v", persistedExpiry, auth.DefaultSessionDuration)
	}

	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != auth.SessionCookieName || cookies[0].MaxAge != int(auth.DefaultSessionDuration.Seconds()) {
		t.Fatalf("response cookies = %+v, want renewed session cookie", cookies)
	}
}

func newRefreshSessionFixture(t *testing.T) (*testutils.TestDB, *auth.SessionManager, *AuthHandler, *auth.Session, *http.Cookie) {
	t.Helper()
	db := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = db.Close() })

	var userID int
	if err := db.QueryRow(`SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("find fixture user: %v", err)
	}
	manager := auth.NewSessionManager(db, false, false, nil, "auth-refresh-test-secret", "strict")
	session, err := manager.CreateSession(userID, refreshSessionTestIP, "test-agent", false)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	handler := NewAuthHandler(nil, nil, nil, manager, nil, nil, nil, utils.NewIPExtractor(false, nil), nil, nil)

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = refreshSessionTestIP + ":4567"
	response := httptest.NewRecorder()
	if err := manager.SetSessionCookie(response, request, session.Token, false); err != nil {
		t.Fatalf("SetSessionCookie: %v", err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("session cookies = %d, want 1", len(cookies))
	}
	return db, manager, handler, session, cookies[0]
}

func performSessionRefresh(t *testing.T, handler *AuthHandler, cookie *http.Cookie, rememberMe bool) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]bool{"remember_me": rememberMe})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", bytes.NewReader(body))
	request.RemoteAddr = refreshSessionTestIP + ":4567"
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.RefreshSession(response, request)
	return response
}
