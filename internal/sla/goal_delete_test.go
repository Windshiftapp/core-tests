//go:build test

package sla

import (
	"testing"
	"time"

	"windshift/internal/models"
)

// Deleting a goal nulls the historical cycle's goal_id via ON DELETE SET NULL
// but keeps the stored duration. The completed-cycle breach, target, and
// remaining-time display must survive.
func TestCompletedCycleKeepsBreachAfterGoalDeletion(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)
	metricID := fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen), statusCondition(models.SLAPhaseStop, fixture.statusDone)},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())
	clock.now = now.Add(2 * time.Hour)
	fixture.observe(t, fixture.statusFact(fixture.statusDone))

	before := derivedState(t, fixture, metricID)
	if len(before.Completed) != 1 {
		t.Fatalf("completed cycles = %d, want 1", len(before.Completed))
	}
	if !before.Completed[0].Breached {
		t.Fatal("overdue completion was not recorded as breached")
	}
	duration := before.Completed[0].GoalDurationMs

	var goalID int
	if err := fixture.db.QueryRow(`SELECT goal_id FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&goalID); err != nil {
		t.Fatalf("load cycle goal: %v", err)
	}
	if _, err := fixture.db.Exec(`DELETE FROM sla_goals WHERE id = ?`, goalID); err != nil {
		t.Fatalf("delete goal: %v", err)
	}

	after := derivedState(t, fixture, metricID)
	if len(after.Completed) != 1 {
		t.Fatalf("completed cycles after goal deletion = %d, want 1", len(after.Completed))
	}
	got := after.Completed[0]
	if got.GoalID != nil {
		t.Fatalf("goal_id = %v after deletion, want nil", got.GoalID)
	}
	if !got.Breached {
		t.Fatal("breach display lost after goal deletion")
	}
	if got.GoalDurationMs != duration {
		t.Fatalf("goal duration = %d after deletion, want %d", got.GoalDurationMs, duration)
	}
	if got.RemainingMs == nil {
		t.Fatal("remaining time lost after goal deletion")
	}
	if got.ElapsedMs != before.Completed[0].ElapsedMs {
		t.Fatalf("elapsed = %d after deletion, want %d", got.ElapsedMs, before.Completed[0].ElapsedMs)
	}
}

// A compliant completed cycle keeps its target and remaining headroom after
// the goal is deleted.
func TestCompliantCycleKeepsHeadroomAfterGoalDeletion(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)
	metricID := fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen), statusCondition(models.SLAPhaseStop, fixture.statusDone)},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())
	clock.now = now.Add(30 * time.Minute)
	fixture.observe(t, fixture.statusFact(fixture.statusDone))

	before := derivedState(t, fixture, metricID)
	if len(before.Completed) != 1 || before.Completed[0].Breached {
		t.Fatalf("completed cycle = %+v, want one compliant cycle", before.Completed)
	}
	var goalID int
	if err := fixture.db.QueryRow(`SELECT goal_id FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&goalID); err != nil {
		t.Fatalf("load cycle goal: %v", err)
	}
	if _, err := fixture.db.Exec(`DELETE FROM sla_goals WHERE id = ?`, goalID); err != nil {
		t.Fatalf("delete goal: %v", err)
	}

	after := derivedState(t, fixture, metricID)
	if len(after.Completed) != 1 {
		t.Fatalf("completed cycles after goal deletion = %d, want 1", len(after.Completed))
	}
	got := after.Completed[0]
	if got.Breached {
		t.Fatal("compliant cycle reported breached after goal deletion")
	}
	if got.RemainingMs == nil || *got.RemainingMs <= 0 {
		t.Fatalf("remaining time = %v after goal deletion, want positive headroom", got.RemainingMs)
	}
	if got.GoalDurationMs != before.Completed[0].GoalDurationMs {
		t.Fatalf("goal duration = %d after deletion, want %d", got.GoalDurationMs, before.Completed[0].GoalDurationMs)
	}
}

// A cycle that never matched a target stays in the no-target state: no
// remaining time and no breach.
func TestNoTargetCycleStaysUnbreached(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	fixture.engine.SetClock(fixedClock{now: now})
	metricID := fixture.createMetric(t,
		[]models.SLACondition{{Phase: models.SLAPhaseStart, Position: 0, ConditionType: conditionCreated, Config: []byte(`{}`)}},
		[]models.SLAGoal{{
			Position: 0, QLQuery: "title ~ 'Nope'", ImportStatus: "native",
			Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 3_600_000, CalendarID: fixture.calendarID}},
		}},
	)
	fixture.observe(t, fixture.createdFact())

	state := derivedState(t, fixture, metricID)
	if state.Ongoing == nil {
		t.Fatal("no ongoing cycle was started")
	}
	if state.Ongoing.GoalDurationMs != 0 {
		t.Fatalf("goal duration = %d for a no-target cycle, want 0", state.Ongoing.GoalDurationMs)
	}
	if state.Ongoing.Breached {
		t.Fatal("no-target cycle reported breached")
	}
	if state.Ongoing.RemainingMs != nil {
		t.Fatalf("remaining time = %v for a no-target cycle, want nil", state.Ongoing.RemainingMs)
	}
	// The synthetic 24/7 snapshot must not be reported as service hours.
	if state.Ongoing.WithinCalendarHours {
		t.Fatal("no-target cycle reported within service hours")
	}
}

func derivedState(t *testing.T, f *engineFixture, metricID int) models.ItemSLA {
	t.Helper()
	states, err := f.engine.ItemsSLA(t.Context(), f.workspaceID, []int{f.itemID})
	if err != nil {
		t.Fatalf("ItemsSLA: %v", err)
	}
	for _, state := range states[f.itemID] {
		if state.MetricID == metricID {
			return state
		}
	}
	t.Fatalf("no SLA state for metric %d", metricID)
	return models.ItemSLA{}
}
