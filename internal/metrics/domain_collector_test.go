//go:build test

package metrics

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"

	"github.com/prometheus/client_golang/prometheus"
)

// countingDomainDatabase counts SELECT queries the domain collector issues so
// tests can assert scrapes stay bounded.
type countingDomainDatabase struct {
	database.Database
	selectQueries   atomic.Int64
	queryRowQueries atomic.Int64
}

func (db *countingDomainDatabase) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db.selectQueries.Add(1)
	return db.Database.QueryContext(ctx, query, args...)
}

func (db *countingDomainDatabase) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	db.queryRowQueries.Add(1)
	return db.Database.QueryRowContext(ctx, query, args...)
}

// brokenDatabase fails every query; used to exercise refresh failures.
type brokenDatabase struct{}

func (brokenDatabase) Query(query string, args ...any) (*sql.Rows, error) {
	return nil, errors.New("database unavailable")
}
func (brokenDatabase) QueryRow(query string, args ...any) *sql.Row {
	panic("not implemented")
}
func (brokenDatabase) Exec(query string, args ...any) (sql.Result, error) {
	return nil, errors.New("database unavailable")
}
func (brokenDatabase) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return nil, errors.New("database unavailable")
}
func (brokenDatabase) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	panic("not implemented")
}
func (brokenDatabase) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return nil, errors.New("database unavailable")
}
func (brokenDatabase) ExecWrite(query string, args ...any) (sql.Result, error) {
	return nil, errors.New("database unavailable")
}
func (brokenDatabase) ExecWriteContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return nil, errors.New("database unavailable")
}
func (brokenDatabase) Begin() (database.Tx, error) { return nil, errors.New("database unavailable") }
func (brokenDatabase) BeginTx(ctx context.Context, opts *sql.TxOptions) (database.Tx, error) {
	return nil, errors.New("database unavailable")
}
func (brokenDatabase) Close() error          { return nil }
func (brokenDatabase) Initialize() error     { return nil }
func (brokenDatabase) GetDB() *sql.DB        { return nil }
func (brokenDatabase) GetDriverName() string { return "sqlite" }

// fakeClock drives the collector's refresh interval without sleeping.
type fakeClock struct {
	current atomic.Int64 // unix nanoseconds
}

func (c *fakeClock) Now() time.Time { return time.Unix(0, c.current.Load()) }

func (c *fakeClock) Advance(d time.Duration) { c.current.Add(int64(d)) }

func newFakeClock() *fakeClock {
	clock := &fakeClock{}
	clock.current.Store(time.Now().UnixNano())
	return clock
}

func newDomainCollectorForTest(t *testing.T, db database.Database, now *fakeClock) *domainCollector {
	t.Helper()
	collector := newDomainCollector(db).(*domainCollector)
	collector.refreshInterval = time.Minute
	collector.now = now.Now
	return collector
}

func seedDomainCollectorFixture(t *testing.T, db database.Database) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (42, 'Metrics', 'MET', TRUE)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_runs(workspace_id, status, started_at, ended_at)
		VALUES
			(42, ?, NULL, NULL),
			(42, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, models.AgentRunStatusQueued, models.AgentRunStatusSucceeded); err != nil {
		t.Fatalf("seed agent runs: %v", err)
	}
	channelID := testutils.InsertID(t, db, `
		INSERT INTO channels(name, type, direction, status)
		VALUES ('Metrics webhook', 'webhook', 'outbound', 'enabled')
	`)
	if _, err := db.Exec(`
		INSERT INTO webhook_deliveries(channel_id, event_type, attempt_type, transport, success)
		VALUES (?, 'item.updated', 'automatic', 'http', TRUE)
	`, channelID); err != nil {
		t.Fatalf("seed webhook delivery: %v", err)
	}
}

func insertQueuedAgentRun(t *testing.T, db database.Database) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO agent_runs(workspace_id, status) VALUES (42, ?)`, models.AgentRunStatusQueued); err != nil {
		t.Fatalf("insert agent run: %v", err)
	}
}

// gatherDomainMetrics collects the collector into a fresh registry and indexes
// gauge values by "name{label="value",...}".
func gatherDomainMetrics(t *testing.T, collector prometheus.Collector) map[string]float64 {
	t.Helper()
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	values := make(map[string]float64)
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			key := family.GetName()
			if labels := metric.GetLabel(); len(labels) > 0 {
				parts := make([]string, len(labels))
				for i, label := range labels {
					parts[i] = label.GetName() + `="` + label.GetValue() + `"`
				}
				key += "{" + strings.Join(parts, ",") + "}"
			}
			values[key] = metric.GetGauge().GetValue()
		}
	}
	return values
}

func assertDomainMetrics(t *testing.T, got map[string]float64, want map[string]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("collected %d metrics, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for key, wantValue := range want {
		gotValue, ok := got[key]
		if !ok {
			t.Fatalf("metric %q missing from collection\n%v", key, got)
		}
		if gotValue != wantValue {
			t.Fatalf("metric %s = %v, want %v", key, gotValue, wantValue)
		}
	}
}

func initialDomainMetricValues() map[string]float64 {
	return map[string]float64{
		`windshift_agent_run_duration_average_seconds{outcome="succeeded"}`: 0,
		`windshift_agent_run_duration_samples{outcome="succeeded"}`:         1,
		`windshift_agent_run_outcomes{outcome="succeeded"}`:                 1,
		`windshift_agent_run_outcomes{outcome="failed"}`:                    0,
		`windshift_agent_run_outcomes{outcome="canceled"}`:                  0,
		`windshift_agent_run_outcomes{outcome="killed"}`:                    0,
		"windshift_agent_run_queue_depth":                                   1,
		"windshift_agent_runs_in_flight":                                    0,
		`windshift_webhook_dispatches{outcome="success"}`:                   1,
		`windshift_webhook_dispatches{outcome="failure"}`:                   0,
	}
}

// A scrape must not re-run the full-table aggregates when the cached summary
// is still fresh; repeated scrapes are the steady state (every ~15-30s).
func TestDomainCollectorRepeatedScrapesServeCachedSummary(t *testing.T) {
	db := &countingDomainDatabase{Database: testutils.CreateTestDB(t, true)}
	seedDomainCollectorFixture(t, db)

	metrics := New(db, "")
	scrape(t, metrics.Handler())
	afterFirst := db.selectQueries.Load()
	if afterFirst == 0 {
		t.Fatal("first scrape issued no queries")
	}

	second := scrape(t, metrics.Handler())
	if got := db.selectQueries.Load(); got != afterFirst {
		t.Fatalf("second scrape changed query count from %d to %d; cached summary was not served", afterFirst, got)
	}

	for _, want := range []string{
		"windshift_agent_run_queue_depth 1",
		"windshift_agent_runs_in_flight 0",
		`windshift_agent_run_outcomes{outcome="succeeded"} 1`,
		`windshift_agent_run_outcomes{outcome="failed"} 0`,
		"windshift_agent_run_duration_average_seconds",
		`windshift_webhook_dispatches{outcome="success"} 1`,
		`windshift_webhook_dispatches{outcome="failure"} 0`,
	} {
		if !strings.Contains(second, want) {
			t.Fatalf("cached scrape output missing %q\n%s", want, second)
		}
	}
}

// The summary must refresh once the interval lapses so new rows become
// visible, and must stay cached until then even as rows change.
func TestDomainCollectorRefreshesSummaryAfterInterval(t *testing.T) {
	clock := newFakeClock()
	db := &countingDomainDatabase{Database: testutils.CreateTestDB(t, true)}
	seedDomainCollectorFixture(t, db)
	collector := newDomainCollectorForTest(t, db, clock)

	assertDomainMetrics(t, gatherDomainMetrics(t, collector), initialDomainMetricValues())
	if got := db.selectQueries.Load(); got != 3 {
		t.Fatalf("first collect issued %d queries, want 3", got)
	}

	insertQueuedAgentRun(t, db)
	clock.Advance(30 * time.Second)
	assertDomainMetrics(t, gatherDomainMetrics(t, collector), initialDomainMetricValues())
	if got := db.selectQueries.Load(); got != 3 {
		t.Fatalf("collect inside the interval issued %d total queries, want 3", got)
	}

	clock.Advance(31 * time.Second)
	got := gatherDomainMetrics(t, collector)
	if got["windshift_agent_run_queue_depth"] != 2 {
		t.Fatalf("queue depth after refresh = %v, want 2", got["windshift_agent_run_queue_depth"])
	}
	if got := db.selectQueries.Load(); got != 6 {
		t.Fatalf("collect after the interval issued %d total queries, want 6", got)
	}
}

// A failed refresh must keep serving the last good summary instead of
// dropping the domain metrics; with no cache at all the failure surfaces as
// invalid metrics for every descriptor.
func TestDomainCollectorServesStaleSummaryWhenRefreshFails(t *testing.T) {
	clock := newFakeClock()
	db := &countingDomainDatabase{Database: testutils.CreateTestDB(t, true)}
	seedDomainCollectorFixture(t, db)
	collector := newDomainCollectorForTest(t, db, clock)

	assertDomainMetrics(t, gatherDomainMetrics(t, collector), initialDomainMetricValues())

	clock.Advance(2 * time.Minute)
	if err := db.Database.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	assertDomainMetrics(t, gatherDomainMetrics(t, collector), initialDomainMetricValues())

	freshCollector := newDomainCollectorForTest(t, brokenDatabase{}, clock)
	registry := prometheus.NewRegistry()
	registry.MustRegister(freshCollector)
	families, err := registry.Gather()
	if err == nil {
		t.Fatal("collect with failing database should report errors")
	}
	if !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("gather error should carry the query failure, got: %v", err)
	}
	if len(families) != 0 {
		t.Fatalf("failing database should emit no metric families, got %d", len(families))
	}
}
