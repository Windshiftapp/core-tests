package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/utils"
)

func TestNewClientUsesSSRFSafeHTTPClient(t *testing.T) {
	utils.SetAllowLocalConnections(false)
	defer utils.SetAllowLocalConnections(true)

	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := NewClient(Config{Endpoint: ts.URL, Timeout: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := client.Health(ctx)
	if err == nil {
		t.Fatal("Health() succeeded for loopback endpoint with local connections disabled")
	}
	if !errors.Is(err, utils.ErrBlockedSSRFAddr) {
		t.Fatalf("Health() error = %v, want ErrBlockedSSRFAddr", err)
	}
	if called {
		t.Fatal("loopback test server was reached; expected SSRF-safe dialer to block before connect")
	}
}
