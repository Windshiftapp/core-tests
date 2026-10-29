package jira

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/time/rate"
)

func TestDataCenterConnectionErrorPreservesDiagnosticAfterHTMLBoilerplate(t *testing.T) {
	t.Parallel()

	body := `<!DOCTYPE html>
<html lang="en">
<head>
  <title>Example Gateway Error</title>
  <script>window.location = "https://example.invalid";</script>
  <style>` + strings.Repeat(".unused { color: #fff; }", 100) + `</style>
</head>
<body>
  <h1>Request rejected</h1>
  <p>The requested API endpoint was rejected by the example gateway.</p>
  <p>Reference: EXAMPLE-123456</p>
</body>
</html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	client := &dataCenterClient{
		baseURL:    server.URL + "/rest/api/2",
		authHeader: "Bearer data-center-pat",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}

	_, err := client.TestConnection(context.Background())
	want := "invalid Jira credentials (jira said: Example Gateway Error Request rejected " +
		"The requested API endpoint was rejected by the example gateway. " +
		"Reference: EXAMPLE-123456)"

	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("TestConnection() error = %v, want ErrInvalidCredentials", err)
	}
	if err.Error() != want {
		t.Fatalf("TestConnection() error = %q, want %q", err, want)
	}
	if strings.Contains(err.Error(), "truncated") {
		t.Fatalf("TestConnection() unexpectedly truncated the diagnostic: %q", err)
	}
}
