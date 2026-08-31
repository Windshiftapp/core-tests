//go:build test

package metrics

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

func TestHandlerExposesRuntimeHTTPAndSCMMetrics(t *testing.T) {
	metrics := New(nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	cloneRequest := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.Clone(r.Context()))
		})
	}
	instrumented := metrics.Instrument(cloneRequest(metrics.CaptureRoutePattern(mux)))
	instrumented.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/items/42", nil),
	)
	metrics.ObserveSCMPoll("repository_sync", 2*time.Second, nil)
	metrics.ObserveSCMPoll("repository_sync", 3*time.Second, errors.New("provider unavailable"))

	body := scrape(t, metrics.Handler())
	assertMetricContains(t, body, "go_goroutines ")
	assertMetricContains(t, body, `windshift_http_requests_total{method="GET",route="/items/{id}",status_code="201"} 1`)
	assertMetricContains(t, body, `windshift_http_request_duration_seconds_count{method="GET",route="/items/{id}",status_code="201"} 1`)
	assertMetricContains(t, body, `windshift_scm_polls_total{operation="repository_sync",outcome="success"} 1`)
	assertMetricContains(t, body, `windshift_scm_polls_total{operation="repository_sync",outcome="failure"} 1`)
	assertMetricContains(t, body, `windshift_scm_poll_duration_seconds_sum{operation="repository_sync"} 5`)
	if strings.Contains(body, "/items/42") {
		t.Fatal("HTTP metrics contain a raw request path")
	}
}

func TestHandlerExposesDatabaseBackedWindshiftMetrics(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (42, 'Metrics', 'MET', TRUE)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_runs(workspace_id, status, started_at, ended_at)
		VALUES
			(42, ?, NULL, NULL),
			(42, ?, CURRENT_TIMESTAMP, NULL),
			(42, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
			(42, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, models.AgentRunStatusQueued, models.AgentRunStatusRunning, models.AgentRunStatusSucceeded, models.AgentRunStatusFailed); err != nil {
		t.Fatalf("seed agent runs: %v", err)
	}
	channelID := testutils.InsertID(t, db, `
		INSERT INTO channels(name, type, direction, status)
		VALUES ('Metrics webhook', 'webhook', 'outbound', 'enabled')
	`)
	if _, err := db.Exec(`
		INSERT INTO webhook_deliveries(channel_id, event_type, attempt_type, transport, success)
		VALUES (?, 'item.updated', 'automatic', 'http', TRUE),
		       (?, 'item.updated', 'automatic', 'http', FALSE)
	`, channelID, channelID); err != nil {
		t.Fatalf("seed webhook deliveries: %v", err)
	}

	metrics := New(db)
	body := scrape(t, metrics.Handler())
	assertMetricContains(t, body, `go_sql_open_connections{db_name="windshift"}`)
	assertMetricContains(t, body, "windshift_agent_run_queue_depth 1")
	assertMetricContains(t, body, "windshift_agent_runs_in_flight 1")
	assertMetricContains(t, body, `windshift_agent_run_outcomes{outcome="succeeded"} 1`)
	assertMetricContains(t, body, `windshift_agent_run_outcomes{outcome="failed"} 1`)
	assertMetricContains(t, body, `windshift_agent_run_duration_average_seconds{outcome="succeeded"} 0`)
	assertMetricContains(t, body, `windshift_webhook_dispatches{outcome="success"} 1`)
	assertMetricContains(t, body, `windshift_webhook_dispatches{outcome="failure"} 1`)
}

func scrape(t *testing.T, handler http.Handler) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d; body: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain;") {
		t.Fatalf("Content-Type = %q, want Prometheus text format", got)
	}
	return recorder.Body.String()
}

func assertMetricContains(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Fatalf("metrics do not contain %q\n%s", want, body)
	}
}
