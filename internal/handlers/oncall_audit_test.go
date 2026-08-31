//go:build test

package handlers

import (
	"net/http"
	"testing"

	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func TestOnCallScheduleAdministrationEmitsAudit(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	// Grant teams.manage through the permission repository.
	newServiceSetup(t, tdb).GrantGlobal(data.UserID, "teams.manage")
	var teamID int
	if err := tdb.QueryRow(`INSERT INTO teams (name, created_by) VALUES ('Audit On-call Team', ?) RETURNING id`, data.UserID).Scan(&teamID); err != nil {
		t.Fatalf("create team: %v", err)
	}

	repo := repository.NewOnCallRepository(tdb.GetDatabase())
	permService, _, _ := createTestServices(t, *tdb)
	handler := NewOnCallHandler(
		repo,
		repository.NewTeamRepository(tdb.GetDatabase()),
		services.NewOnCallService(tdb.GetDatabase(), repo, repository.NewLeaveRepository(tdb.GetDatabase())),
		permService,
		logger.NewAuditor(tdb.GetDatabase()),
	)
	body := models.OnCallScheduleRequest{Name: "Primary", Timezone: "UTC"}
	req := testutils.CreateJSONRequest(t, http.MethodPost, "/api/teams/1/on-call/schedules", body)
	req.SetPathValue("id", testutils.IntToString(teamID))
	recorder := testutils.ExecuteAuthenticatedRequest(t, handler.CreateSchedule, req, nil)
	recorder.AssertStatusCode(http.StatusCreated)

	var count int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action_type = ?`, logger.ActionOnCallScheduleCreate).Scan(&count); err != nil {
		t.Fatalf("count schedule create audits: %v", err)
	}
	if count != 1 {
		t.Fatalf("schedule create audit count = %d, want 1", count)
	}
}
