package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// loginRequest builds a login request whose socket peer is a private (trusted)
// proxy and whose leading X-Forwarded-For entry is attacker-chosen. The trusted
// proxy appends the real client (203.0.113.7) on the right.
func loginRequest(spoofedXFF string) *http.Request {
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "10.0.0.2:54321"
	r.Header.Set("X-Forwarded-For", spoofedXFF+", 203.0.113.7")
	return r
}

func TestRateLimiterGetClientIPStableAcrossRotatingXFF(t *testing.T) {
	rl := NewRateLimiter(100, 100, true, nil)
	defer rl.Stop()

	for _, spoofed := range []string{"10.9.0.1", "10.9.0.2", "10.9.0.3"} {
		if got := rl.getClientIP(loginRequest(spoofed)); got != "203.0.113.7" {
			t.Fatalf("getClientIP() with spoofed %q = %q, want real client 203.0.113.7", spoofed, got)
		}
	}
}

func TestRateLimiterAllowRequestKeyedOnRealClientDespiteRotatingXFF(t *testing.T) {
	rl := NewRateLimiter(1, 1, true, nil)
	defer rl.Stop()

	if !rl.AllowRequest(loginRequest("10.9.0.1")) {
		t.Fatal("first request for the real client should be allowed")
	}
	if rl.AllowRequest(loginRequest("10.9.0.2")) {
		t.Fatal("rotating X-Forwarded-For bypassed the per-IP rate limit")
	}
}
