//go:build test

package sla

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
)

type capturingSideEffects struct {
	breachElapsedMs  int64
	warningElapsedMs int64
}

func (c *capturingSideEffects) EmitBreach(_ context.Context, _ database.Tx, cycle *models.ItemSLACycle) error {
	c.breachElapsedMs = cycle.ElapsedMs
	return nil
}

func (c *capturingSideEffects) EmitWarning(_ context.Context, _ database.Tx, cycle *models.ItemSLACycle, _ string) error {
	c.warningElapsedMs = cycle.ElapsedMs
	return nil
}

// A scheduled breach on an untouched cycle must carry the elapsed time at the
// promised deadline, not the last stored value (which is zero before any
// mutation).
func TestBreachJobEmitsElapsedAtDeadline(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)
	effects := &capturingSideEffects{}
	fixture.engine.SetSideEffectEmitter(effects)
	fixture.createMetric(t,
		[]models.SLACondition{{Phase: models.SLAPhaseStart, Position: 0, ConditionType: conditionCreated, Config: json.RawMessage(`{}`)}},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())

	var jobID, cycleID int64
	var deadline time.Time
	if err := fixture.db.QueryRow(`SELECT id, cycle_id, deadline_at FROM sla_jobs WHERE kind = 'breach'`).Scan(&jobID, &cycleID, &deadline); err != nil {
		t.Fatalf("load breach job: %v", err)
	}
	// Only the clock advances; the item is never mutated.
	clock.now = deadline.Add(5 * time.Minute)
	if err := fixture.engine.RunJob(context.Background(), models.SLAJob{
		ID: jobID, Kind: models.SLAJobBreach, CycleID: &cycleID, DueAt: deadline, DeadlineAt: &deadline,
	}); err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	want := deadline.Sub(now).Milliseconds()
	if effects.breachElapsedMs != want {
		t.Fatalf("emitted breach elapsed_ms = %d, want %d", effects.breachElapsedMs, want)
	}
	if got := int64(lifecyclePayload(t, fixture, cycleID, "sla.breached")["elapsed_ms"].(float64)); got != want {
		t.Fatalf("lifecycle elapsed_ms = %d, want %d", got, want)
	}
	var storedElapsed int64
	var breachedAt time.Time
	if err := fixture.db.QueryRow(`SELECT elapsed_ms, breached_at FROM item_sla_cycles WHERE id = ?`, cycleID).Scan(&storedElapsed, &breachedAt); err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if storedElapsed != want {
		t.Fatalf("stored elapsed_ms = %d, want %d", storedElapsed, want)
	}
	if !breachedAt.Equal(deadline) {
		t.Fatalf("breached_at = %v, want the deadline %v", breachedAt, deadline)
	}
}

// A scheduled warning must carry the elapsed time at its firing instant.
func TestWarningJobEmitsCurrentElapsed(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)
	effects := &capturingSideEffects{}
	fixture.engine.SetSideEffectEmitter(effects)
	fixture.addWarningThreshold(t, 50, nil)
	fixture.createMetric(t,
		[]models.SLACondition{{Phase: models.SLAPhaseStart, Position: 0, ConditionType: conditionCreated, Config: json.RawMessage(`{}`)}},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())

	var jobID, cycleID int64
	var dueAt, deadline time.Time
	if err := fixture.db.QueryRow(`SELECT id, cycle_id, due_at, deadline_at FROM sla_jobs WHERE kind = 'warning'`).Scan(&jobID, &cycleID, &dueAt, &deadline); err != nil {
		t.Fatalf("load warning job: %v", err)
	}
	clock.now = dueAt.Add(time.Second)
	if err := fixture.engine.RunJob(context.Background(), models.SLAJob{
		ID: jobID, Kind: models.SLAJobWarning, CycleID: &cycleID, DueAt: dueAt, DeadlineAt: &deadline,
	}); err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	want := dueAt.Sub(now).Milliseconds() + 1000
	if effects.warningElapsedMs != want {
		t.Fatalf("emitted warning elapsed_ms = %d, want %d", effects.warningElapsedMs, want)
	}
	if got := int64(lifecyclePayload(t, fixture, cycleID, "sla.warning")["elapsed_ms"].(float64)); got != want {
		t.Fatalf("lifecycle elapsed_ms = %d, want %d", got, want)
	}
}

func lifecyclePayload(t *testing.T, f *engineFixture, cycleID int64, eventType string) map[string]any {
	t.Helper()
	var raw []byte
	if err := f.db.QueryRow(`SELECT payload FROM domain_events WHERE event_type = ? AND aggregate_type = 'sla_cycle' AND aggregate_id = ?`,
		eventType, strconv.FormatInt(cycleID, 10)).Scan(&raw); err != nil {
		t.Fatalf("load %s event: %v", eventType, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode %s payload: %v", eventType, err)
	}
	return payload
}
