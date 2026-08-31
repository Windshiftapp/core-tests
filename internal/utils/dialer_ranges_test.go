package utils

import (
	"net"
	"testing"
)

func TestIsBlockedSSRFAddrRanges(t *testing.T) {
	SetAllowLocalConnections(false)
	defer SetAllowLocalConnections(true)

	tests := []struct {
		address string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.0.1", true},
		{"169.254.169.254", true},
		{"100.64.0.1", true},
		{"0.0.0.0", true},
		{"224.0.0.1", true},
		{"::1", true},
		{"fe80::1", true},
		{"ff02::1", true},
		{"::127.0.0.1", true},
		{"2002:0a00:0001::", true},
		{"64:ff9b::a9fe:a9fe", true},
		{"8.8.8.8", false},
		{"2606:4700:4700::1111", false},
	}
	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			if got := IsBlockedSSRFAddr(net.ParseIP(test.address)); got != test.blocked {
				t.Fatalf("IsBlockedSSRFAddr(%s) = %v, want %v", test.address, got, test.blocked)
			}
		})
	}
}
