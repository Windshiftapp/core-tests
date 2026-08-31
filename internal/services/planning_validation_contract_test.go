//go:build test

package services

import (
	"errors"
	"testing"

	"windshift/internal/testutils"
)

func assertPlanningValidationField(t *testing.T, err error, field string) {
	t.Helper()
	var validationErr *PlanningValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error = %v, want PlanningValidationError", err)
	}
	if validationErr.Field != field {
		t.Fatalf("validation field = %q, want %q (error: %v)", validationErr.Field, field, err)
	}
}

func TestPlanningServiceCentralValidation(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()
	service := NewPlanningService(db)

	var workspaceID int
	if err := db.QueryRow(
		"INSERT INTO workspaces (name, key, active) VALUES (?, ?, ?) RETURNING id",
		"Planning validation", "PV", true,
	).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}

	milestoneTests := []struct {
		name   string
		params CreateMilestoneParams
		field  string
	}{
		{"name", CreateMilestoneParams{Status: "planning", IsGlobal: true}, "name"},
		{"target date", CreateMilestoneParams{Name: "M", Status: "planning", TargetDate: strPtr("24-07-2026"), IsGlobal: true}, "target_date"},
		{"status", CreateMilestoneParams{Name: "M", Status: "active", IsGlobal: true}, "status"},
		{"scope", CreateMilestoneParams{Name: "M", Status: "planning", IsGlobal: false}, "workspace_id"},
		{"workspace reference", CreateMilestoneParams{Name: "M", Status: "planning", WorkspaceID: intPtr(999999)}, "workspace_id"},
		{"category reference", CreateMilestoneParams{Name: "M", Status: "planning", IsGlobal: true, CategoryID: intPtr(999999)}, "category_id"},
	}
	for _, tt := range milestoneTests {
		t.Run("milestone "+tt.name, func(t *testing.T) {
			_, err := service.CreateMilestone(tt.params)
			assertPlanningValidationField(t, err, tt.field)
		})
	}

	iterationTests := []struct {
		name   string
		params CreateIterationParams
		field  string
	}{
		{"name", CreateIterationParams{StartDate: "2026-07-01", EndDate: "2026-07-14", Status: "planned", IsGlobal: true}, "name"},
		{"start date", CreateIterationParams{Name: "I", StartDate: "bad", EndDate: "2026-07-14", Status: "planned", IsGlobal: true}, "start_date"},
		{"end before start", CreateIterationParams{Name: "I", StartDate: "2026-07-14", EndDate: "2026-07-01", Status: "planned", IsGlobal: true}, "end_date"},
		{"status", CreateIterationParams{Name: "I", StartDate: "2026-07-01", EndDate: "2026-07-14", Status: "planning", IsGlobal: true}, "status"},
		{"scope", CreateIterationParams{Name: "I", StartDate: "2026-07-01", EndDate: "2026-07-14", Status: "planned"}, "workspace_id"},
		{"workspace reference", CreateIterationParams{Name: "I", StartDate: "2026-07-01", EndDate: "2026-07-14", Status: "planned", WorkspaceID: intPtr(999999)}, "workspace_id"},
		{"type reference", CreateIterationParams{Name: "I", StartDate: "2026-07-01", EndDate: "2026-07-14", Status: "planned", IsGlobal: true, TypeID: intPtr(999999)}, "type_id"},
	}
	for _, tt := range iterationTests {
		t.Run("iteration "+tt.name, func(t *testing.T) {
			_, err := service.CreateIteration(tt.params)
			assertPlanningValidationField(t, err, tt.field)
		})
	}

	workspaceIteration, err := service.CreateIteration(CreateIterationParams{
		Name: "Valid", StartDate: "2026-07-01", EndDate: "2026-07-14",
		Status: "planned", WorkspaceID: &workspaceID,
	})
	if err != nil {
		t.Fatalf("valid workspace iteration rejected: %v", err)
	}
	if workspaceIteration.WorkspaceID == nil || *workspaceIteration.WorkspaceID != workspaceID {
		t.Fatalf("workspace iteration = %+v", workspaceIteration)
	}
}
