//go:build test

package utils

import (
	"context"
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
