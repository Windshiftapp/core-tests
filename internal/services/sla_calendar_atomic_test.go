//go:build test

package services_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/sla"
	"windshift/internal/testutils"
)

func TestCalendarUpdateRollsBackWhenRecalculationEnqueueFails(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('SLA calendar atomic', 'SCAA') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	repo := repository.NewSLARepository(db)
	calendarID, err := repo.CreateCalendar(context.Background(), &models.WorkingCalendar{
		WorkspaceID: &workspaceID, Name: "Original", Timezone: "UTC",
		WeeklyIntervals: json.RawMessage(`{"monday":[{"start":"09:00","end":"17:00"}]}`),
		Holidays:        json.RawMessage("[]"),
	})
	if err != nil {
		t.Fatalf("create calendar: %v", err)
	}
	engine := sla.NewEngine(db)
	metricService := services.NewSLAMetricService(db, engine)
	_, err = metricService.Create(context.Background(), workspaceID, services.SLAMetricInput{
		Name: "Calendar target", IsActive: slaCalendarBoolPointer(true),
		Goals: []models.SLAGoal{{Position: 0, QLQuery: "1 = 1", Targets: []models.SLAGoalTarget{{
			Position: 0, TargetMs: 3_600_000, CalendarID: calendarID,
		}}}},
	})
	if err != nil {
		t.Fatalf("create metric: %v", err)
	}
	var generationBefore int
	if err := db.QueryRow(`SELECT config_generation FROM sla_workspace_state WHERE workspace_id = ?`, workspaceID).Scan(&generationBefore); err != nil {
		t.Fatalf("load initial config generation: %v", err)
	}
	if err := injectCalendarEnqueueFailure(db); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	calendarService := services.NewSLACalendarService(db, engine)
	_, err = calendarService.UpdateWorkspace(context.Background(), workspaceID, calendarID, services.SLACalendarInput{
		Name: "Changed", Timezone: "Europe/Berlin", WeeklyIntervals: json.RawMessage(`{"monday":[{"start":"10:00","end":"18:00"}]}`),
		Holidays: json.RawMessage("[]"), ApplyToOngoing: true,
	})
	if err == nil || !strings.Contains(err.Error(), "enqueue failure") {
		t.Fatalf("UpdateWorkspace error = %v, want the injected enqueue failure", err)
	}
	var name, timezone string
	if err := db.QueryRow(`SELECT name, timezone FROM working_calendars WHERE id = ?`, calendarID).Scan(&name, &timezone); err != nil {
		t.Fatalf("reload calendar: %v", err)
	}
	if name != "Original" || timezone != "UTC" {
		t.Fatalf("calendar after failed update = %q/%q, want Original/UTC", name, timezone)
	}
	var generationAfter int
	if err := db.QueryRow(`SELECT config_generation FROM sla_workspace_state WHERE workspace_id = ?`, workspaceID).Scan(&generationAfter); err != nil {
		t.Fatalf("load config generation after failed update: %v", err)
	}
	if generationAfter != generationBefore {
		t.Fatalf("config generation after failed update = %d, want %d", generationAfter, generationBefore)
	}
}

func TestTeamCalendarUpdateInvalidatesBoundWorkspaceConfigurations(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	var firstWorkspaceID, secondWorkspaceID, teamID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('First SLA workspace', 'FSLW') RETURNING id`).Scan(&firstWorkspaceID); err != nil {
		t.Fatalf("insert first workspace: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Second SLA workspace', 'SSLW') RETURNING id`).Scan(&secondWorkspaceID); err != nil {
		t.Fatalf("insert second workspace: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO teams (name) VALUES ('Shared SLA team') RETURNING id`).Scan(&teamID); err != nil {
		t.Fatalf("insert team: %v", err)
	}
	repo := repository.NewSLARepository(db)
	for _, workspaceID := range []int{firstWorkspaceID, secondWorkspaceID} {
		if _, err := repo.CreateTeamWorkspaceBinding(context.Background(), &models.TeamWorkspaceBinding{TeamID: teamID, WorkspaceID: workspaceID}); err != nil {
			t.Fatalf("bind workspace %d: %v", workspaceID, err)
		}
	}
	engine := sla.NewEngine(db)
	calendarService := services.NewSLACalendarService(db, engine)
	calendar, err := calendarService.CreateTeam(context.Background(), teamID, services.SLACalendarInput{
		Name: "Shared hours", Timezone: "UTC", WeeklyIntervals: json.RawMessage(`{"monday":[{"start":"09:00","end":"17:00"}]}`),
	})
	if err != nil {
		t.Fatalf("create team calendar: %v", err)
	}
	metricService := services.NewSLAMetricService(db, engine)
	metricIDs := make([]int, 0, 2)
	for _, workspaceID := range []int{firstWorkspaceID, secondWorkspaceID} {
		metric, err := metricService.Create(context.Background(), workspaceID, services.SLAMetricInput{
			Name: "Shared calendar metric", IsActive: slaCalendarBoolPointer(true),
			Goals: []models.SLAGoal{{Position: 0, QLQuery: "1 = 1", Targets: []models.SLAGoalTarget{{
				Position: 0, TargetMs: 3_600_000, CalendarID: calendar.ID,
			}}}},
		})
		if err != nil {
			t.Fatalf("create metric for workspace %d: %v", workspaceID, err)
		}
		metricIDs = append(metricIDs, metric.ID)
	}
	var firstGeneration, secondGeneration int
	if err := db.QueryRow(`SELECT config_generation FROM sla_workspace_state WHERE workspace_id = ?`, firstWorkspaceID).Scan(&firstGeneration); err != nil {
		t.Fatalf("load first generation: %v", err)
	}
	if err := db.QueryRow(`SELECT config_generation FROM sla_workspace_state WHERE workspace_id = ?`, secondWorkspaceID).Scan(&secondGeneration); err != nil {
		t.Fatalf("load second generation: %v", err)
	}
	_, err = calendarService.UpdateTeam(context.Background(), teamID, calendar.ID, services.SLACalendarInput{
		Name: "Shared hours", Timezone: "Europe/Berlin", WeeklyIntervals: json.RawMessage(`{"monday":[{"start":"10:00","end":"18:00"}]}`), ApplyToOngoing: true,
	})
	if err != nil {
		t.Fatalf("update team calendar: %v", err)
	}
	for _, expected := range []struct{ workspaceID, generation, metricID int }{
		{firstWorkspaceID, firstGeneration, metricIDs[0]},
		{secondWorkspaceID, secondGeneration, metricIDs[1]},
	} {
		var generation, jobs int
		if err := db.QueryRow(`SELECT config_generation FROM sla_workspace_state WHERE workspace_id = ?`, expected.workspaceID).Scan(&generation); err != nil {
			t.Fatalf("load workspace %d generation: %v", expected.workspaceID, err)
		}
		if generation <= expected.generation {
			t.Fatalf("workspace %d generation = %d, want greater than %d", expected.workspaceID, generation, expected.generation)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM sla_jobs WHERE kind = 'recalc_calendar' AND metric_id = ? AND threshold_key = ?`, expected.metricID, fmt.Sprint(calendar.ID)).Scan(&jobs); err != nil {
			t.Fatalf("count workspace %d calendar jobs: %v", expected.workspaceID, err)
		}
		if jobs != 1 {
			t.Fatalf("workspace %d calendar recalculation jobs = %d, want 1", expected.workspaceID, jobs)
		}
	}
}

func slaCalendarBoolPointer(value bool) *bool { return &value }

// injectCalendarEnqueueFailure makes the next recalc_calendar job insert fail so
// the calendar update path exercises its transaction rollback. SQLite and
// PostgreSQL trigger syntax differ, so each engine gets its own DDL.
func injectCalendarEnqueueFailure(db database.Database) error {
	if testutils.IsPostgres() {
		if _, err := db.Exec(`CREATE FUNCTION fail_calendar_recalculation() RETURNS trigger AS $$
			BEGIN
				IF NEW.kind = 'recalc_calendar' THEN
					RAISE EXCEPTION 'enqueue failure';
				END IF;
				RETURN NEW;
			END;
			$$ LANGUAGE plpgsql`); err != nil {
			return err
		}
		_, err := db.Exec(`CREATE TRIGGER fail_calendar_recalculation BEFORE INSERT ON sla_jobs FOR EACH ROW EXECUTE FUNCTION fail_calendar_recalculation()`)
		return err
	}
	_, err := db.Exec(`CREATE TRIGGER fail_calendar_recalculation BEFORE INSERT ON sla_jobs WHEN NEW.kind = 'recalc_calendar' BEGIN SELECT RAISE(ABORT, 'enqueue failure'); END`)
	return err
}
