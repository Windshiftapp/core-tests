//go:build test

package services

import (
	"testing"

	"windshift/internal/logger"
	"windshift/internal/testutils"
)

func TestPlanningService_UserMutationsEmitExactlyOneAuditPerOperation(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	service := NewPlanningService(tdb.GetDatabase())
	actor := AuditActor{UserID: data.UserID, Username: "testuser", Source: "rest_v1", AuthMethod: "bearer", APITokenID: 42}

	milestone, err := service.CreateMilestone(CreateMilestoneParams{
		Name: "Audited milestone", Status: "planning", IsGlobal: true, AuditActor: &actor,
	})
	if err != nil {
		t.Fatalf("create milestone: %v", err)
	}
	if _, err := service.UpdateMilestone(UpdateMilestoneParams{
		ID: milestone.ID, Name: "Audited milestone updated", Status: "planning", AuditActor: &actor,
	}); err != nil {
		t.Fatalf("update milestone: %v", err)
	}
	if err := service.DeleteMilestone(milestone.ID, actor); err != nil {
		t.Fatalf("delete milestone: %v", err)
	}

	iteration, err := service.CreateIteration(CreateIterationParams{
		Name: "Audited iteration", StartDate: "2026-08-01", EndDate: "2026-08-14", Status: "planned", IsGlobal: true, AuditActor: &actor,
	})
	if err != nil {
		t.Fatalf("create iteration: %v", err)
	}
	if _, err := service.UpdateIteration(UpdateIterationParams{
		ID: iteration.ID, Name: "Audited iteration updated", StartDate: iteration.StartDate, EndDate: iteration.EndDate, Status: "active", AuditActor: &actor,
	}); err != nil {
		t.Fatalf("update iteration: %v", err)
	}
	if err := service.DeleteIteration(iteration.ID, actor); err != nil {
		t.Fatalf("delete iteration: %v", err)
	}

	want := []string{
		logger.ActionMilestoneCreate, logger.ActionMilestoneUpdate, logger.ActionMilestoneDelete,
		logger.ActionIterationCreate, logger.ActionIterationUpdate, logger.ActionIterationDelete,
	}
	for _, action := range want {
		var count int
		if err := tdb.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action_type = ?`, action).Scan(&count); err != nil {
			t.Fatalf("count %s audits: %v", action, err)
		}
		if count != 1 {
			t.Fatalf("%s audit count = %d, want 1", action, count)
		}
	}

	if _, err := service.CreateMilestone(CreateMilestoneParams{Name: "Internal milestone", Status: "planning", IsGlobal: true}); err != nil {
		t.Fatalf("create internal milestone: %v", err)
	}
	var createCount int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action_type = ?`, logger.ActionMilestoneCreate).Scan(&createCount); err != nil {
		t.Fatalf("count milestone create audits: %v", err)
	}
	if createCount != 1 {
		t.Fatalf("internal caller emitted a user audit; create count = %d, want 1", createCount)
	}
}
