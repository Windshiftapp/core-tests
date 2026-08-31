package utils

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestIsBlockedSSRFAddr(t *testing.T) {
	SetAllowLocalConnections(false)
	defer SetAllowLocalConnections(true)

	cases := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},             // loopback
		{"::1", true},                   // IPv6 loopback
		{"0.0.0.0", true},               // unspecified
		{"169.254.169.254", true},       // link-local (cloud metadata)
		{"10.0.0.1", true},              // RFC1918
		{"172.16.0.1", true},            // RFC1918
		{"192.168.1.1", true},           // RFC1918
		{"100.64.0.1", true},            // CGNAT
		{"100.127.255.255", true},       // CGNAT upper
		{"224.0.0.1", true},             // multicast
		{"fe80::1", true},               // IPv6 link-local
		{"fc00::1", true},               // IPv6 unique local
		{"8.8.8.8", false},              // public
		{"1.1.1.1", false},              // public
		{"100.63.255.255", false},       // just below CGNAT
		{"100.128.0.0", false},          // just above CGNAT
		{"2001:4860:4860::8888", false}, // public IPv6
	}

	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("invalid test IP %q", tc.ip)
			}
			got := IsBlockedSSRFAddr(ip)
			if got != tc.blocked {
				t.Errorf("IsBlockedSSRFAddr(%s) = %v, want %v", tc.ip, got, tc.blocked)
			}
		})
	}
}

func TestSafeNetDialer_RefusesLoopback(t *testing.T) {
	SetAllowLocalConnections(false)
	defer SetAllowLocalConnections(true)

	// Listen on 127.0.0.1 so the address is concrete and reachable in the
	// absence of the SSRF guard. The guard should reject the dial before TCP.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	dialer := SafeNetDialer(2 * time.Second)
	_, err = dialer.Dial("tcp", ln.Addr().String())
	if err == nil {
		t.Fatal("expected SafeNetDialer to refuse loopback address, got nil error")
	}
	if !errors.Is(err, ErrBlockedSSRFAddr) && !strings.Contains(err.Error(), "blocked IP range") {
		t.Errorf("expected ErrBlockedSSRFAddr, got %v", err)
	}
}

func TestSafeNetDialer_RefusesPrivateLiteral(t *testing.T) {
	SetAllowLocalConnections(false)
	defer SetAllowLocalConnections(true)

	// Use a literal RFC1918 address. The dial should fail before TCP either
	// way (no listener), but with the guard it fails with a blocked-IP error
	// rather than connection refused / timeout. We assert the *error type*,
	// not the wall-clock timing, which is what makes this test deterministic.
	dialer := SafeNetDialer(2 * time.Second)
	_, err := dialer.Dial("tcp", "10.0.0.1:1")
	if err == nil {
		t.Fatal("expected error dialing 10.0.0.1, got nil")
	}
	if !errors.Is(err, ErrBlockedSSRFAddr) && !strings.Contains(err.Error(), "blocked IP range") {
		t.Errorf("expected ErrBlockedSSRFAddr, got %v", err)
	}
}

func TestAllowLocalConnections_DefaultsToAllowAndCanBeDisabled(t *testing.T) {
	if !AllowLocalConnections() {
		t.Fatal("AllowLocalConnections() = false, want true by default")
	}
	defer SetAllowLocalConnections(true) // never leak global state to siblings

	// Local and private destinations are permitted by default.
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1"} {
		if IsBlockedSSRFAddr(net.ParseIP(ip)) {
			t.Errorf("with local connections allowed, IsBlockedSSRFAddr(%s) = true, want false", ip)
		}
	}

	// Explicitly setting the switch to false blocks those destinations.
	SetAllowLocalConnections(false)
	if AllowLocalConnections() {
		t.Fatal("SetAllowLocalConnections(false) did not take effect")
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1"} {
		if !IsBlockedSSRFAddr(net.ParseIP(ip)) {
			t.Errorf("with local connections disallowed, IsBlockedSSRFAddr(%s) = false, want true", ip)
		}
	}

	// Re-enabling the setting allows a real loopback dial.
	SetAllowLocalConnections(true)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	conn, err := SafeNetDialer(2*time.Second).Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("with local connections allowed, loopback dial should succeed, got %v", err)
	}
	_ = conn.Close()

	// A malformed (nil) IP stays blocked regardless of the setting.
	if !IsBlockedSSRFAddr(nil) {
		t.Error("nil IP must remain blocked even with the switch on")
	}
}
