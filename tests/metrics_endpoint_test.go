package tests

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"windshift/internal/server"
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

func TestMetricsEndpointRequiresBearerTokenWhenConfigured(t *testing.T) {
	const token = "metrics-scrape-token"
	testServer, _ := StartTestServerWithConfig(t, GetDBType(), func(cfg *server.Config) {
		cfg.MetricsToken = token
	})

	unauthorized := makeRequest(t, http.MethodGet, testServer.BaseURL+"/metrics", "", nil, nil)
	defer unauthorized.Body.Close()
	body, err := io.ReadAll(unauthorized.Body)
	if err != nil {
		t.Fatalf("read unauthorized metrics body: %v", err)
	}
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("metrics without token status = %d, want %d; body: %s", unauthorized.StatusCode, http.StatusUnauthorized, body)
	}
	if got := unauthorized.Header.Get("WWW-Authenticate"); got != `Bearer realm="metrics"` {
		t.Fatalf("WWW-Authenticate = %q, want bearer challenge", got)
	}
	if strings.Contains(string(body), token) {
		t.Fatal("unauthorized response leaked the configured metrics token")
	}

	wrong := makeRequest(t, http.MethodGet, testServer.BaseURL+"/metrics", "wrong-token", nil, nil)
	defer wrong.Body.Close()
	_, _ = io.Copy(io.Discard, wrong.Body)
	if wrong.StatusCode != http.StatusUnauthorized {
		t.Fatalf("metrics with wrong token status = %d, want %d", wrong.StatusCode, http.StatusUnauthorized)
	}

	authorized := makeRequest(t, http.MethodGet, testServer.BaseURL+"/metrics", token, nil, nil)
	defer authorized.Body.Close()
	authorizedBody, err := io.ReadAll(authorized.Body)
	if err != nil {
		t.Fatalf("read authorized metrics body: %v", err)
	}
	if authorized.StatusCode != http.StatusOK {
		t.Fatalf("metrics with token status = %d, want %d; body: %s", authorized.StatusCode, http.StatusOK, authorizedBody)
	}
	if !strings.Contains(string(authorizedBody), "windshift_build_info") {
		t.Fatalf("authorized metrics body missing windshift_build_info\n%s", authorizedBody)
	}
}
