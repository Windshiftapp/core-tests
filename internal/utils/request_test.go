package utils

import (
	"context"
	"net"
	"net/http/httptest"
	"testing"

	"windshift/internal/contextkeys"
)

func TestGetClientIPPrefersValidatedContextValue(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "198.51.100.1:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.99")
	r = r.WithContext(context.WithValue(r.Context(), contextkeys.ClientIP, "192.0.2.10"))

	if got := GetClientIP(r); got != "192.0.2.10" {
		t.Fatalf("GetClientIP() = %q, want context client IP", got)
	}
}

func TestForwardedClientIPPrefersRightmostUntrustedHop(t *testing.T) {
	tests := []struct {
		name      string
		forwarded string
		trusted   map[string]bool
		want      string
	}{
		{
			name:      "client supplied leading entries are ignored",
			forwarded: "203.0.113.9, 198.51.100.7",
			want:      "198.51.100.7",
		},
		{
			name:      "trusted proxy hops are skipped",
			forwarded: "203.0.113.9, 10.0.0.5",
			trusted:   map[string]bool{"10.0.0.5": true},
			want:      "203.0.113.9",
		},
		{
			name:      "no untrusted hop when every hop is trusted",
			forwarded: "10.0.0.9, 10.0.0.5",
			trusted:   map[string]bool{"10.0.0.9": true, "10.0.0.5": true},
			want:      "",
		},
		{
			name:      "malformed entries are ignored",
			forwarded: "198.51.100.7, not-an-ip",
			want:      "198.51.100.7",
		},
		{
			name:      "unspecified entries are ignored",
			forwarded: "198.51.100.7, 0.0.0.0",
			want:      "198.51.100.7",
		},
		{
			name:      "empty header",
			forwarded: "",
			want:      "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isTrusted := func(ip net.IP) bool { return tc.trusted[ip.String()] }
			if got := ForwardedClientIP(tc.forwarded, isTrusted); got != tc.want {
				t.Fatalf("ForwardedClientIP(%q) = %q, want %q", tc.forwarded, got, tc.want)
			}
		})
	}
}

func TestExtractClientIPIgnoresForwardedHeadersFromUntrustedPeer(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "198.51.100.10:54321"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	r.Header.Set("X-Real-IP", "203.0.113.8")

	if got := ExtractClientIP(r, true, nil); got != "198.51.100.10" {
		t.Fatalf("ExtractClientIP() = %q, want socket peer 198.51.100.10", got)
	}
}

func TestExtractClientIPReturnsRealClientBehindTrustedProxy(t *testing.T) {
	// The attacker rotates the leading X-Forwarded-For entry; the trusted proxy
	// appends its own observed peer (the real client) on the right. Every
	// rotation must resolve to the same real client.
	for _, spoofed := range []string{"10.9.0.1", "10.9.0.2", "10.9.0.3"} {
		r := httptest.NewRequest("POST", "/api/auth/login", nil)
		r.RemoteAddr = "10.0.0.2:54321" // private peer is a trusted proxy in proxy mode
		r.Header.Set("X-Forwarded-For", spoofed+", 203.0.113.7")

		if got := ExtractClientIP(r, true, nil); got != "203.0.113.7" {
			t.Fatalf("ExtractClientIP() with spoofed %q = %q, want real client 203.0.113.7", spoofed, got)
		}
	}
}

func TestExtractClientIPSkipsTrustedInternalHops(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "10.0.0.2:54321"
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.9") // 10.0.0.9 is an internal load balancer

	if got := ExtractClientIP(r, true, nil); got != "203.0.113.7" {
		t.Fatalf("ExtractClientIP() = %q, want real client 203.0.113.7", got)
	}
}

func TestExtractClientIPFallsBackToSocketPeerWhenEveryHopIsTrusted(t *testing.T) {
	// A client can only inject trusted-range (private) entries, which are
	// indistinguishable from proxies, so the header cannot attribute a client
	// and the socket peer is used instead. Rotating the injected value must not
	// change the result.
	for _, spoofed := range []string{"10.9.0.1", "10.9.0.2", "10.9.0.3"} {
		r := httptest.NewRequest("POST", "/api/auth/login", nil)
		r.RemoteAddr = "10.0.0.2:54321"
		r.Header.Set("X-Forwarded-For", spoofed)

		if got := ExtractClientIP(r, true, nil); got != "10.0.0.2" {
			t.Fatalf("ExtractClientIP() with spoofed %q = %q, want socket peer 10.0.0.2", spoofed, got)
		}
	}
}

func TestExtractClientIPUsesXRealIPOnlyFromTrustedPeer(t *testing.T) {
	trusted := httptest.NewRequest("POST", "/api/auth/login", nil)
	trusted.RemoteAddr = "10.0.0.2:54321"
	trusted.Header.Set("X-Real-IP", "203.0.113.7")
	if got := ExtractClientIP(trusted, true, nil); got != "203.0.113.7" {
		t.Fatalf("trusted peer X-Real-IP = %q, want 203.0.113.7", got)
	}

	untrusted := httptest.NewRequest("POST", "/api/auth/login", nil)
	untrusted.RemoteAddr = "198.51.100.10:54321"
	untrusted.Header.Set("X-Real-IP", "203.0.113.7")
	if got := ExtractClientIP(untrusted, true, nil); got != "198.51.100.10" {
		t.Fatalf("untrusted peer X-Real-IP = %q, want socket peer 198.51.100.10", got)
	}
}

func TestExtractClientIPIgnoresTrustedProxyXRealIP(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "10.0.0.2:54321"
	r.Header.Set("X-Real-IP", "10.9.0.1") // private/trusted-range value, not attributable

	if got := ExtractClientIP(r, true, nil); got != "10.0.0.2" {
		t.Fatalf("ExtractClientIP() with trusted-range X-Real-IP = %q, want socket peer 10.0.0.2", got)
	}
}

func TestExtractClientIPHonorsAdditionalProxies(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "203.0.113.200:54321" // public peer, trusted only via the additional list
	r.Header.Set("X-Forwarded-For", "198.51.100.7")

	if got := ExtractClientIP(r, true, []net.IP{net.ParseIP("203.0.113.200")}); got != "198.51.100.7" {
		t.Fatalf("ExtractClientIP() with additional proxy = %q, want 198.51.100.7", got)
	}
	if got := ExtractClientIP(r, true, nil); got != "203.0.113.200" {
		t.Fatalf("ExtractClientIP() without additional proxy = %q, want socket peer 203.0.113.200", got)
	}
}

func TestExtractClientIPIgnoresProxyHeadersWhenProxyModeDisabled(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "10.0.0.2:54321"
	r.Header.Set("X-Forwarded-For", "203.0.113.7")

	if got := ExtractClientIP(r, false, nil); got != "10.0.0.2" {
		t.Fatalf("ExtractClientIP() with proxy disabled = %q, want socket peer 10.0.0.2", got)
	}
}
