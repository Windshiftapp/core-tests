//go:build test

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

type slaFixture struct {
	db          database.Database
	repo        *SLARepository
	workspaceID int
	itemID      int
	calendarID  int
}

func newSLAFixture(t *testing.T) *slaFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('SLA workspace', 'SLAT') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	var itemID int
	if err := db.QueryRow(`INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index) VALUES (?, 1, 'Item', '', ?) RETURNING id`, workspaceID, testutils.NextTestFracIndex()).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	repo := NewSLARepository(db)
	calendarID, err := repo.CreateCalendar(context.Background(), &models.WorkingCalendar{
		WorkspaceID:     &workspaceID,
		Name:            "Service hours",
		Timezone:        "UTC",
		WeeklyIntervals: json.RawMessage(`{"monday":[{"start":"09:00","end":"17:00"}]}`),
	})
	if err != nil {
		t.Fatalf("create calendar: %v", err)
	}
	return &slaFixture{db: db, repo: repo, workspaceID: workspaceID, itemID: itemID, calendarID: calendarID}
}

func (f *slaFixture) createMetric(t *testing.T) int {
	t.Helper()
	var metricID int
	err := database.WithTx(f.db, func(tx database.Tx) error {
		id, err := f.repo.CreateMetric(context.Background(), tx, &models.SLAMetric{
			WorkspaceID: f.workspaceID, Name: "First response", DisplayFormat: "time", IsActive: true, ImportStatus: "native",
			Conditions: []models.SLACondition{{Phase: models.SLAPhaseStart, Position: 0, ConditionType: "created", Config: json.RawMessage(`{}`)}},
			Goals: []models.SLAGoal{{
				Position: 0, QLQuery: "priority = high", ImportStatus: "native",
				Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 3_600_000, CalendarID: f.calendarID}},
			}},
		})
		metricID = id
		return err
	})
	if err != nil {
		t.Fatalf("create metric: %v", err)
	}
	return metricID
}

func TestSLAMetricRoundTrip(t *testing.T) {
	fixture := newSLAFixture(t)
	metricID := fixture.createMetric(t)

	metric, err := fixture.repo.GetMetric(context.Background(), metricID)
	if err != nil {
		t.Fatalf("GetMetric: %v", err)
	}
	if metric.Name != "First response" || metric.WorkspaceID != fixture.workspaceID || !metric.IsActive {
		t.Fatalf("metric = %#v", metric)
	}
	if len(metric.Conditions) != 1 || metric.Conditions[0].ConditionType != "created" {
		t.Fatalf("conditions = %#v", metric.Conditions)
	}
	if len(metric.Goals) != 1 || metric.Goals[0].QLQuery != "priority = high" {
		t.Fatalf("goals = %#v", metric.Goals)
	}
	if len(metric.Goals[0].Targets) != 1 || metric.Goals[0].Targets[0].TargetMs != 3_600_000 {
		t.Fatalf("targets = %#v", metric.Goals[0].Targets)
	}
	if metric.Goals[0].Targets[0].CalendarID != fixture.calendarID {
		t.Fatalf("target calendar = %d, want %d", metric.Goals[0].Targets[0].CalendarID, fixture.calendarID)
	}
}

func TestSLAJobUpsertIsIdempotentPerSubject(t *testing.T) {
	fixture := newSLAFixture(t)
	ctx := context.Background()

	job := &models.SLAJob{Kind: models.SLAJobRecalcItem, ItemID: &fixture.itemID, DueAt: time.Now().Add(time.Hour)}
	if err := database.WithTx(fixture.db, func(tx database.Tx) error {
		return fixture.repo.UpsertJob(ctx, tx, job)
	}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Re-arming the same subject must update the row, not accumulate one.
	newDue := time.Now().Add(2 * time.Hour)
	err := database.WithTx(fixture.db, func(tx database.Tx) error {
		return fixture.repo.UpsertJob(ctx, tx, &models.SLAJob{Kind: models.SLAJobRecalcItem, ItemID: &fixture.itemID, DueAt: newDue})
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	var count int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM sla_jobs`).Scan(&count); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 1 {
		t.Fatalf("job count = %d, want 1", count)
	}
	next, ok, err := fixture.repo.NextDueAt(ctx)
	if err != nil || !ok {
		t.Fatalf("NextDueAt = %v/%v", next, err)
	}
	if next.Sub(newDue) > time.Second || next.Sub(newDue) < -time.Second {
		t.Fatalf("next due = %s, want ~%s", next, newDue)
	}
}

func TestSLAJobClaimLeasesDueJobs(t *testing.T) {
	fixture := newSLAFixture(t)
	ctx := context.Background()
	now := time.Now()

	err := database.WithTx(fixture.db, func(tx database.Tx) error {
		return fixture.repo.UpsertJob(ctx, tx, &models.SLAJob{Kind: models.SLAJobRecalcItem, ItemID: &fixture.itemID, DueAt: now.Add(-time.Minute)})
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	jobs, err := fixture.repo.ClaimDueJobs(ctx, now, time.Minute, "replica-a", 50)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("claimed %d jobs, want 1", len(jobs))
	}
	if jobs[0].Attempts != 1 || jobs[0].LeaseOwner == nil || *jobs[0].LeaseOwner != "replica-a" {
		t.Fatalf("claimed job = %#v", jobs[0])
	}

	// The lease pushes due_at into the future, so a second claim finds nothing.
	second, err := fixture.repo.ClaimDueJobs(ctx, now, time.Minute, "replica-b", 50)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second claim returned %d jobs, want 0 while leased", len(second))
	}
}

func TestSLAJobFailAndReplay(t *testing.T) {
	fixture := newSLAFixture(t)
	ctx := context.Background()
	now := time.Now()
	metricID := fixture.createMetric(t)

	if err := database.WithTx(fixture.db, func(tx database.Tx) error {
		return fixture.repo.UpsertJob(ctx, tx, &models.SLAJob{Kind: models.SLAJobRecalcMetric, MetricID: &metricID, DueAt: now})
	}); err != nil {
		t.Fatalf("upsert metric job: %v", err)
	}

	jobs, err := fixture.repo.ClaimDueJobs(ctx, now.Add(time.Second), time.Minute, "replica-a", 50)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	var metricJobID int64
	for _, job := range jobs {
		if job.Kind == models.SLAJobRecalcMetric {
			metricJobID = job.ID
		}
	}
	if metricJobID == 0 {
		t.Fatalf("no recalc_metric job claimed: %#v", jobs)
	}
	if err := fixture.repo.FailJob(ctx, metricJobID, "boom"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	failed, err := fixture.repo.ListFailedJobs(ctx, 10)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(failed) != 1 || failed[0].LastError == nil || *failed[0].LastError != "boom" {
		t.Fatalf("failed jobs = %#v", failed)
	}
	if _, ok, err := fixture.repo.NextDueAt(ctx); err != nil || ok {
		t.Fatalf("failed job should be excluded from the due scan: ok=%v err=%v", ok, err)
	}
	if err := fixture.repo.ReplayJob(ctx, metricJobID, now); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if _, ok, err := fixture.repo.NextDueAt(ctx); err != nil || !ok {
		t.Fatalf("replayed job should be pending: ok=%v err=%v", ok, err)
	}
}

func TestSLADeleteCalendarRefusesWhenGoalTargetReferencesIt(t *testing.T) {
	fixture := newSLAFixture(t)
	fixture.createMetric(t)

	err := fixture.repo.DeleteCalendar(context.Background(), fixture.calendarID)
	if !errors.Is(err, ErrSLAInUse) {
		t.Fatalf("DeleteCalendar error = %v, want ErrSLAInUse", err)
	}
	var count int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM working_calendars WHERE id = ?`, fixture.calendarID).Scan(&count); err != nil {
		t.Fatalf("count calendars: %v", err)
	}
	if count != 1 {
		t.Fatalf("calendar was deleted despite an active target")
	}
}

func TestTeamBindingDeleteRefusesWhenTeamCalendarTargeted(t *testing.T) {
	fixture := newSLAFixture(t)
	ctx := context.Background()

	var teamID int
	if err := fixture.db.QueryRow(`INSERT INTO teams (name) VALUES ('Service desk') RETURNING id`).Scan(&teamID); err != nil {
		t.Fatalf("insert team: %v", err)
	}
	teamCalendarID, err := fixture.repo.CreateCalendar(ctx, &models.WorkingCalendar{
		TeamID: &teamID, Name: "Team hours", Timezone: "UTC",
		WeeklyIntervals: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create team calendar: %v", err)
	}
	bindingID, err := fixture.repo.CreateTeamWorkspaceBinding(ctx, &models.TeamWorkspaceBinding{TeamID: teamID, WorkspaceID: fixture.workspaceID})
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	err = database.WithTx(fixture.db, func(tx database.Tx) error {
		_, err := fixture.repo.CreateMetric(ctx, tx, &models.SLAMetric{
			WorkspaceID: fixture.workspaceID, Name: "Bound", IsActive: true, ImportStatus: "native",
			Goals: []models.SLAGoal{{Position: 0, QLQuery: "true", ImportStatus: "native",
				Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 1000, CalendarID: teamCalendarID}}}},
		})
		return err
	})
	if err != nil {
		t.Fatalf("create metric: %v", err)
	}

	err = fixture.repo.DeleteTeamWorkspaceBinding(ctx, fixture.workspaceID, bindingID)
	if !errors.Is(err, ErrSLAInUse) {
		t.Fatalf("DeleteTeamWorkspaceBinding error = %v, want ErrSLAInUse", err)
	}
}

func TestTeamWorkspaceBindingDeleteRemovesUnreferencedBinding(t *testing.T) {
	fixture := newSLAFixture(t)
	ctx := context.Background()
	var teamID int
	if err := fixture.db.QueryRow(`INSERT INTO teams (name) VALUES ('Unused') RETURNING id`).Scan(&teamID); err != nil {
		t.Fatalf("insert team: %v", err)
	}
	bindingID, err := fixture.repo.CreateTeamWorkspaceBinding(ctx, &models.TeamWorkspaceBinding{TeamID: teamID, WorkspaceID: fixture.workspaceID})
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	if err := fixture.repo.DeleteTeamWorkspaceBinding(ctx, fixture.workspaceID, bindingID); err != nil {
		t.Fatalf("delete binding: %v", err)
	}
	var count int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM team_workspace_bindings`).Scan(&count); err != nil {
		t.Fatalf("count bindings: %v", err)
	}
	if count != 0 {
		t.Fatalf("binding count = %d, want 0", count)
	}
}

func TestTouchWorkspaceStateAdvancesGeneration(t *testing.T) {
	fixture := newSLAFixture(t)
	ctx := context.Background()
	touch := func() int {
		t.Helper()
		var generation int
		if err := database.WithTx(fixture.db, func(tx database.Tx) error {
			var err error
			generation, err = fixture.repo.TouchWorkspaceState(ctx, tx, fixture.workspaceID)
			return err
		}); err != nil {
			t.Fatalf("TouchWorkspaceState: %v", err)
		}
		return generation
	}

	if generation := touch(); generation != 1 {
		t.Fatalf("first generation = %d, want 1", generation)
	}
	if generation := touch(); generation != 2 {
		t.Fatalf("second generation = %d, want 2", generation)
	}
	loaded, err := fixture.repo.WorkspaceGeneration(ctx, fixture.workspaceID)
	if err != nil {
		t.Fatalf("WorkspaceGeneration: %v", err)
	}
	if configured, err := fixture.repo.HasConfiguration(ctx); err != nil || !configured {
		t.Fatalf("HasConfiguration = %v/%v, want true/nil", configured, err)
	}
	if loaded != 2 {
		t.Fatalf("loaded generation = %d, want 2", loaded)
	}
	if missing, err := fixture.repo.WorkspaceGeneration(ctx, 9999); err != nil || missing != 0 {
		t.Fatalf("missing workspace generation = %d/%v, want 0/nil", missing, err)
	}
}

// TestListRecalculationJobsScopesToWorkspace covers the recalculation tab
// read path, which joins sla_metrics and items to scope jobs to a workspace.
func TestListRecalculationJobsScopesToWorkspace(t *testing.T) {
	fixture := newSLAFixture(t)
	ctx := context.Background()
	metricID := fixture.createMetric(t)
	now := time.Now().UTC()

	var otherWorkspaceID int
	if err := fixture.db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Other workspace', 'SLAO') RETURNING id`).Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("insert other workspace: %v", err)
	}
	var otherItemID int
	if err := fixture.db.QueryRow(`INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index) VALUES (?, 1, 'Other item', '', ?) RETURNING id`, otherWorkspaceID, testutils.NextTestFracIndex()).Scan(&otherItemID); err != nil {
		t.Fatalf("insert other item: %v", err)
	}

	if err := database.WithTx(fixture.db, func(tx database.Tx) error {
		if err := fixture.repo.UpsertJob(ctx, tx, &models.SLAJob{Kind: models.SLAJobRecalcMetric, MetricID: &metricID, DueAt: now}); err != nil {
			return err
		}
		if err := fixture.repo.UpsertJob(ctx, tx, &models.SLAJob{Kind: models.SLAJobRecalcItem, ItemID: &fixture.itemID, DueAt: now.Add(time.Minute)}); err != nil {
			return err
		}
		if err := fixture.repo.UpsertJob(ctx, tx, &models.SLAJob{Kind: models.SLAJobRecalcCalendar, ThresholdKey: fmt.Sprintf("%d", fixture.calendarID), MetricID: &metricID, DueAt: now.Add(2 * time.Minute)}); err != nil {
			return err
		}
		return fixture.repo.UpsertJob(ctx, tx, &models.SLAJob{Kind: models.SLAJobRecalcItem, ItemID: &otherItemID, DueAt: now.Add(3 * time.Minute)})
	}); err != nil {
		t.Fatalf("upsert jobs: %v", err)
	}

	jobs, err := fixture.repo.ListRecalculationJobs(ctx, fixture.workspaceID)
	if err != nil {
		t.Fatalf("ListRecalculationJobs: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("jobs = %#v, want 3", jobs)
	}
	if jobs[0].Kind != models.SLAJobRecalcMetric || jobs[0].MetricID == nil || *jobs[0].MetricID != metricID {
		t.Fatalf("first job = %#v, want recalc_metric for metric %d", jobs[0], metricID)
	}
	if jobs[1].Kind != models.SLAJobRecalcItem || jobs[1].ItemID == nil || *jobs[1].ItemID != fixture.itemID {
		t.Fatalf("second job = %#v, want recalc_item for item %d", jobs[1], fixture.itemID)
	}
	if jobs[2].Kind != models.SLAJobRecalcCalendar || jobs[2].MetricID == nil || *jobs[2].MetricID != metricID {
		t.Fatalf("third job = %#v, want recalc_calendar for metric %d", jobs[2], metricID)
	}
}

// TestSLAMetricUpdatePreservesCycleGoalLink guards against editing a metric
// deleting and recreating its goals, which nulled the goal link on every cycle.
func TestSLAMetricUpdatePreservesCycleGoalLink(t *testing.T) {
	fixture := newSLAFixture(t)
	ctx := context.Background()
	metricID := fixture.createMetric(t)

	metric, err := fixture.repo.GetMetric(ctx, metricID)
	if err != nil {
		t.Fatalf("GetMetric: %v", err)
	}
	goalID := metric.Goals[0].ID
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)

	fixture.insertCycle(t, &models.ItemSLACycle{
		ItemID: fixture.itemID, MetricID: metricID, GoalID: &goalID, CalendarID: &fixture.calendarID,
		CycleNo: 1, Status: models.SLACycleOngoing, StartedAt: now.Add(-time.Hour), LastCalculatedAt: now,
		GoalDurationMs: 3_600_000, RemainingMs: 1_800_000, Origin: models.SLAOriginNative,
		CalendarSnapshot: json.RawMessage(`{}`), GoalQuerySnapshot: "1 = 1",
	})
	completed := completedCycle(fixture.itemID, metricID, 2, false, now)
	completed.GoalID = &goalID
	fixture.insertCycle(t, completed)

	// Change a target duration, which previously rewrote every goal row.
	metric.Goals[0].Targets[0].TargetMs = 7_200_000
	if err := database.WithTx(fixture.db, func(tx database.Tx) error {
		return fixture.repo.UpdateMetric(ctx, tx, metric)
	}); err != nil {
		t.Fatalf("UpdateMetric: %v", err)
	}

	reloaded, err := fixture.repo.GetMetric(ctx, metricID)
	if err != nil {
		t.Fatalf("GetMetric after update: %v", err)
	}
	if reloaded.Goals[0].ID != goalID {
		t.Fatalf("goal id = %d, want %d", reloaded.Goals[0].ID, goalID)
	}
	if got := reloaded.Goals[0].Targets[0].TargetMs; got != 7_200_000 {
		t.Fatalf("target ms = %d, want 7200000", got)
	}

	cycles, err := fixture.repo.ListCyclesForItem(ctx, fixture.itemID)
	if err != nil {
		t.Fatalf("ListCyclesForItem: %v", err)
	}
	if len(cycles) != 2 {
		t.Fatalf("cycles = %d, want 2", len(cycles))
	}
	for i := range cycles {
		if cycles[i].GoalID == nil || *cycles[i].GoalID != goalID {
			t.Fatalf("cycle %d goal id = %v, want %d", cycles[i].CycleNo, cycles[i].GoalID, goalID)
		}
	}
}

// TestSLAReportExcludesGoalLessCompletedCycles guards against cycles without a
// matching goal inflating the completed count and dragging the average down.
func TestSLAReportExcludesGoalLessCompletedCycles(t *testing.T) {
	fixture := newSLAFixture(t)
	metricID := fixture.createMetric(t)
	stopped := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)

	goalLess := completedCycle(fixture.itemID, metricID, 1, false, stopped)
	goalLess.GoalDurationMs = 0
	fixture.insertCycle(t, goalLess)
	fixture.insertCycle(t, completedCycle(fixture.itemID, metricID, 2, false, stopped.Add(time.Hour)))

	report, err := fixture.repo.SLAReport(context.Background(), fixture.workspaceID, nil, nil)
	if err != nil {
		t.Fatalf("SLAReport: %v", err)
	}
	if len(report.Metrics) != 1 {
		t.Fatalf("report metrics = %#v", report.Metrics)
	}
	metric := report.Metrics[0]
	if metric.Completed != 1 {
		t.Fatalf("completed = %d, want 1 (goal-less cycle excluded)", metric.Completed)
	}
	if metric.AvgGoalMs == nil || *metric.AvgGoalMs != 1_000 {
		t.Fatalf("avg goal = %v, want 1000", metric.AvgGoalMs)
	}
}

// TestSLADurationsAcceptLongTargets guards the PostgreSQL int4 overflow: a
// 30-day target and a cycle open longer than 24.8 days must round-trip.
func TestSLADurationsAcceptLongTargets(t *testing.T) {
	fixture := newSLAFixture(t)
	ctx := context.Background()
	const thirtyDaysMs = int64(30 * 24 * 60 * 60 * 1000)

	metricID := 0
	if err := database.WithTx(fixture.db, func(tx database.Tx) error {
		id, err := fixture.repo.CreateMetric(ctx, tx, &models.SLAMetric{
			WorkspaceID: fixture.workspaceID, Name: "Long target", DisplayFormat: "time", IsActive: true, ImportStatus: "native",
			Goals: []models.SLAGoal{{Position: 0, QLQuery: "true", ImportStatus: "native",
				Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: thirtyDaysMs, CalendarID: fixture.calendarID}}}},
		})
		metricID = id
		return err
	}); err != nil {
		t.Fatalf("CreateMetric: %v", err)
	}
	metric, err := fixture.repo.GetMetric(ctx, metricID)
	if err != nil {
		t.Fatalf("GetMetric: %v", err)
	}
	if got := metric.Goals[0].Targets[0].TargetMs; got != thirtyDaysMs {
		t.Fatalf("target ms = %d, want %d", got, thirtyDaysMs)
	}

	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	cycleID := fixture.insertCycle(t, &models.ItemSLACycle{
		ItemID: fixture.itemID, MetricID: metricID, CycleNo: 1, Status: models.SLACycleOngoing,
		StartedAt: now.Add(-time.Duration(thirtyDaysMs) * time.Millisecond), LastCalculatedAt: now,
		GoalDurationMs: thirtyDaysMs, ElapsedMs: thirtyDaysMs - 1, RemainingMs: 1,
		Origin: models.SLAOriginNative, CalendarSnapshot: json.RawMessage(`{}`), GoalQuerySnapshot: "1 = 1",
	})
	loaded, err := fixture.repo.GetCycle(ctx, cycleID)
	if err != nil {
		t.Fatalf("GetCycle: %v", err)
	}
	if loaded.GoalDurationMs != thirtyDaysMs || loaded.ElapsedMs != thirtyDaysMs-1 {
		t.Fatalf("cycle durations = %d/%d, want %d/%d", loaded.GoalDurationMs, loaded.ElapsedMs, thirtyDaysMs, thirtyDaysMs-1)
	}
}

// TestDeleteImportedCyclesNotInHandlesLargeKeepList guards against building an
// unbounded NOT IN list; the placeholder count exceeded the engine's variable
// limit for a large import even when few rows existed.
func TestDeleteImportedCyclesNotInHandlesLargeKeepList(t *testing.T) {
	fixture := newSLAFixture(t)
	ctx := context.Background()
	metricID := fixture.createMetric(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)

	insert := func(cycleNo int, sourceID *string) {
		fixture.insertCycle(t, &models.ItemSLACycle{
			ItemID: fixture.itemID, MetricID: metricID, CycleNo: cycleNo, Status: models.SLACycleCompleted,
			StartedAt: now.Add(-time.Hour), StoppedAt: &now, LastCalculatedAt: now,
			GoalDurationMs: 1_000, ElapsedMs: 1_000, RemainingMs: 0,
			Origin: models.SLAOriginImport, SourceID: sourceID,
			CalendarSnapshot: json.RawMessage(`{}`), GoalQuerySnapshot: "1 = 1",
		})
	}
	keepOne, keepTwo := "keep-1", "keep-2"
	insert(1, &keepOne)
	insert(2, &keepTwo)
	stale := "stale-1"
	insert(3, &stale)
	insert(4, nil)

	// Exceed SQLite's 32766 and PostgreSQL's 65535 bind-variable limits.
	phantomCount := 33_000
	if testutils.IsPostgres() {
		phantomCount = 66_000
	}
	keep := make([]string, 0, phantomCount+2)
	for i := 0; i < phantomCount; i++ {
		keep = append(keep, fmt.Sprintf("phantom-%d", i))
	}
	keep = append(keep, keepOne, keepTwo)

	if err := database.WithTx(fixture.db, func(tx database.Tx) error {
		return fixture.repo.DeleteImportedCyclesNotIn(ctx, tx, metricID, keep)
	}); err != nil {
		t.Fatalf("DeleteImportedCyclesNotIn: %v", err)
	}

	rows, err := fixture.db.Query(`SELECT source_id FROM item_sla_cycles WHERE metric_id = ? ORDER BY cycle_no`, metricID)
	if err != nil {
		t.Fatalf("list remaining cycles: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var remaining []string
	for rows.Next() {
		var sourceID string
		if err := rows.Scan(&sourceID); err != nil {
			t.Fatalf("scan remaining cycle: %v", err)
		}
		remaining = append(remaining, sourceID)
	}
	if len(remaining) != 2 || remaining[0] != keepOne || remaining[1] != keepTwo {
		t.Fatalf("remaining imported cycles = %v, want [%s %s]", remaining, keepOne, keepTwo)
	}
}

// TestClaimDueJobsSkipsRowsLockedByAnotherWorker reproduces the concurrent
// claim on PostgreSQL: a job held by another transaction must be skipped, not
// blocked on and then claimed. SQLite serializes writers, so it is excluded.
func TestClaimDueJobsSkipsRowsLockedByAnotherWorker(t *testing.T) {
	if !testutils.IsPostgres() {
		t.Skip("SKIP LOCKED concurrency only applies to PostgreSQL")
	}
	fixture := newSLAFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := database.WithTx(fixture.db, func(tx database.Tx) error {
		return fixture.repo.UpsertJob(ctx, tx, &models.SLAJob{Kind: models.SLAJobRecalcItem, ItemID: &fixture.itemID, DueAt: now.Add(-time.Minute)})
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Another worker holds the row lock.
	tx, err := fixture.db.Begin()
	if err != nil {
		t.Fatalf("begin locker tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	var lockedID int64
	if err := tx.QueryRow(`SELECT id FROM sla_jobs WHERE state = 'pending' FOR UPDATE`).Scan(&lockedID); err != nil {
		t.Fatalf("lock job: %v", err)
	}

	claimCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	jobs, err := fixture.repo.ClaimDueJobs(claimCtx, now, time.Minute, "replica-b", 50)
	if err != nil {
		t.Fatalf("claim while locked: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("claimed a job another worker holds: %#v", jobs)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("release lock: %v", err)
	}
	jobs, err = fixture.repo.ClaimDueJobs(ctx, now, time.Minute, "replica-b", 50)
	if err != nil {
		t.Fatalf("claim after release: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != lockedID {
		t.Fatalf("claimed after release = %#v, want job %d", jobs, lockedID)
	}
}

// TestTeamReferencedCalendarsCountsBoundTargets backs the team-delete guard
// that turns a raw calendar FK failure into a clean conflict.
func TestTeamReferencedCalendarsCountsBoundTargets(t *testing.T) {
	fixture := newSLAFixture(t)
	ctx := context.Background()

	var teamID int
	if err := fixture.db.QueryRow(`INSERT INTO teams (name) VALUES ('Service desk') RETURNING id`).Scan(&teamID); err != nil {
		t.Fatalf("insert team: %v", err)
	}
	teamCalendarID, err := fixture.repo.CreateCalendar(ctx, &models.WorkingCalendar{
		TeamID: &teamID, Name: "Team hours", Timezone: "UTC", WeeklyIntervals: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create team calendar: %v", err)
	}
	if count, err := fixture.repo.TeamReferencedCalendars(ctx, teamID); err != nil || count != 0 {
		t.Fatalf("unreferenced count = %d/%v, want 0/nil", count, err)
	}
	if err := database.WithTx(fixture.db, func(tx database.Tx) error {
		_, err := fixture.repo.CreateMetric(ctx, tx, &models.SLAMetric{
			WorkspaceID: fixture.workspaceID, Name: "Bound", IsActive: true, ImportStatus: "native",
			Goals: []models.SLAGoal{{Position: 0, QLQuery: "true", ImportStatus: "native",
				Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 1_000, CalendarID: teamCalendarID}}}},
		})
		return err
	}); err != nil {
		t.Fatalf("create metric: %v", err)
	}
	if count, err := fixture.repo.TeamReferencedCalendars(ctx, teamID); err != nil || count != 1 {
		t.Fatalf("referenced count = %d/%v, want 1/nil", count, err)
	}
}

// TestListMetricSummariesSkipsConfiguration pins the badge read path: summaries
// carry metric identity fields but never hydrate conditions, goals, or targets,
// so badge batches do not pay the 1+2M+G configuration queries per chunk.
func TestListMetricSummariesSkipsConfiguration(t *testing.T) {
	fixture := newSLAFixture(t)
	ctx := context.Background()
	metricID := fixture.createMetric(t)

	hydrated, err := fixture.repo.ListMetrics(ctx, fixture.workspaceID)
	if err != nil {
		t.Fatalf("ListMetrics: %v", err)
	}
	if len(hydrated) != 1 {
		t.Fatalf("ListMetrics returned %d metrics, want 1", len(hydrated))
	}
	if len(hydrated[0].Conditions) == 0 || len(hydrated[0].Goals) == 0 {
		t.Fatalf("ListMetrics did not hydrate configuration: %+v", hydrated[0])
	}

	summaries, err := fixture.repo.ListMetricSummaries(ctx, fixture.workspaceID)
	if err != nil {
		t.Fatalf("ListMetricSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("ListMetricSummaries returned %d metrics, want 1", len(summaries))
	}
	got := summaries[0]
	if got.ID != metricID || got.Name != "First response" || got.DisplayFormat != "time" {
		t.Fatalf("summary identity = %+v, want id=%d name=First response format=time", got, metricID)
	}
	if len(got.Conditions) != 0 || len(got.Goals) != 0 {
		t.Fatalf("summary hydrated unused configuration: %+v", got)
	}
}
