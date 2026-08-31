package utils

import (
	"net"
	"testing"
)

func TestIsBlockedSSRFAddrWithAllowedCIDRs(t *testing.T) {
	SetAllowLocalConnections(false)
	defer SetAllowLocalConnections(true)

	cidrs, err := ParseCIDRList("100.64.0.0/10,10.2.3.4")
	if err != nil {
		t.Fatalf("ParseCIDRList: %v", err)
	}

	cases := []struct {
		ip      string
		blocked bool
	}{
		{"100.95.24.194", false}, // explicitly allowed WireGuard / CGNAT range
		{"10.2.3.4", false},      // bare IP parsed as /32
		{"10.2.3.5", true},
		{"127.0.0.1", true},       // loopback cannot be allowlisted
		{"169.254.169.254", true}, // link-local metadata cannot be allowlisted
		{"8.8.8.8", false},
	}

	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("invalid test IP %q", tc.ip)
			}
			got := IsBlockedSSRFAddrWithAllowedCIDRs(ip, cidrs)
			if got != tc.blocked {
				t.Errorf("IsBlockedSSRFAddrWithAllowedCIDRs(%s) = %v, want %v", tc.ip, got, tc.blocked)
			}
		})
	}
}

func TestParseCIDRListRejectsInvalidInput(t *testing.T) {
	if _, err := ParseCIDRList("100.64.0.0/10,not-a-cidr"); err == nil {
		t.Fatal("expected ParseCIDRList to reject invalid input")
	}
}
