//go:build test

package sla

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"windshift/internal/itemevents"
	"windshift/internal/models"
)

// A cycle that pauses after its deadline but before the breach job runs must
// still record the original breach. Pausing clears the armed jobs, so without
// recording first the breach history, notification, and automation events are
// lost and a later longer target can hide the breach permanently.
func TestPauseAfterDeadlineRecordsOriginalBreach(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)
	effects := &recordingSideEffects{}
	fixture.engine.SetSideEffectEmitter(effects)

	fixture.createMetric(t,
		[]models.SLACondition{
			statusCondition(models.SLAPhaseStart, fixture.statusOpen),
			statusCondition(models.SLAPhasePause, fixture.statusWait),
			statusCondition(models.SLAPhaseStop, fixture.statusDone),
		},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())

	// The breach worker never runs; the item pauses after the deadline.
	clock.now = now.Add(2 * time.Hour)
	fixture.observe(t, fixture.statusFact(fixture.statusWait))

	var cycleID int64
	var breachedAt *time.Time
	var paused bool
	var nextDeadline *time.Time
	if err := fixture.db.QueryRow(`SELECT id, breached_at, paused, next_deadline_at FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).
		Scan(&cycleID, &breachedAt, &paused, &nextDeadline); err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if !paused {
		t.Fatal("cycle is not paused after entering the pause status")
	}
	if breachedAt == nil {
		t.Fatal("pausing after the deadline lost the original breach")
	}
	if nextDeadline != nil {
		t.Fatalf("paused cycle kept an armed deadline %v", nextDeadline)
	}
	if effects.breaches != 1 {
		t.Fatalf("breach side effects = %d, want 1 recorded before the pause", effects.breaches)
	}
	if events := cycleEventCount(t, fixture, cycleID, "sla.breached"); events != 1 {
		t.Fatalf("sla.breached lifecycle events = %d, want 1", events)
	}
	if jobs := fixture.breachJobCount(t); jobs != 0 {
		t.Fatalf("breach jobs after pause = %d, want 0", jobs)
	}

	// Resuming and completing within the recomputed target must not erase the
	// original breach.
	clock.now = now.Add(3 * time.Hour)
	fixture.observe(t, resumeFact(fixture))
	clock.now = now.Add(4 * time.Hour)
	fixture.observe(t, fixture.statusFact(fixture.statusDone))

	var status string
	var keptBreach *time.Time
	if err := fixture.db.QueryRow(`SELECT status, breached_at FROM item_sla_cycles WHERE id = ?`, cycleID).Scan(&status, &keptBreach); err != nil {
		t.Fatalf("reload cycle: %v", err)
	}
	if status != models.SLACycleCompleted {
		t.Fatalf("cycle status = %q, want completed", status)
	}
	if keptBreach == nil || !keptBreach.Equal(*breachedAt) {
		t.Fatalf("breached_at = %v after resume/complete, want the original %v", keptBreach, breachedAt)
	}
	if effects.breaches != 1 {
		t.Fatalf("breach side effects = %d, want exactly 1 for the original breach", effects.breaches)
	}
}

// A target change that adds headroom after the deadline must record the
// original breach before re-goaling, even when the breach worker has not run.
func TestReGoalAfterDeadlineRecordsOriginalBreach(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)
	effects := &recordingSideEffects{}
	fixture.engine.SetSideEffectEmitter(effects)

	var lowPriority int
	if err := fixture.db.QueryRow(`INSERT INTO priorities (name) VALUES ('SLA Low') RETURNING id`).Scan(&lowPriority); err != nil {
		t.Fatalf("insert low priority: %v", err)
	}

	fixture.createMetric(t,
		[]models.SLACondition{{Phase: models.SLAPhaseStart, Position: 0, ConditionType: conditionCreated, Config: json.RawMessage(`{}`)}},
		[]models.SLAGoal{
			{Position: 0, QLQuery: "priority = 'SLA High'", ImportStatus: "native",
				Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 600_000, CalendarID: fixture.calendarID}}},
			{Position: 1, QLQuery: "1 = 1", ImportStatus: "native",
				Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 3_600_000, CalendarID: fixture.calendarID}}},
		},
	)
	fixture.observe(t, fixture.createdFact())

	// The breach worker never runs; the priority change grants a longer target.
	clock.now = now.Add(11 * time.Minute)
	if _, err := fixture.db.Exec(`UPDATE items SET priority_id = ? WHERE id = ?`, lowPriority, fixture.itemID); err != nil {
		t.Fatalf("update item priority: %v", err)
	}
	fixture.observe(t, itemevents.RecordedFact{
		Type: itemevents.Updated, ItemID: fixture.itemID, WorkspaceID: fixture.workspaceID,
		Snapshot: itemevents.ItemSnapshot{ID: fixture.itemID, WorkspaceID: fixture.workspaceID, StatusID: &fixture.statusOpen, PriorityID: &lowPriority},
		Changes:  []itemevents.FieldChange{{Field: "priority_id", OldValue: fixture.priorityAPI, NewValue: lowPriority}},
		Metadata: itemevents.Metadata{ActorKind: "user"},
	})

	var cycleID int64
	var breachedAt *time.Time
	var nextDeadline *time.Time
	if err := fixture.db.QueryRow(`SELECT id, breached_at, next_deadline_at FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).
		Scan(&cycleID, &breachedAt, &nextDeadline); err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if breachedAt == nil {
		t.Fatal("granting headroom after the deadline lost the original breach")
	}
	if nextDeadline == nil || !nextDeadline.After(clock.now) {
		t.Fatalf("re-goal deadline = %v, want a fresh future deadline", nextDeadline)
	}
	if effects.breaches != 1 {
		t.Fatalf("breach side effects = %d, want 1 recorded before the re-goal", effects.breaches)
	}
	if events := cycleEventCount(t, fixture, cycleID, "sla.breached"); events != 1 {
		t.Fatalf("sla.breached lifecycle events = %d, want 1", events)
	}
}

func resumeFact(f *engineFixture) itemevents.RecordedFact {
	return itemevents.RecordedFact{
		Type: itemevents.StatusChanged, ItemID: f.itemID, WorkspaceID: f.workspaceID,
		OldStatusID: &f.statusWait, NewStatusID: &f.statusOpen,
		Snapshot: itemevents.ItemSnapshot{ID: f.itemID, WorkspaceID: f.workspaceID, StatusID: &f.statusOpen, PriorityID: &f.priorityAPI},
		Changes:  []itemevents.FieldChange{{Field: "status_id", OldValue: f.statusWait, NewValue: f.statusOpen}},
		Metadata: itemevents.Metadata{ActorKind: "user"},
	}
}

func cycleEventCount(t *testing.T, f *engineFixture, cycleID int64, eventType string) int {
	t.Helper()
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE event_type = ? AND aggregate_type = 'sla_cycle' AND aggregate_id = ?`,
		eventType, strconv.FormatInt(cycleID, 10)).Scan(&count); err != nil {
		t.Fatalf("count %s events: %v", eventType, err)
	}
	return count
}
