//go:build test

package utils

import (
	"net"
	"testing"
)

// TestIsPrivateIPEncodingSafe guards the encoding-safety of IsPrivateIP: an
// IPv4 loopback/RFC1918/link-local target embedded in a transitional IPv6 form
// (IPv4-compatible ::a.b.c.d, 6to4 2002::/16, NAT64 64:ff9b::/96) must be
// recognized as private. Go's net.IP predicates only normalize native and
// IPv4-mapped (::ffff:) addresses, so without unwrapping these forms a
// server-side client could be steered at e.g. ::127.0.0.1 or 2002:0a00:0001::.
func TestIsPrivateIPEncodingSafe(t *testing.T) {
	cases := []struct {
		ip      string
		private bool
	}{
		// Baselines net.IP already handles.
		{"127.0.0.1", true},
		{"::1", true},
		{"10.0.0.1", true},
		{"169.254.169.254", true},
		{"::ffff:127.0.0.1", true}, // IPv4-mapped loopback
		{"::ffff:10.0.0.1", true},  // IPv4-mapped RFC1918

		// Encoded forms that the old check missed.
		{"::127.0.0.1", true},      // IPv4-compatible loopback
		{"::10.0.0.1", true},       // IPv4-compatible RFC1918
		{"::a9fe:a9fe", true},      // IPv4-compatible 169.254.169.254 (metadata)
		{"2002:7f00:0001::", true}, // 6to4 wrapping 127.0.0.1
		{"2002:0a00:0001::", true}, // 6to4 wrapping 10.0.0.1
		{"64:ff9b::7f00:1", true},  // NAT64 wrapping 127.0.0.1
		{"64:ff9b::a00:1", true},   // NAT64 wrapping 10.0.0.1

		// Must stay non-private: public targets, including transitional forms
		// that wrap a public IPv4, the unspecified address, and public IPv6.
		{"8.8.8.8", false},
		{"2002:0808:0808::", false},     // 6to4 wrapping 8.8.8.8
		{"64:ff9b::808:808", false},     // NAT64 wrapping 8.8.8.8
		{"2001:4860:4860::8888", false}, // public IPv6
		{"::", false},                   // unspecified (not "private")
	}

	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("invalid test IP %q", tc.ip)
			}
			if got := IsPrivateIP(ip); got != tc.private {
				t.Errorf("IsPrivateIP(%s) = %v, want %v", tc.ip, got, tc.private)
			}
		})
	}
}

// TestIsBlockedSSRFAddrEncodingSafe is the SSRF-dialer counterpart: the same
// embedded-IPv4 forms wrapping a blocked range must be blocked, while forms
// wrapping a public IPv4 stay allowed.
func TestIsBlockedSSRFAddrEncodingSafe(t *testing.T) {
	SetAllowLocalConnections(false)
	defer SetAllowLocalConnections(true)

	cases := []struct {
		ip      string
		blocked bool
	}{
		{"::127.0.0.1", true},       // IPv4-compatible loopback
		{"::a9fe:a9fe", true},       // IPv4-compatible metadata (link-local)
		{"2002:0a00:0001::", true},  // 6to4 RFC1918
		{"2002:6440:0001::", true},  // 6to4 CGNAT (100.64.0.1)
		{"64:ff9b::7f00:1", true},   // NAT64 loopback
		{"2002:0808:0808::", false}, // 6to4 public
		{"64:ff9b::808:808", false}, // NAT64 public
	}

	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("invalid test IP %q", tc.ip)
			}
			if got := IsBlockedSSRFAddr(ip); got != tc.blocked {
				t.Errorf("IsBlockedSSRFAddr(%s) = %v, want %v", tc.ip, got, tc.blocked)
			}
		})
	}
}
