//go:build test

package services_test

import (
	"context"
	"encoding/json"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// TestWorkspaceServiceDeleteWithSelfTargetingMetric asserts a workspace whose
// own SLA metric targets its own calendar deletes cleanly. The goal-target
// calendar RESTRICT previously could abort depending on cascade order.
func TestWorkspaceServiceDeleteWithSelfTargetingMetric(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	db := tdb.GetDatabase()

	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('SLA delete', 'SLAD') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	repo := repository.NewSLARepository(db)
	calendarID, err := repo.CreateCalendar(context.Background(), &models.WorkingCalendar{
		WorkspaceID: &workspaceID, Name: "Own hours", Timezone: "UTC",
		WeeklyIntervals: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create calendar: %v", err)
	}
	if err := database.WithTx(db, func(tx database.Tx) error {
		_, err := repo.CreateMetric(context.Background(), tx, &models.SLAMetric{
			WorkspaceID: workspaceID, Name: "Self target", IsActive: true, ImportStatus: "native",
			Goals: []models.SLAGoal{{Position: 0, QLQuery: "true", ImportStatus: "native",
				Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 3_600_000, CalendarID: calendarID}}}},
		})
		return err
	}); err != nil {
		t.Fatalf("create metric: %v", err)
	}

	if err := services.NewWorkspaceService(db).Delete(workspaceID); err != nil {
		t.Fatalf("Delete workspace: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspaces WHERE id = ?`, workspaceID).Scan(&count); err != nil {
		t.Fatalf("count workspaces: %v", err)
	}
	if count != 0 {
		t.Fatal("workspace still exists after delete")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sla_goal_targets`).Scan(&count); err != nil {
		t.Fatalf("count goal targets: %v", err)
	}
	if count != 0 {
		t.Fatalf("goal targets = %d, want 0", count)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM working_calendars WHERE workspace_id = ?`, workspaceID).Scan(&count); err != nil {
		t.Fatalf("count calendars: %v", err)
	}
	if count != 0 {
		t.Fatalf("calendars = %d, want 0", count)
	}
}
