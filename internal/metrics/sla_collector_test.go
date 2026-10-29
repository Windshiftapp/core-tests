//go:build test

package metrics

import (
	"strings"
	"testing"
	"time"

	"windshift/internal/testutils"
)

func TestSLAJobCollectorReportsPendingAndFailed(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = db.Close() })

	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('SLA metrics', 'SLAM') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	insertJob := func(name, state string) {
		var metricID int
		if err := db.QueryRow(`INSERT INTO sla_metrics (workspace_id, name, display_format, is_active, import_status, position)
			VALUES (?, ?, 'time', TRUE, 'native', 0) RETURNING id`, workspaceID, name).Scan(&metricID); err != nil {
			t.Fatalf("insert metric: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO sla_jobs (kind, metric_id, due_at, state)
			VALUES ('recalc_metric', ?, CURRENT_TIMESTAMP, ?)`, metricID, state); err != nil {
			t.Fatalf("insert job: %v", err)
		}
	}
	insertJob("Metric A", "pending")
	insertJob("Metric B", "pending")
	insertJob("Metric C", "failed")

	got := gatherDomainMetrics(t, newSLAJobCollector(db))
	if got["windshift_sla_jobs_pending"] != 2 {
		t.Fatalf("jobs_pending = %v, want 2", got["windshift_sla_jobs_pending"])
	}
	if got["windshift_sla_jobs_failed"] != 1 {
		t.Fatalf("jobs_failed = %v, want 1", got["windshift_sla_jobs_failed"])
	}
}

// TestMetricsExposeSLAInlineHistogram verifies the inline-cost guardrail is
// registered and observable.
func TestMetricsExposeSLAInlineHistogram(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = db.Close() })

	m := New(db, "")
	m.ObserveSLAInlineDuration(5 * time.Millisecond)

	out := scrape(t, m.Handler())
	if !strings.Contains(out, "windshift_sla_inline_duration_seconds_bucket") {
		t.Fatalf("inline duration histogram missing from exposition\n%s", out)
	}
}

func TestSLAJobCollectorCachesWithinInterval(t *testing.T) {
	clock := newFakeClock()
	db := &countingDomainDatabase{Database: testutils.CreateTestDB(t, true)}
	t.Cleanup(func() { _ = db.Close() })

	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('SLA metrics', 'SLAM') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	var metricID int
	if err := db.QueryRow(`INSERT INTO sla_metrics (workspace_id, name, display_format, is_active, import_status, position)
		VALUES (?, 'Metric', 'time', TRUE, 'native', 0) RETURNING id`, workspaceID).Scan(&metricID); err != nil {
		t.Fatalf("insert metric: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sla_jobs (kind, metric_id, due_at, state)
		VALUES ('recalc_metric', ?, CURRENT_TIMESTAMP, 'pending')`, metricID); err != nil {
		t.Fatalf("insert job: %v", err)
	}

	collector := newSLAJobCollector(db).(*slaJobCollector)
	collector.refreshInterval = time.Minute
	collector.now = clock.Now

	_ = gatherDomainMetrics(t, collector)
	first := db.queryRowQueries.Load()
	if first == 0 {
		t.Fatal("first collect issued no queries")
	}

	clock.Advance(30 * time.Second)
	got := gatherDomainMetrics(t, collector)
	if db.queryRowQueries.Load() != first {
		t.Fatalf("collect inside the interval issued extra queries: %d -> %d", first, db.queryRowQueries.Load())
	}
	if got["windshift_sla_jobs_pending"] != 1 {
		t.Fatalf("cached jobs_pending = %v, want 1", got["windshift_sla_jobs_pending"])
	}
}
