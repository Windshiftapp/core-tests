//go:build test

package sla

import (
	"testing"
	"time"

	"windshift/internal/models"
)

// A pending metric recalculation must mark the affected items as recalculating.
// recalc_metric jobs carry only a metric_id, so the previous query's
// item_id IS NOT NULL filter hid them.
func TestPendingMetricRecalculationMarksItemsRecalculating(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	fixture.engine.SetClock(fixedClock{now: now})
	metricID := fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen), statusCondition(models.SLAPhaseStop, fixture.statusDone)},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())

	recalculating := func(t *testing.T) bool {
		t.Helper()
		states, err := fixture.engine.ItemsSLABadges(t.Context(), fixture.workspaceID, []int{fixture.itemID})
		if err != nil {
			t.Fatalf("ItemsSLABadges: %v", err)
		}
		for _, state := range states[fixture.itemID] {
			if state.Recalculating {
				return true
			}
		}
		return false
	}

	if recalculating(t) {
		t.Fatal("item reported recalculating before any job was queued")
	}

	if _, err := fixture.db.Exec(`INSERT INTO sla_jobs (kind, metric_id, due_at, state) VALUES ('recalc_metric', ?, CURRENT_TIMESTAMP, 'pending')`, metricID); err != nil {
		t.Fatalf("insert recalc_metric job: %v", err)
	}
	if !recalculating(t) {
		t.Fatal("pending metric recalculation did not mark the item recalculating")
	}

	if _, err := fixture.db.Exec(`UPDATE sla_jobs SET state = 'completed' WHERE kind = 'recalc_metric' AND metric_id = ?`, metricID); err != nil {
		t.Fatalf("complete recalc_metric job: %v", err)
	}
	if recalculating(t) {
		t.Fatal("item still reported recalculating after the metric job completed")
	}
}

// A pending item repair marks only its item, and a metric job in another
// workspace must not leak into this workspace's read.
func TestRecalculatingJobsAreScopedToItemAndWorkspace(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	fixture.engine.SetClock(fixedClock{now: now})
	fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen), statusCondition(models.SLAPhaseStop, fixture.statusDone)},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())

	var otherWorkspaceID int
	if err := fixture.db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Other SLA', 'OSLA') RETURNING id`).Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("insert other workspace: %v", err)
	}
	var otherMetricID int
	if err := fixture.db.QueryRow(`INSERT INTO sla_metrics (workspace_id, name, is_active) VALUES (?, 'Other metric', true) RETURNING id`, otherWorkspaceID).Scan(&otherMetricID); err != nil {
		t.Fatalf("insert other metric: %v", err)
	}
	if _, err := fixture.db.Exec(`INSERT INTO sla_jobs (kind, metric_id, due_at, state) VALUES ('recalc_metric', ?, CURRENT_TIMESTAMP, 'pending')`, otherMetricID); err != nil {
		t.Fatalf("insert cross-workspace job: %v", err)
	}

	states, err := fixture.engine.ItemsSLABadges(t.Context(), fixture.workspaceID, []int{fixture.itemID})
	if err != nil {
		t.Fatalf("ItemsSLABadges: %v", err)
	}
	if states[fixture.itemID][0].Recalculating {
		t.Fatal("a metric job in another workspace marked this item recalculating")
	}

	if _, err := fixture.db.Exec(`INSERT INTO sla_jobs (kind, item_id, due_at, state) VALUES ('recalc_item', ?, CURRENT_TIMESTAMP, 'pending')`, fixture.itemID); err != nil {
		t.Fatalf("insert recalc_item job: %v", err)
	}
	states, err = fixture.engine.ItemsSLABadges(t.Context(), fixture.workspaceID, []int{fixture.itemID})
	if err != nil {
		t.Fatalf("ItemsSLABadges after item repair: %v", err)
	}
	if !states[fixture.itemID][0].Recalculating {
		t.Fatal("pending item repair did not mark the item recalculating")
	}
}
