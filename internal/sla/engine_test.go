//go:build test

package sla

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"windshift/internal/businesstime"
	"windshift/internal/database"
	"windshift/internal/itemevents"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type engineFixture struct {
	db          database.Database
	engine      *Engine
	repo        *repository.SLARepository
	workspaceID int
	itemID      int
	statusOpen  int
	statusWait  int
	statusDone  int
	priorityAPI int
	calendarID  int
	nudges      []time.Time
}

func newEngineFixture(t *testing.T) *engineFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	repo := repository.NewSLARepository(db)

	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('SLA', 'SLAT') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	activeCategory := insertStatusCategory(t, db, "SLA Active", false)
	completedCategory := insertStatusCategory(t, db, "SLA Completed", true)
	statusOpen := insertStatus(t, db, "SLA Open", activeCategory)
	statusWait := insertStatus(t, db, "SLA Waiting", activeCategory)
	statusDone := insertStatus(t, db, "SLA Done", completedCategory)

	var priorityAPI int
	if err := db.QueryRow(`INSERT INTO priorities (name) VALUES ('SLA High') RETURNING id`).Scan(&priorityAPI); err != nil {
		t.Fatalf("insert priority: %v", err)
	}

	openRaw := businesstime.AlwaysOpenRaw("UTC")
	weekly, _ := json.Marshal(openRaw.WeeklyIntervals)
	holidays, _ := json.Marshal(openRaw.Holidays)
	calendarID, err := repo.CreateCalendar(context.Background(), &models.WorkingCalendar{
		WorkspaceID:     &workspaceID,
		Name:            "Always open",
		Timezone:        "UTC",
		WeeklyIntervals: weekly,
		Holidays:        holidays,
	})
	if err != nil {
		t.Fatalf("create calendar: %v", err)
	}

	if err := database.WithTx(db, func(tx database.Tx) error {
		_, err := repo.TouchWorkspaceState(context.Background(), tx, workspaceID)
		return err
	}); err != nil {
		t.Fatalf("initialize workspace SLA state: %v", err)
	}

	var itemID int
	if err := db.QueryRow(`INSERT INTO items (workspace_id, workspace_item_number, title, status_id, priority_id, frac_index) VALUES (?, 1, 'Ticket', ?, ?, ?) RETURNING id`,
		workspaceID, statusOpen, priorityAPI, testutils.NextTestFracIndex()).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	fixture := &engineFixture{
		db: db, repo: repo, workspaceID: workspaceID, itemID: itemID,
		statusOpen: statusOpen, statusWait: statusWait, statusDone: statusDone,
		priorityAPI: priorityAPI, calendarID: calendarID,
	}
	fixture.engine = NewEngine(db)
	fixture.engine.SetNudge(func(dueAt time.Time) { fixture.nudges = append(fixture.nudges, dueAt) })
	t.Cleanup(func() { fixture.engine.InvalidateWorkspace(workspaceID) })
	return fixture
}

func insertStatusCategory(t *testing.T, db database.Database, name string, completed bool) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`INSERT INTO status_categories (name, color, is_completed) VALUES (?, '#888888', ?) RETURNING id`, name, completed).Scan(&id); err != nil {
		t.Fatalf("insert status category %s: %v", name, err)
	}
	return id
}

func insertStatus(t *testing.T, db database.Database, name string, categoryID int) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`INSERT INTO statuses (name, category_id) VALUES (?, ?) RETURNING id`, name, categoryID).Scan(&id); err != nil {
		t.Fatalf("insert status %s: %v", name, err)
	}
	return id
}

func statusCondition(phase string, statusID int) models.SLACondition {
	config, _ := json.Marshal(map[string]any{"status_ids": []int{statusID}})
	return models.SLACondition{Phase: phase, Position: 0, ConditionType: conditionStatusEntered, Config: config}
}

func (f *engineFixture) createMetric(t *testing.T, conditions []models.SLACondition, goals []models.SLAGoal) int {
	t.Helper()
	var metricID int
	err := database.WithTx(f.db, func(tx database.Tx) error {
		_, err := f.repo.TouchWorkspaceState(context.Background(), tx, f.workspaceID)
		if err != nil {
			return err
		}
		id, err := f.repo.CreateMetric(context.Background(), tx, &models.SLAMetric{
			WorkspaceID: f.workspaceID, Name: "First response", DisplayFormat: "time", IsActive: true, ImportStatus: "native",
			Conditions: conditions,
			Goals:      goals,
		})
		metricID = id
		return err
	})
	if err != nil {
		t.Fatalf("create metric: %v", err)
	}
	f.engine.InvalidateWorkspace(f.workspaceID)
	return metricID
}

func (f *engineFixture) defaultGoal() []models.SLAGoal {
	return []models.SLAGoal{{
		Position: 0, QLQuery: "1 = 1", ImportStatus: "native",
		Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 3_600_000, CalendarID: f.calendarID}},
	}}
}

func (f *engineFixture) observe(t *testing.T, facts ...itemevents.RecordedFact) {
	t.Helper()
	err := database.WithTx(f.db, func(tx database.Tx) error {
		return f.engine.ObserveItemFacts(context.Background(), tx, facts)
	})
	if err != nil {
		t.Fatalf("ObserveItemFacts: %v", err)
	}
}

func (f *engineFixture) createdFact() itemevents.RecordedFact {
	return itemevents.RecordedFact{
		Type: itemevents.Created, ItemID: f.itemID, WorkspaceID: f.workspaceID,
		Snapshot: itemevents.ItemSnapshot{ID: f.itemID, WorkspaceID: f.workspaceID, StatusID: &f.statusOpen, PriorityID: &f.priorityAPI},
		Metadata: itemevents.Metadata{ActorKind: "user"},
	}
}

func (f *engineFixture) statusFact(toStatus int) itemevents.RecordedFact {
	oldStatus := f.statusOpen
	return itemevents.RecordedFact{
		Type: itemevents.StatusChanged, ItemID: f.itemID, WorkspaceID: f.workspaceID,
		OldStatusID: &oldStatus, NewStatusID: &toStatus,
		Snapshot: itemevents.ItemSnapshot{ID: f.itemID, WorkspaceID: f.workspaceID, StatusID: &toStatus, PriorityID: &f.priorityAPI},
		Changes:  []itemevents.FieldChange{{Field: "status_id", OldValue: oldStatus, NewValue: toStatus}},
		Metadata: itemevents.Metadata{ActorKind: "user"},
	}
}

func (f *engineFixture) commentFact(authorKind string, isPrivate bool) itemevents.RecordedFact {
	return itemevents.RecordedFact{
		Type: itemevents.CommentCreated, ItemID: f.itemID, WorkspaceID: f.workspaceID,
		CommentAuthorKind: authorKind, CommentIsPrivate: isPrivate,
		Snapshot: itemevents.ItemSnapshot{ID: f.itemID, WorkspaceID: f.workspaceID, StatusID: &f.statusOpen, PriorityID: &f.priorityAPI},
		Metadata: itemevents.Metadata{ActorKind: authorKind},
	}
}

func (f *engineFixture) cycleCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM item_sla_cycles`).Scan(&count); err != nil {
		t.Fatalf("count cycles: %v", err)
	}
	return count
}

func (f *engineFixture) breachJobCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM sla_jobs WHERE kind = 'breach'`).Scan(&count); err != nil {
		t.Fatalf("count breach jobs: %v", err)
	}
	return count
}

func TestInlineEvaluationStartsAndCompletesCycle(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	fixture.engine.SetClock(fixedClock{now: now})
	fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen), statusCondition(models.SLAPhaseStop, fixture.statusDone)},
		fixture.defaultGoal(),
	)

	fixture.observe(t, fixture.createdFact())

	var status, origin string
	var deadline *time.Time
	err := fixture.db.QueryRow(`SELECT status, origin, next_deadline_at FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&status, &origin, &deadline)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if status != models.SLACycleOngoing || origin != models.SLAOriginNative {
		t.Fatalf("cycle = %s/%s, want ongoing/native", status, origin)
	}
	if deadline == nil || !deadline.Equal(now.Add(time.Hour)) {
		t.Fatalf("deadline = %v, want %v", deadline, now.Add(time.Hour))
	}
	if jobs := fixture.breachJobCount(t); jobs != 1 {
		t.Fatalf("breach jobs = %d, want 1", jobs)
	}
	if len(fixture.nudges) == 0 {
		t.Fatal("expected a due-work nudge after arming the breach job")
	}

	fixture.observe(t, fixture.statusFact(fixture.statusDone))

	if err := fixture.db.QueryRow(`SELECT status FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&status); err != nil {
		t.Fatalf("reload cycle: %v", err)
	}
	if status != models.SLACycleCompleted {
		t.Fatalf("cycle status = %s, want completed", status)
	}
	if jobs := fixture.breachJobCount(t); jobs != 0 {
		t.Fatalf("breach jobs after completion = %d, want 0", jobs)
	}
}

// A goal with a blank query matches every item, so a metric whose only goal is
// blank always has a target.
func TestBlankGoalQueryMatchesEveryItem(t *testing.T) {
	fixture := newEngineFixture(t)
	fixture.engine.SetClock(fixedClock{now: time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)})
	metricID := fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen)},
		[]models.SLAGoal{{Position: 0, QLQuery: "", ImportStatus: "native",
			Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 3_600_000, CalendarID: fixture.calendarID}}}},
	)
	fixture.observe(t, fixture.createdFact())

	state := derivedState(t, fixture, metricID)
	if state.Ongoing == nil {
		t.Fatal("no ongoing cycle for a match-all goal")
	}
	if state.Ongoing.GoalDurationMs != 3_600_000 {
		t.Fatalf("goal duration = %d, want 3600000 for a match-all goal", state.Ongoing.GoalDurationMs)
	}
}

func TestInlineEvaluationStopBeatsStart(t *testing.T) {
	fixture := newEngineFixture(t)
	fixture.engine.SetClock(fixedClock{now: time.Now().UTC()})
	// The same status satisfies start and stop; stop wins.
	fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen), statusCondition(models.SLAPhaseStop, fixture.statusOpen)},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())
	if count := fixture.cycleCount(t); count != 0 {
		t.Fatalf("cycles = %d, want 0 when stop beats start", count)
	}
}

func TestInlineEvaluationPausesAndResumes(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	fixture.engine.SetClock(fixedClock{now: now})
	fixture.createMetric(t,
		[]models.SLACondition{
			statusCondition(models.SLAPhaseStart, fixture.statusOpen),
			statusCondition(models.SLAPhasePause, fixture.statusWait),
			statusCondition(models.SLAPhaseStop, fixture.statusDone),
		},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())

	fixture.observe(t, fixture.statusFact(fixture.statusWait))
	var paused bool
	var nextDeadline *time.Time
	if err := fixture.db.QueryRow(`SELECT paused, next_deadline_at FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&paused, &nextDeadline); err != nil {
		t.Fatalf("load paused cycle: %v", err)
	}
	if !paused || nextDeadline != nil {
		t.Fatalf("paused=%v deadline=%v, want paused with no deadline", paused, nextDeadline)
	}
	if jobs := fixture.breachJobCount(t); jobs != 0 {
		t.Fatalf("breach jobs while paused = %d, want 0", jobs)
	}

	fixture.observe(t, fixture.statusFact(fixture.statusOpen))
	if err := fixture.db.QueryRow(`SELECT paused, next_deadline_at FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&paused, &nextDeadline); err != nil {
		t.Fatalf("load resumed cycle: %v", err)
	}
	if paused || nextDeadline == nil {
		t.Fatalf("paused=%v deadline=%v, want resumed with a deadline", paused, nextDeadline)
	}
	if jobs := fixture.breachJobCount(t); jobs != 1 {
		t.Fatalf("breach jobs after resume = %d, want 1", jobs)
	}
}

func TestResumeReevaluatesGoalChangedWhilePaused(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)
	var lowPriority int
	if err := fixture.db.QueryRow(`INSERT INTO priorities (name) VALUES ('SLA Low') RETURNING id`).Scan(&lowPriority); err != nil {
		t.Fatalf("insert low priority: %v", err)
	}
	fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen), statusCondition(models.SLAPhasePause, fixture.statusWait)},
		[]models.SLAGoal{
			{Position: 0, QLQuery: "priority = 'SLA High'", ImportStatus: "native", Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 600_000, CalendarID: fixture.calendarID}}},
			{Position: 1, QLQuery: "1 = 1", ImportStatus: "native", Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 3_600_000, CalendarID: fixture.calendarID}}},
		},
	)
	fixture.observe(t, fixture.createdFact())
	clock.now = now.Add(5 * time.Minute)
	fixture.observe(t, fixture.statusFact(fixture.statusWait))

	clock.now = now.Add(7 * time.Minute)
	if _, err := fixture.db.Exec(`UPDATE items SET priority_id = ? WHERE id = ?`, lowPriority, fixture.itemID); err != nil {
		t.Fatalf("update item priority: %v", err)
	}
	priorityFact := itemevents.RecordedFact{
		Type: itemevents.Updated, ItemID: fixture.itemID, WorkspaceID: fixture.workspaceID,
		Snapshot: itemevents.ItemSnapshot{ID: fixture.itemID, WorkspaceID: fixture.workspaceID, StatusID: &fixture.statusWait, PriorityID: &lowPriority},
		Changes:  []itemevents.FieldChange{{Field: "priority_id", OldValue: fixture.priorityAPI, NewValue: lowPriority}},
		Metadata: itemevents.Metadata{ActorKind: "user"},
	}
	fixture.observe(t, priorityFact)

	clock.now = now.Add(10 * time.Minute)
	if _, err := fixture.db.Exec(`UPDATE items SET status_id = ? WHERE id = ?`, fixture.statusOpen, fixture.itemID); err != nil {
		t.Fatalf("resume item status: %v", err)
	}
	resumeFact := fixture.statusFact(fixture.statusOpen)
	resumeFact.Snapshot.PriorityID = &lowPriority
	fixture.observe(t, resumeFact)

	var duration, elapsed int64
	var paused bool
	var deadline *time.Time
	var goalSnapshot string
	if err := fixture.db.QueryRow(`SELECT goal_duration_ms, elapsed_ms, paused, next_deadline_at, goal_query_snapshot FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&duration, &elapsed, &paused, &deadline, &goalSnapshot); err != nil {
		t.Fatalf("load resumed cycle: %v", err)
	}
	wantDeadline := now.Add(65 * time.Minute)
	if duration != 3_600_000 || elapsed != (5*time.Minute).Milliseconds() || paused || deadline == nil || !deadline.Equal(wantDeadline) || goalSnapshot != "1 = 1" {
		t.Fatalf("resumed cycle duration/elapsed/paused/deadline/query = %d/%d/%v/%v/%q, want 3600000/300000/false/%v/1 = 1", duration, elapsed, paused, deadline, goalSnapshot, wantDeadline)
	}
}

func TestRecalculateAppliesEditedTargetDurationToOngoingCycle(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	fixture.engine.SetClock(fixedClock{now: now})
	metricID := fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen)},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())
	var targetID int
	if err := fixture.db.QueryRow(`SELECT t.id FROM sla_goal_targets t JOIN sla_goals g ON g.id = t.goal_id WHERE g.metric_id = ?`, metricID).Scan(&targetID); err != nil {
		t.Fatalf("load target: %v", err)
	}
	updatedCalendar := businesstime.AlwaysOpenRaw("Europe/Berlin")
	weekly, err := json.Marshal(updatedCalendar.WeeklyIntervals)
	if err != nil {
		t.Fatalf("encode updated calendar: %v", err)
	}
	holidays, err := json.Marshal(updatedCalendar.Holidays)
	if err != nil {
		t.Fatalf("encode updated holidays: %v", err)
	}
	newCalendarID, err := fixture.repo.CreateCalendar(context.Background(), &models.WorkingCalendar{
		WorkspaceID: &fixture.workspaceID, Name: "Updated hours", Timezone: updatedCalendar.Timezone, WeeklyIntervals: weekly, Holidays: holidays,
	})
	if err != nil {
		t.Fatalf("create updated calendar: %v", err)
	}
	if _, err := fixture.db.Exec(`UPDATE sla_goal_targets SET target_ms = ?, calendar_id = ? WHERE id = ?`, 7_200_000, newCalendarID, targetID); err != nil {
		t.Fatalf("edit target duration and calendar: %v", err)
	}
	if err := database.WithTx(fixture.db, func(tx database.Tx) error {
		_, err := fixture.engine.BumpConfigGeneration(context.Background(), tx, fixture.workspaceID)
		return err
	}); err != nil {
		t.Fatalf("bump config generation: %v", err)
	}
	if err := fixture.engine.RecalculateItem(context.Background(), fixture.itemID); err != nil {
		t.Fatalf("RecalculateItem: %v", err)
	}
	var duration, storedCalendarID int64
	var deadline *time.Time
	if err := fixture.db.QueryRow(`SELECT goal_duration_ms, calendar_id, next_deadline_at FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&duration, &storedCalendarID, &deadline); err != nil {
		t.Fatalf("load recalculated cycle: %v", err)
	}
	if duration != 7_200_000 || storedCalendarID != int64(newCalendarID) || deadline == nil || !deadline.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("recalculated duration/calendar/deadline = %d/%d/%v, want 7200000/%d/%v", duration, storedCalendarID, deadline, newCalendarID, now.Add(2*time.Hour))
	}
}

func TestInlineEvaluationIrrelevantFactWritesNothing(t *testing.T) {
	fixture := newEngineFixture(t)
	fixture.engine.SetClock(fixedClock{now: time.Now().UTC()})
	// Goal depends only on priority, so a rank drag must not open a cycle.
	fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen)},
		[]models.SLAGoal{{Position: 0, QLQuery: "priority = High", ImportStatus: "native",
			Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 3_600_000, CalendarID: fixture.calendarID}}}},
	)
	rank := "a0"
	next := "a1"
	fact := itemevents.RecordedFact{
		Type: itemevents.Updated, ItemID: fixture.itemID, WorkspaceID: fixture.workspaceID,
		Snapshot: itemevents.ItemSnapshot{ID: fixture.itemID, WorkspaceID: fixture.workspaceID, StatusID: &fixture.statusOpen},
		Changes:  []itemevents.FieldChange{{Field: "frac_index", OldValue: rank, NewValue: next}},
		Metadata: itemevents.Metadata{ActorKind: "user"},
	}
	fixture.observe(t, fact)
	if count := fixture.cycleCount(t); count != 0 {
		t.Fatalf("cycles = %d, want 0 for an irrelevant rank change", count)
	}
}

func TestRecalculateMetricBackfillsExistingItemAsBackfill(t *testing.T) {
	fixture := newEngineFixture(t)
	fixture.engine.SetClock(fixedClock{now: time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)})
	metricID := fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen)},
		fixture.defaultGoal(),
	)

	next, done, err := fixture.engine.RecalculateMetric(context.Background(), metricID, "", 10)
	if err != nil {
		t.Fatalf("RecalculateMetric: %v", err)
	}
	if done || next == "" {
		t.Fatalf("first recalc page = %q/%v, want a non-empty cursor and not done", next, done)
	}
	next, done, err = fixture.engine.RecalculateMetric(context.Background(), metricID, next, 10)
	if err != nil {
		t.Fatalf("second RecalculateMetric: %v", err)
	}
	if !done || next != "" {
		t.Fatalf("final recalc page = %q/%v, want done", next, done)
	}
	var origin, status string
	if err := fixture.db.QueryRow(`SELECT origin, status FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&origin, &status); err != nil {
		t.Fatalf("load backfilled cycle: %v", err)
	}
	if origin != models.SLAOriginBackfill || status != models.SLACycleOngoing {
		t.Fatalf("backfill cycle = %s/%s, want backfill/ongoing", origin, status)
	}
}

func TestRecalculateItemDoesNotMutateCompletedCycles(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	fixture.engine.SetClock(fixedClock{now: now})
	fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen), statusCondition(models.SLAPhaseStop, fixture.statusDone)},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())
	fixture.observe(t, fixture.statusFact(fixture.statusDone))

	// Mark the item back to the start status, then recalc. The completed cycle
	// must keep its stored elapsed and stay the only cycle.
	if _, err := fixture.db.Exec(`UPDATE items SET status_id = ? WHERE id = ?`, fixture.statusOpen, fixture.itemID); err != nil {
		t.Fatalf("reset item status: %v", err)
	}
	if err := fixture.engine.RecalculateItem(context.Background(), fixture.itemID); err != nil {
		t.Fatalf("RecalculateItem: %v", err)
	}
	var completed int
	var elapsed *int64
	if err := fixture.db.QueryRow(`SELECT COUNT(*), MAX(elapsed_ms) FROM item_sla_cycles WHERE status = 'completed'`).Scan(&completed, &elapsed); err != nil {
		t.Fatalf("load completed cycles: %v", err)
	}
	if completed != 1 {
		t.Fatalf("completed cycles = %d, want 1 (immutable)", completed)
	}
	if elapsed == nil || *elapsed < 0 {
		t.Fatalf("completed elapsed = %v, want a stored non-negative value", elapsed)
	}
}

func TestBreachJobFiresOnceAndEmitsEvent(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)
	fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen)},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())

	var jobID int64
	var cycleID int64
	var deadline time.Time
	if err := fixture.db.QueryRow(`SELECT id, cycle_id, deadline_at FROM sla_jobs WHERE kind = 'breach'`).Scan(&jobID, &cycleID, &deadline); err != nil {
		t.Fatalf("load breach job: %v", err)
	}
	clock.now = deadline.Add(time.Second)
	job := models.SLAJob{ID: jobID, Kind: models.SLAJobBreach, CycleID: &cycleID, DueAt: deadline, DeadlineAt: &deadline}
	if err := fixture.engine.RunJob(context.Background(), job); err != nil {
		t.Fatalf("RunJob breaches: %v", err)
	}
	var breachedAt sql.NullTime
	if err := fixture.db.QueryRow(`SELECT breached_at FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&breachedAt); err != nil {
		t.Fatalf("load breached cycle: %v", err)
	}
	if !breachedAt.Valid || !breachedAt.Time.Equal(deadline) {
		t.Fatalf("breached_at = %v, want %v", breachedAt, deadline)
	}
	var events int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE event_type = 'sla.breached'`).Scan(&events); err != nil {
		t.Fatalf("count breach events: %v", err)
	}
	if events != 1 {
		t.Fatalf("breach events = %d, want exactly 1", events)
	}
	// Running the same job again must not emit a second breach.
	if err := fixture.engine.RunJob(context.Background(), job); err != nil {
		t.Fatalf("RunJob repeat: %v", err)
	}
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE event_type = 'sla.breached'`).Scan(&events); err != nil {
		t.Fatalf("count breach events after repeat: %v", err)
	}
	if events != 1 {
		t.Fatalf("breach events after repeat = %d, want still 1", events)
	}
}

type advanceClock struct{ now time.Time }

func (c *advanceClock) Now() time.Time { return c.now }

func TestInlineEvaluationGoalChangePreservesElapsed(t *testing.T) {
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

	var goalID int
	var duration int64
	if err := fixture.db.QueryRow(`SELECT goal_id, goal_duration_ms FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&goalID, &duration); err != nil {
		t.Fatalf("load initial cycle: %v", err)
	}
	if duration != 600_000 {
		t.Fatalf("initial goal duration = %d, want 600000 (priority target)", duration)
	}

	// Advance five minutes, then change priority. The elapsed time must carry
	// into the newly matched goal. The item row is written before the fact, as
	// it is in the real source transaction, so goal matching sees the new value.
	clock.now = now.Add(5 * time.Minute)
	if _, err := fixture.db.Exec(`UPDATE items SET priority_id = ? WHERE id = ?`, lowPriority, fixture.itemID); err != nil {
		t.Fatalf("update item priority: %v", err)
	}
	change := itemevents.RecordedFact{
		Type: itemevents.Updated, ItemID: fixture.itemID, WorkspaceID: fixture.workspaceID,
		Snapshot: itemevents.ItemSnapshot{ID: fixture.itemID, WorkspaceID: fixture.workspaceID, StatusID: &fixture.statusOpen, PriorityID: &lowPriority},
		Changes:  []itemevents.FieldChange{{Field: "priority_id", OldValue: fixture.priorityAPI, NewValue: lowPriority}},
		Metadata: itemevents.Metadata{ActorKind: "user"},
	}
	fixture.observe(t, change)

	var newGoalID int
	var newDuration, elapsed int64
	if err := fixture.db.QueryRow(`SELECT goal_id, goal_duration_ms, elapsed_ms FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&newGoalID, &newDuration, &elapsed); err != nil {
		t.Fatalf("reload cycle: %v", err)
	}
	if newGoalID == goalID {
		t.Fatalf("goal_id = %d, want a newly matched goal", newGoalID)
	}
	if newDuration != 3_600_000 {
		t.Fatalf("new goal duration = %d, want 3600000", newDuration)
	}
	if elapsed != (5 * time.Minute).Milliseconds() {
		t.Fatalf("elapsed after goal change = %d, want %d (preserved)", elapsed, (5 * time.Minute).Milliseconds())
	}
}

func TestInlineEvaluationFailureEnqueuesRepairWithoutFailingItemWrite(t *testing.T) {
	fixture := newEngineFixture(t)
	fixture.engine.SetClock(fixedClock{now: time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)})
	// Deliberately invalid goal QL: config compilation fails.
	fixture.createMetric(t,
		[]models.SLACondition{{Phase: models.SLAPhaseStart, Position: 0, ConditionType: conditionCreated, Config: json.RawMessage(`{}`)}},
		[]models.SLAGoal{{Position: 0, QLQuery: "===", ImportStatus: "native",
			Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 1000, CalendarID: fixture.calendarID}}}},
	)

	// The observer must swallow the failure: the caller's transaction commits.
	fixture.observe(t, fixture.createdFact())

	if count := fixture.cycleCount(t); count != 0 {
		t.Fatalf("cycles = %d, want 0 after a failed evaluation", count)
	}
	var repairs int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM sla_jobs WHERE kind = 'recalc_item' AND item_id = ?`, fixture.itemID).Scan(&repairs); err != nil {
		t.Fatalf("count repair jobs: %v", err)
	}
	if repairs != 1 {
		t.Fatalf("recalc_item jobs = %d, want 1", repairs)
	}
}

type recordingSideEffects struct {
	breaches int
	warnings int
}

func (r *recordingSideEffects) EmitBreach(context.Context, database.Tx, *models.ItemSLACycle) error {
	r.breaches++
	return nil
}

func (r *recordingSideEffects) EmitWarning(context.Context, database.Tx, *models.ItemSLACycle, string) error {
	r.warnings++
	return nil
}

func TestBreachJobInvokesSideEffectEmitter(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)
	effects := &recordingSideEffects{}
	fixture.engine.SetSideEffectEmitter(effects)
	fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen)},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())

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
	if effects.breaches != 1 {
		t.Fatalf("breach side effects = %d, want 1", effects.breaches)
	}
}

func TestCalendarRecalculationUpdatesExistingCyclesWithoutStartingCycles(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 16, 0, 0, 0, time.UTC)
	fixture.engine.SetClock(fixedClock{now: now})
	metricID := fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen)},
		[]models.SLAGoal{{Position: 0, QLQuery: "1 = 1", ImportStatus: "native", Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 7_200_000, CalendarID: fixture.calendarID}}}},
	)
	fixture.observe(t, fixture.createdFact())
	var secondItemID int
	if err := fixture.db.QueryRow(`INSERT INTO items (workspace_id, workspace_item_number, title, status_id, priority_id, frac_index) VALUES (?, 2, 'No cycle', ?, ?, ?) RETURNING id`,
		fixture.workspaceID, fixture.statusOpen, fixture.priorityAPI, testutils.NextTestFracIndex()).Scan(&secondItemID); err != nil {
		t.Fatalf("insert item without cycle: %v", err)
	}
	calendar, err := fixture.repo.GetCalendar(context.Background(), fixture.calendarID)
	if err != nil {
		t.Fatalf("load calendar: %v", err)
	}
	weekly, err := json.Marshal(map[string][]businesstime.ClockInterval{
		"wednesday": {{Start: "09:00", End: "17:00"}},
		"thursday":  {{Start: "09:00", End: "17:00"}},
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
	job := models.SLAJob{Kind: models.SLAJobRecalcCalendar, ThresholdKey: fmt.Sprint(fixture.calendarID), MetricID: &metricID}
	if err := fixture.engine.RunJob(context.Background(), job); err != nil {
		t.Fatalf("RunJob: %v", err)
	}

	var deadline sql.NullTime
	var snapshot string
	if err := fixture.db.QueryRow(`SELECT next_deadline_at, calendar_snapshot FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&deadline, &snapshot); err != nil {
		t.Fatalf("load recalculated cycle: %v", err)
	}
	if !deadline.Valid || !deadline.Time.Equal(time.Date(2025, 1, 9, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("recalculated deadline = %v, want 2025-01-09T10:00:00Z", deadline)
	}
	if !strings.Contains(snapshot, "wednesday") || !strings.Contains(snapshot, "thursday") {
		t.Fatalf("calendar snapshot = %q, want the edited schedule", snapshot)
	}
	var secondCycleCount int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM item_sla_cycles WHERE item_id = ?`, secondItemID).Scan(&secondCycleCount); err != nil {
		t.Fatalf("count second item cycles: %v", err)
	}
	if secondCycleCount != 0 {
		t.Fatalf("new cycles started by calendar recalculation = %d, want 0", secondCycleCount)
	}
	var metricIDFromCycle int
	if err := fixture.db.QueryRow(`SELECT metric_id FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&metricIDFromCycle); err != nil {
		t.Fatalf("load metric: %v", err)
	}
	if metricIDFromCycle != metricID {
		t.Fatalf("cycle metric ID = %d, want %d", metricIDFromCycle, metricID)
	}
}

func TestBreachJobYieldsToEarlierStopTransition(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)
	fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen), statusCondition(models.SLAPhaseStop, fixture.statusDone)},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())
	var jobID, cycleID int64
	var deadline time.Time
	if err := fixture.db.QueryRow(`SELECT id, cycle_id, deadline_at FROM sla_jobs WHERE kind = 'breach'`).Scan(&jobID, &cycleID, &deadline); err != nil {
		t.Fatalf("load breach job: %v", err)
	}
	stoppedAt := deadline.Add(-time.Minute)
	if _, err := fixture.db.Exec(`UPDATE items SET status_id = ?, updated_at = ? WHERE id = ?`, fixture.statusDone, stoppedAt, fixture.itemID); err != nil {
		t.Fatalf("apply stop transition: %v", err)
	}
	clock.now = deadline.Add(time.Minute)
	if err := fixture.engine.RunJob(context.Background(), models.SLAJob{
		ID: jobID, Kind: models.SLAJobBreach, CycleID: &cycleID, DueAt: deadline, DeadlineAt: &deadline,
	}); err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	var status string
	var breachedAt sql.NullTime
	var elapsed int64
	if err := fixture.db.QueryRow(`SELECT status, breached_at, elapsed_ms FROM item_sla_cycles WHERE id = ?`, cycleID).Scan(&status, &breachedAt, &elapsed); err != nil {
		t.Fatalf("load stopped cycle: %v", err)
	}
	if status != models.SLACycleCompleted || breachedAt.Valid || elapsed != time.Hour.Milliseconds()-time.Minute.Milliseconds() {
		t.Fatalf("stopped cycle = status:%q breach:%v elapsed:%d, want completed/unbreached/3540000", status, breachedAt, elapsed)
	}
	var events int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE event_type = 'sla.breached'`).Scan(&events); err != nil {
		t.Fatalf("count breach events: %v", err)
	}
	if events != 0 {
		t.Fatalf("breach events = %d, want 0 when stop preceded deadline", events)
	}
}

func (f *engineFixture) addWarningThreshold(t *testing.T, percent int, metricID *int) int {
	t.Helper()
	var id int
	err := database.WithTx(f.db, func(tx database.Tx) error {
		created, err := f.repo.CreateWarningThreshold(context.Background(), tx, &models.SLAWarningThreshold{
			WorkspaceID: f.workspaceID, MetricID: metricID, Percent: percent, IsActive: true,
		})
		if err != nil {
			return err
		}
		id = created
		_, err = f.repo.TouchWorkspaceState(context.Background(), tx, f.workspaceID)
		return err
	})
	if err != nil {
		t.Fatalf("create warning threshold: %v", err)
	}
	f.engine.InvalidateWorkspace(f.workspaceID)
	return id
}

func TestWarningThresholdArmsAndFiresOnce(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)
	fixture.addWarningThreshold(t, 50, nil)
	fixture.createMetric(t,
		[]models.SLACondition{{Phase: models.SLAPhaseStart, Position: 0, ConditionType: conditionCreated, Config: json.RawMessage(`{}`)}},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())

	var warningID, cycleID int64
	var dueAt, deadline time.Time
	if err := fixture.db.QueryRow(`SELECT id, cycle_id, due_at, deadline_at FROM sla_jobs WHERE kind = 'warning'`).Scan(&warningID, &cycleID, &dueAt, &deadline); err != nil {
		t.Fatalf("load warning job: %v", err)
	}
	wantDue := now.Add(30 * time.Minute)
	if diff := dueAt.Sub(wantDue); diff > time.Minute || diff < -time.Minute {
		t.Fatalf("warning due_at = %s, want ~%s", dueAt, wantDue)
	}
	if !deadline.Equal(now.Add(time.Hour)) {
		t.Fatalf("warning deadline = %s, want the breach deadline %s", deadline, now.Add(time.Hour))
	}

	clock.now = dueAt.Add(time.Second)
	job := models.SLAJob{ID: warningID, Kind: models.SLAJobWarning, CycleID: &cycleID, DueAt: dueAt, DeadlineAt: &deadline}
	if err := fixture.engine.RunJob(context.Background(), job); err != nil {
		t.Fatalf("RunJob warning: %v", err)
	}
	assertEventCount := func(eventType string, want int) {
		t.Helper()
		var count int
		if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE event_type = ?`, eventType).Scan(&count); err != nil {
			t.Fatalf("count %s events: %v", eventType, err)
		}
		if count != want {
			t.Fatalf("%s events = %d, want %d", eventType, count, want)
		}
	}
	assertEventCount("sla.warning", 1)
	// Re-running the same warning must not emit twice.
	if err := fixture.engine.RunJob(context.Background(), job); err != nil {
		t.Fatalf("RunJob warning repeat: %v", err)
	}
	assertEventCount("sla.warning", 1)
}

func TestWarningThresholdNotArmedWhenNoThresholdConfigured(t *testing.T) {
	fixture := newEngineFixture(t)
	fixture.engine.SetClock(fixedClock{now: time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)})
	fixture.createMetric(t,
		[]models.SLACondition{{Phase: models.SLAPhaseStart, Position: 0, ConditionType: conditionCreated, Config: json.RawMessage(`{}`)}},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())
	var warnings int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM sla_jobs WHERE kind = 'warning'`).Scan(&warnings); err != nil {
		t.Fatalf("count warning jobs: %v", err)
	}
	if warnings != 0 {
		t.Fatalf("warning jobs = %d, want 0 without a configured threshold", warnings)
	}
}

func TestWarningJobInvokesSideEffectEmitter(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)
	effects := &recordingSideEffects{}
	fixture.engine.SetSideEffectEmitter(effects)
	fixture.addWarningThreshold(t, 75, nil)
	fixture.createMetric(t,
		[]models.SLACondition{{Phase: models.SLAPhaseStart, Position: 0, ConditionType: conditionCreated, Config: json.RawMessage(`{}`)}},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())

	var warningID, cycleID int64
	var dueAt, deadline time.Time
	if err := fixture.db.QueryRow(`SELECT id, cycle_id, due_at, deadline_at FROM sla_jobs WHERE kind = 'warning'`).Scan(&warningID, &cycleID, &dueAt, &deadline); err != nil {
		t.Fatalf("load warning job: %v", err)
	}
	clock.now = dueAt.Add(time.Second)
	if err := fixture.engine.RunJob(context.Background(), models.SLAJob{
		ID: warningID, Kind: models.SLAJobWarning, CycleID: &cycleID, DueAt: dueAt, DeadlineAt: &deadline,
	}); err != nil {
		t.Fatalf("RunJob warning: %v", err)
	}
	if effects.warnings != 1 {
		t.Fatalf("warning side effects = %d, want 1", effects.warnings)
	}
}

// A first-response metric stops on the first public agent comment. This is the
// helpdesk pack's definition: a ticket replied to by an agent is satisfied even
// when it was never assigned.
func TestCommentByAgentStopsCycle(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)
	fixture.createMetric(t,
		[]models.SLACondition{
			{Phase: models.SLAPhaseStart, Position: 0, ConditionType: conditionCreated, Config: json.RawMessage(`{}`)},
			{Phase: models.SLAPhaseStop, Position: 1, ConditionType: conditionCommentByAgent, Config: json.RawMessage(`{}`)},
		},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())

	var status string
	if err := fixture.db.QueryRow(`SELECT status FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&status); err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if status != models.SLACycleOngoing {
		t.Fatalf("cycle status = %s, want ongoing before any reply", status)
	}

	clock.now = now.Add(5 * time.Minute)
	fixture.observe(t, fixture.commentFact("user", false))

	var stoppedAt time.Time
	var elapsed int64
	if err := fixture.db.QueryRow(`SELECT status, stopped_at, elapsed_ms FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&status, &stoppedAt, &elapsed); err != nil {
		t.Fatalf("reload cycle: %v", err)
	}
	if status != models.SLACycleCompleted {
		t.Fatalf("cycle status = %s, want completed after a public agent comment", status)
	}
	if elapsed != (5 * time.Minute).Milliseconds() {
		t.Fatalf("elapsed = %d, want %d", elapsed, (5*time.Minute).Milliseconds())
	}
	if !stoppedAt.Equal(clock.now) {
		t.Fatalf("stopped_at = %s, want %s", stoppedAt, clock.now)
	}
	if jobs := fixture.breachJobCount(t); jobs != 0 {
		t.Fatalf("breach jobs after completion = %d, want 0", jobs)
	}
}

// A private note is not a response to the customer, so it must leave the
// first-response clock running even though an agent wrote it.
func TestPrivateAgentCommentDoesNotStopCycle(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)
	fixture.createMetric(t,
		[]models.SLACondition{
			{Phase: models.SLAPhaseStart, Position: 0, ConditionType: conditionCreated, Config: json.RawMessage(`{}`)},
			{Phase: models.SLAPhaseStop, Position: 1, ConditionType: conditionCommentByAgent, Config: json.RawMessage(`{}`)},
		},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())

	clock.now = now.Add(5 * time.Minute)
	fixture.observe(t, fixture.commentFact("user", true))

	var status string
	var stoppedAt sql.NullTime
	if err := fixture.db.QueryRow(`SELECT status, stopped_at FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&status, &stoppedAt); err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if status != models.SLACycleOngoing || stoppedAt.Valid {
		t.Fatalf("cycle = status:%s stopped:%v, want ongoing with no stop after a private note", status, stoppedAt.Valid)
	}

	// The next public comment still stops it.
	clock.now = now.Add(10 * time.Minute)
	fixture.observe(t, fixture.commentFact("user", false))
	if err := fixture.db.QueryRow(`SELECT status FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&status); err != nil {
		t.Fatalf("reload cycle: %v", err)
	}
	if status != models.SLACycleCompleted {
		t.Fatalf("cycle status = %s, want completed after the following public comment", status)
	}
}
