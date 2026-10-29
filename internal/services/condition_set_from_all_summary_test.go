//go:build test

package services

import (
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// TestConditionSetListSummarizesFromAllTransitions pins the gated-transition
// summary the condition-set manager renders. From-all rows have no
// from_status_id/name, so the summary must carry from_all_statuses to avoid
// mistaking them for initial transitions.
func TestConditionSetListSummarizesFromAllTransitions(t *testing.T) {
	tdb, permissions, seed := convergenceFixture(t, true)
	db := tdb.GetDatabase()

	workflowID := testutils.InsertID(t, db,
		`INSERT INTO workflows (name, description) VALUES (?, ?)`,
		"from-all summary workflow", "")
	targetStatusID := testutils.InsertID(t, db,
		`INSERT INTO statuses (name, category_id) VALUES (?, ?)`,
		"From-all Target", seed.StatusCategoryID)
	transitionID := testutils.InsertID(t, db,
		`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, from_all_statuses, display_order)
		 VALUES (?, NULL, ?, TRUE, 0)`,
		workflowID, targetStatusID)

	service := NewConditionSetApplicationService(db, permissions)
	created, err := service.Create(AuditActor{UserID: seed.UserID}, models.ConditionSet{
		Name:       "from-all condition set",
		WorkflowID: workflowID,
		TransitionConditions: []models.TransitionCondition{
			{TransitionID: transitionID, LogicMode: "and"},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	items, err := service.List(&workflowID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("list = %+v, want the created set", items)
	}
	if len(items[0].GatedTransitions) != 1 {
		t.Fatalf("gated transitions = %+v, want 1", items[0].GatedTransitions)
	}
	got := items[0].GatedTransitions[0]
	if !got.FromAllStatuses {
		t.Errorf("from_all_statuses = false, want true")
	}
	if got.FromStatusName != "" {
		t.Errorf("from_status_name = %q, want empty", got.FromStatusName)
	}
	if got.ToStatusName != "From-all Target" {
		t.Errorf("to_status_name = %q, want %q", got.ToStatusName, "From-all Target")
	}
}
