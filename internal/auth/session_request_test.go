package auth

import (
	"net/http/httptest"
	"testing"
)

func TestGetSessionFromRequestDoesNotAcceptBearerHeader(t *testing.T) {
	t.Parallel()

	cm := newCookieManager(false, false, nil, "test-cookie-secret", "test-hash", "test-block")
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer session-token")

	if token, err := cm.getSessionFromRequest(req, SessionCookieName); err == nil {
		t.Fatalf("getSessionFromRequest returned bearer token %q, want error", token)
	}
}
