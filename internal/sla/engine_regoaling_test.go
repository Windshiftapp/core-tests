//go:build test

package sla

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"windshift/internal/businesstime"
	"windshift/internal/database"
	"windshift/internal/itemevents"
	"windshift/internal/models"
)

// TestReGoalWithHeadroomKeepsFirstBreach proves a re-goal to a longer target
// never erases the recorded breach: breached_at stays as first-breach
// history, the fresh chance arms a new deadline, and a second expiry records
// another breach event without overwriting the first instant (WI-1537).
func TestReGoalWithHeadroomKeepsFirstBreach(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)

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

	fireBreach := func(t *testing.T) {
		t.Helper()
		var jobID, cycleID int64
		var deadline time.Time
		if err := fixture.db.QueryRow(`SELECT id, cycle_id, deadline_at FROM sla_jobs WHERE kind = 'breach'`).Scan(&jobID, &cycleID, &deadline); err != nil {
			t.Fatalf("load breach job: %v", err)
		}
		clock.now = deadline.Add(time.Second)
		if err := fixture.engine.RunJob(context.Background(), models.SLAJob{
			ID: jobID, Kind: models.SLAJobBreach, CycleID: &cycleID, DueAt: deadline, DeadlineAt: &deadline,
		}); err != nil {
			t.Fatalf("RunJob: %v", err)
		}
	}

	fireBreach(t)
	var firstBreach *time.Time
	var cycleID int64
	if err := fixture.db.QueryRow(`SELECT id, breached_at FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&cycleID, &firstBreach); err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if firstBreach == nil {
		t.Fatal("breached_at is nil after the deadline passed")
	}

	// Re-goal to the longer fallback target: elapsed (11m) is under the new
	// one-hour goal, so the cycle gets a fresh chance — but the breach
	// instant must survive.
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

	var keptBreach *time.Time
	var armedDeadline *time.Time
	if err := fixture.db.QueryRow(`SELECT breached_at, next_deadline_at FROM item_sla_cycles WHERE id = ?`, cycleID).Scan(&keptBreach, &armedDeadline); err != nil {
		t.Fatalf("reload cycle: %v", err)
	}
	if keptBreach == nil {
		t.Fatal("regoaling erased breached_at; compliance reports would lose the breach")
	}
	if !keptBreach.Equal(*firstBreach) {
		t.Fatalf("breached_at = %v after regoal, want the first breach %v", keptBreach, firstBreach)
	}
	if armedDeadline == nil {
		t.Fatal("regoal with headroom left no armed deadline; the fresh chance would never breach")
	}

	// The fresh chance expires: a second breach fires and appends its own
	// event, without overwriting the recorded first instant.
	fireBreach(t)
	var secondBreach *time.Time
	if err := fixture.db.QueryRow(`SELECT breached_at FROM item_sla_cycles WHERE id = ?`, cycleID).Scan(&secondBreach); err != nil {
		t.Fatalf("reload cycle after second breach: %v", err)
	}
	if secondBreach == nil || !secondBreach.Equal(*firstBreach) {
		t.Fatalf("breached_at = %v after second breach, want the first instant %v", secondBreach, firstBreach)
	}
	var breachEvents int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE event_type = 'sla.breached' AND aggregate_type = 'sla_cycle' AND aggregate_id = ?`,
		strconv.FormatInt(cycleID, 10)).Scan(&breachEvents); err != nil {
		t.Fatalf("count breach events: %v", err)
	}
	if breachEvents != 2 {
		t.Fatalf("sla.breached events = %d, want 2 (one per breach)", breachEvents)
	}
}

// TestOverdueCompletionEmitsBreachSideEffects proves a completion that
// crosses the deadline requests the same breach notifications and actions a
// due breach job would have delivered (WI-1576).
func TestOverdueCompletionEmitsBreachSideEffects(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)
	effects := &recordingSideEffects{}
	fixture.engine.SetSideEffectEmitter(effects)

	fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen), statusCondition(models.SLAPhaseStop, fixture.statusDone)},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())

	// The breach worker never runs; the item is completed after the deadline.
	clock.now = now.Add(2 * time.Hour)
	fixture.observe(t, fixture.statusFact(fixture.statusDone))

	var completed string
	var breachedAt *time.Time
	if err := fixture.db.QueryRow(`SELECT status, breached_at FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&completed, &breachedAt); err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if completed != models.SLACycleCompleted {
		t.Fatalf("cycle status = %q, want completed", completed)
	}
	if breachedAt == nil {
		t.Fatal("overdue completion recorded no breach")
	}
	if effects.breaches != 1 {
		t.Fatalf("breach side effects = %d, want 1 from the overdue completion", effects.breaches)
	}

	// Completion before the deadline must still suppress the breach.
	fixture2 := newEngineFixture(t)
	fixture2.engine.SetClock(&advanceClock{now: now})
	effects2 := &recordingSideEffects{}
	fixture2.engine.SetSideEffectEmitter(effects2)
	fixture2.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture2.statusOpen), statusCondition(models.SLAPhaseStop, fixture2.statusDone)},
		fixture2.defaultGoal(),
	)
	fixture2.observe(t, fixture2.createdFact())
	fixture2.observe(t, fixture2.statusFact(fixture2.statusDone))
	if effects2.breaches != 0 {
		t.Fatalf("breach side effects = %d for an on-time completion, want 0", effects2.breaches)
	}
}

// TestStaleBreachRunnerCannotDeleteRearmedJob proves a claimed job whose
// deadline was replaced by an inline re-arm cannot delete the replacement
// when it finally runs (WI-1575).
func TestStaleBreachRunnerCannotDeleteRearmedJob(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)
	fixture.engine.SetJobOwner("runner-a")

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

	var jobID, cycleID int64
	var staleDeadline time.Time
	if err := fixture.db.QueryRow(`SELECT id, cycle_id, deadline_at FROM sla_jobs WHERE kind = 'breach'`).Scan(&jobID, &cycleID, &staleDeadline); err != nil {
		t.Fatalf("load breach job: %v", err)
	}
	// Runner A claims the job.
	if _, err := fixture.db.Exec(`UPDATE sla_jobs SET lease_owner = 'runner-a' WHERE id = ?`, jobID); err != nil {
		t.Fatalf("claim job: %v", err)
	}

	// An inline re-goal replaces the deadline before runner A executes.
	clock.now = now.Add(time.Minute)
	if _, err := fixture.db.Exec(`UPDATE items SET priority_id = ? WHERE id = ?`, lowPriority, fixture.itemID); err != nil {
		t.Fatalf("update item priority: %v", err)
	}
	fixture.observe(t, itemevents.RecordedFact{
		Type: itemevents.Updated, ItemID: fixture.itemID, WorkspaceID: fixture.workspaceID,
		Snapshot: itemevents.ItemSnapshot{ID: fixture.itemID, WorkspaceID: fixture.workspaceID, StatusID: &fixture.statusOpen, PriorityID: &lowPriority},
		Changes:  []itemevents.FieldChange{{Field: "priority_id", OldValue: fixture.priorityAPI, NewValue: lowPriority}},
		Metadata: itemevents.Metadata{ActorKind: "user"},
	})

	// The stale claim finally runs against the re-armed subject.
	clock.now = staleDeadline.Add(time.Minute)
	if err := fixture.engine.RunJob(context.Background(), models.SLAJob{
		ID: jobID, Kind: models.SLAJobBreach, CycleID: &cycleID, DueAt: staleDeadline, DeadlineAt: &staleDeadline,
	}); err != nil {
		t.Fatalf("stale RunJob: %v", err)
	}

	var remaining int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM sla_jobs WHERE kind = 'breach' AND cycle_id = ?`, cycleID).Scan(&remaining); err != nil {
		t.Fatalf("count breach jobs: %v", err)
	}
	var remainingDeadline *time.Time
	if err := fixture.db.QueryRow(`SELECT deadline_at FROM sla_jobs WHERE kind = 'breach' AND cycle_id = ?`, cycleID).Scan(&remainingDeadline); err != nil {
		t.Fatalf("read remaining breach job: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("breach jobs after stale run = %d, want 1 (the replacement must survive)", remaining)
	}
	if remainingDeadline == nil || remainingDeadline.Equal(staleDeadline) {
		t.Fatalf("remaining deadline = %v, want the re-armed deadline (not %v)", remainingDeadline, staleDeadline)
	}
	var breached *time.Time
	if err := fixture.db.QueryRow(`SELECT breached_at FROM item_sla_cycles WHERE id = ?`, cycleID).Scan(&breached); err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if breached != nil {
		t.Fatalf("stale runner breached the cycle at %v; a replaced deadline must not fire", breached)
	}
}

// TestLoopDropsJobsLostToAnotherClaim proves the loop renews its batch's
// leases before running and skips a job whose lease was lost, so an expired
// owner cannot execute work another instance claimed (WI-1593).
func TestLoopDropsJobsLostToAnotherClaim(t *testing.T) {
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	store := newFakeStore()
	store.dueJobs = []models.SLAJob{
		{ID: 7, Kind: models.SLAJobRecalcItem, DueAt: now.Add(-time.Minute)},
		{ID: 9, Kind: models.SLAJobRecalcItem, DueAt: now.Add(-time.Minute)},
	}
	// The first renewal keeps job 7; job 9 was reclaimed by another owner.
	store.renewKeep = map[int64]bool{7: true}
	runner := &fakeRunner{}
	loop := NewLoop(store, runner, fixedClock{now: now}, newTimerRegistry().factory, LoopConfig{})

	if err := loop.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if calls := runner.callCount(); calls != 1 {
		t.Fatalf("runner executions = %d, want 1 (the lost job must not run)", calls)
	}
}

// TestLoopRenewalKeepsSlowBatchOwned proves a full batch stays owned across
// per-job renewals, so every claimed job still runs (WI-1593).
func TestLoopRenewalKeepsSlowBatchOwned(t *testing.T) {
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	store := newFakeStore()
	store.dueJobs = []models.SLAJob{
		{ID: 7, Kind: models.SLAJobRecalcItem, DueAt: now.Add(-time.Minute)},
		{ID: 9, Kind: models.SLAJobRecalcItem, DueAt: now.Add(-time.Minute)},
	}
	runner := &fakeRunner{}
	loop := NewLoop(store, runner, fixedClock{now: now}, newTimerRegistry().factory, LoopConfig{})

	if err := loop.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if calls := runner.callCount(); calls != 2 {
		t.Fatalf("runner executions = %d, want 2 (renewal keeps the batch owned)", calls)
	}
	if store.renewCalls == 0 {
		t.Fatal("the loop never renewed its leases")
	}
}

// A cycle that has not matched a goal carries the synthetic 24/7 snapshot. It
// must not accrue wall-clock elapsed: a goal matched later would inherit that
// time and breach immediately. Reproduces a Saturday intake that breached the
// instant the metric's goal was edited to match it.
func TestNoGoalCycleDoesNotAccrueWallClockElapsed(t *testing.T) {
	fixture := newEngineFixture(t)
	// Mon-Fri 09:00-17:00: the whole Saturday window is outside service hours.
	calendar, err := fixture.repo.GetCalendar(context.Background(), fixture.calendarID)
	if err != nil {
		t.Fatalf("load calendar: %v", err)
	}
	weekly, err := json.Marshal(map[string][]businesstime.ClockInterval{
		"monday":    {{Start: "09:00", End: "17:00"}},
		"tuesday":   {{Start: "09:00", End: "17:00"}},
		"wednesday": {{Start: "09:00", End: "17:00"}},
		"thursday":  {{Start: "09:00", End: "17:00"}},
		"friday":    {{Start: "09:00", End: "17:00"}},
	})
	if err != nil {
		t.Fatalf("encode weekly intervals: %v", err)
	}
	calendar.WeeklyIntervals = json.RawMessage(weekly)
	if err := database.WithTx(fixture.db, func(tx database.Tx) error {
		if err := fixture.repo.UpdateCalendarTx(context.Background(), tx, calendar); err != nil {
			return err
		}
		_, err := fixture.engine.BumpConfigGeneration(context.Background(), tx, fixture.workspaceID)
		return err
	}); err != nil {
		t.Fatalf("update calendar and generation: %v", err)
	}

	saturday := time.Date(2026, 10, 17, 12, 49, 51, 0, time.UTC)
	clock := &advanceClock{now: saturday}
	fixture.engine.SetClock(clock)

	var lowPriority int
	if err := fixture.db.QueryRow(`INSERT INTO priorities (name) VALUES ('SLA Low') RETURNING id`).Scan(&lowPriority); err != nil {
		t.Fatalf("insert low priority: %v", err)
	}

	// The only goal is scoped to a priority the item does not have, so the
	// cycle starts with no matched target.
	metricID := fixture.createMetric(t,
		[]models.SLACondition{{Phase: models.SLAPhaseStart, Position: 0, ConditionType: conditionCreated, Config: json.RawMessage(`{}`)}},
		[]models.SLAGoal{{Position: 0, QLQuery: "priority = 'SLA Low'", ImportStatus: "native",
			Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 4 * 3600_000, CalendarID: fixture.calendarID}}}},
	)
	fixture.observe(t, fixture.createdFact())

	// Eight wall-clock hours later the priority changes and the goal matches.
	clock.now = saturday.Add(8 * time.Hour)
	if _, err := fixture.db.Exec(`UPDATE items SET priority_id = ? WHERE id = ?`, lowPriority, fixture.itemID); err != nil {
		t.Fatalf("update item priority: %v", err)
	}
	fixture.observe(t, itemevents.RecordedFact{
		Type: itemevents.Updated, ItemID: fixture.itemID, WorkspaceID: fixture.workspaceID,
		Snapshot: itemevents.ItemSnapshot{ID: fixture.itemID, WorkspaceID: fixture.workspaceID, StatusID: &fixture.statusOpen, PriorityID: &lowPriority},
		Changes:  []itemevents.FieldChange{{Field: "priority_id", OldValue: fixture.priorityAPI, NewValue: lowPriority}},
		Metadata: itemevents.Metadata{ActorKind: "user"},
	})

	state := derivedState(t, fixture, metricID)
	if state.Ongoing == nil {
		t.Fatal("no ongoing cycle")
	}
	if state.Ongoing.ElapsedMs != 0 {
		t.Fatalf("elapsed = %d for a goal matched outside service hours, want 0", state.Ongoing.ElapsedMs)
	}
	if state.Ongoing.Breached {
		t.Fatal("a goal matched outside service hours inherited wall-clock elapsed and breached")
	}
	wantDeadline := time.Date(2026, 10, 19, 13, 0, 0, 0, time.UTC) // Monday 09:00 + 4h
	if state.Ongoing.NextDeadlineAt == nil || !state.Ongoing.NextDeadlineAt.Equal(wantDeadline) {
		t.Fatalf("deadline = %v, want %v", state.Ongoing.NextDeadlineAt, wantDeadline)
	}
}
