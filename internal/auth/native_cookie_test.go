package auth_test

import (
	"net/http"
	"testing"

	"windshift/internal/auth"
)

// TestEncodeSessionCookieValueRoundTrips proves the load-bearing invariant of
// the native SSO handoff (WI-446): the value /api/auth/native/exchange returns
// — which the desktop writes verbatim into its webview cookie store — decodes
// back to the original session token under the canonical cookie name. If this
// ever diverged, desktop SSO sessions would silently fail to authenticate.
func TestEncodeSessionCookieValueRoundTrips(t *testing.T) {
	t.Parallel()

	// Deterministic keys via a fixed secret so the encoder/decoder agree.
	sm := auth.NewSessionManagerWithValidationCacheTTL(nil, false, false, nil, "test-cookie-secret", "strict", auth.DefaultSessionValidationCacheTTL)

	const token = "session-token-deadbeef"
	encoded, err := sm.EncodeSessionCookieValue(token)
	if err != nil {
		t.Fatalf("EncodeSessionCookieValue: %v", err)
	}
	if encoded == "" || encoded == token {
		t.Fatalf("expected an encoded (signed/encrypted) value, got %q", encoded)
	}

	// Replay it the way a webview would: as the windshift_session cookie.
	req := &http.Request{Header: http.Header{}}
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: encoded})

	decoded, err := sm.GetSessionFromCookie(req)
	if err != nil {
		t.Fatalf("GetSessionFromCookie: %v", err)
	}
	if decoded != token {
		t.Errorf("round-trip token = %q, want %q", decoded, token)
	}

	// A tampered value must not decode.
	tampered := &http.Request{Header: http.Header{}}
	tampered.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: encoded + "x"})
	if _, err := sm.GetSessionFromCookie(tampered); err == nil {
		t.Error("expected tampered cookie to fail decoding, got nil error")
	}
}
