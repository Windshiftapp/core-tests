package tests

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMetricsEndpointIsPublicAndUsesRoutePatterns(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())

	healthResponse := makeAnonymousRequest(t, server, http.MethodGet, "/healthz")
	_, _ = io.Copy(io.Discard, healthResponse.Body)
	_ = healthResponse.Body.Close()
	if healthResponse.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want %d", healthResponse.StatusCode, http.StatusOK)
	}

	response := makeAnonymousRequest(t, server, http.MethodGet, "/metrics")
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read metrics body: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d; body: %s", response.StatusCode, http.StatusOK, body)
	}
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/plain;") {
		t.Fatalf("Content-Type = %q, want Prometheus text format", got)
	}
	text := string(body)
	for _, metric := range []string{
		"go_goroutines ",
		"windshift_process_open_fds ",
		`go_sql_open_connections{db_name="windshift"}`,
		`windshift_http_requests_total{method="GET",route="/healthz",status_code="200"} 1`,
		"windshift_agent_run_queue_depth 0",
		"windshift_agent_runs_in_flight 0",
	} {
		if !strings.Contains(text, metric) {
			t.Fatalf("metrics do not contain %q\n%s", metric, text)
		}
	}
}
