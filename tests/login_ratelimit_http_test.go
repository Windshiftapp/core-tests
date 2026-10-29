package tests

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/server"
)

// TestLoginLockoutNotBypassedByClientControlledXForwardedFor is the regression
// test for WI-1854: with proxy mode enabled, a client that rotates the
// X-Forwarded-For header must still be locked out after a few failed logins.
// Before the fix the extractor took the first (client-supplied) entry, so every
// request landed in a fresh lockout bucket and no 429 was ever returned.
func TestLoginLockoutNotBypassedByClientControlledXForwardedFor(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cases := []struct {
		name string
		// forwarded builds the X-Forwarded-For value for attempt i.
		forwarded func(i int) string
	}{
		{
			name: "trusted-range spoof falls back to the socket peer",
			forwarded: func(i int) string {
				return fmt.Sprintf("10.9.%d.%d", (i>>8)&255, i&255)
			},
		},
		{
			name: "client-supplied entry ahead of the proxy-appended real client",
			forwarded: func(i int) string {
				// The trusted proxy appends the real client (203.0.113.7) after
				// the attacker's rotating leading entry.
				return fmt.Sprintf("10.9.%d.%d, 203.0.113.7", (i>>8)&255, i&255)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testServer, cleanup := StartTestServerWithConfig(t, GetDBType(), func(cfg *server.Config) {
				cfg.UseProxy = true
			})
			defer cleanup()
			CreateBearerToken(t, testServer)

			sawLockout := false
			for i := 0; i < 12; i++ {
				resp := makeRequest(t, http.MethodPost, testServer.APIBase+"/auth/login", "", map[string]string{
					"email_or_username": "admin",
					"password":          fmt.Sprintf("wrong-%d", i),
				}, map[string]string{"X-Forwarded-For": tc.forwarded(i)})
				status := resp.StatusCode
				resp.Body.Close()
				if status == http.StatusTooManyRequests {
					sawLockout = true
					break
				}
			}
			if !sawLockout {
				t.Fatal("rotating X-Forwarded-For bypassed the login lockout; no 429 within 12 attempts")
			}
		})
	}
}
