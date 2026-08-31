//go:build test

package services

import (
	"context"
	"testing"

	"windshift/internal/database"
)

// insertFromAllTransition adds a from-all row so every other status may move
// to toStatusID.
func insertFromAllTransition(t *testing.T, db database.Database, workflowID, toStatusID, displayOrder int) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, from_all_statuses, display_order)
		 VALUES (?, NULL, ?, TRUE, ?)`,
		workflowID, toStatusID, displayOrder,
	); err != nil {
		t.Fatalf("insert from-all transition: %v", err)
	}
}

func TestWorkflowServiceFromAllTransitionIsValid(t *testing.T) {
	db := createWorkflowTestDB(t)
	env := setupWorkflowTestEnv(t, db)
	service := NewWorkflowService(db)

	// Baseline from the seeded graph: Open -> Done is not allowed yet.
	valid, err := service.IsValidStatusTransition(env.WorkspaceID, nil, int64(env.StatusID1), int64(env.StatusID3))
	if err != nil {
		t.Fatalf("IsValidStatusTransition() error = %v", err)
	}
	if valid {
		t.Fatal("Open -> Done allowed before from-all row exists")
	}

	insertFromAllTransition(t, db, env.WorkflowID, env.StatusID3, 10)

	valid, err = service.IsValidStatusTransition(env.WorkspaceID, nil, int64(env.StatusID1), int64(env.StatusID3))
	if err != nil {
		t.Fatalf("IsValidStatusTransition() error = %v", err)
	}
	if !valid {
		t.Fatal("Open -> Done denied despite from-all row")
	}

	valid, err = service.IsValidStatusTransition(env.WorkspaceID, nil, int64(env.StatusID3), int64(env.StatusID1))
	if err != nil {
		t.Fatalf("IsValidStatusTransition() reverse error = %v", err)
	}
	if valid {
		t.Fatal("Done -> Open allowed even though only Done accepts from all")
	}
}

func TestWorkflowServiceGetInitialStatusIDIgnoresFromAllRows(t *testing.T) {
	db := createWorkflowTestDB(t)
	env := setupWorkflowTestEnv(t, db)

	// The from-all row sorts before the seeded initial row on purpose.
	insertFromAllTransition(t, db, env.WorkflowID, env.StatusID3, -100)

	statusID, err := NewWorkflowService(db).GetInitialStatusID(env.WorkflowID)
	if err != nil {
		t.Fatalf("GetInitialStatusID() error = %v", err)
	}
	if statusID == nil || *statusID != env.StatusID1 {
		t.Fatalf("initial status = %v, want %d", statusID, env.StatusID1)
	}
}

func TestWorkflowServiceGetTransitionsForItemIncludesFromAllRows(t *testing.T) {
	db := createWorkflowTestDB(t)
	env := setupWorkflowTestEnv(t, db)
	service := NewWorkflowService(db)

	// Done accepts transitions from every status; In Progress also gains a
	// from-all row even though Open already has a direct edge to it.
	insertFromAllTransition(t, db, env.WorkflowID, env.StatusID3, 10)
	insertFromAllTransition(t, db, env.WorkflowID, env.StatusID2, 11)

	transitions, err := service.GetTransitionsForItem(env.WorkspaceID, nil, env.StatusID1)
	if err != nil {
		t.Fatalf("GetTransitionsForItem() error = %v", err)
	}

	var inProgressCount, inProgressFromAll, doneFromAll int
	for _, transition := range transitions {
		switch transition.ToStatusID {
		case env.StatusID2:
			inProgressCount++
			if transition.FromAllStatuses {
				inProgressFromAll++
			}
			if transition.FromStatusID == nil || *transition.FromStatusID != env.StatusID1 {
				t.Fatalf("In Progress transition = %#v, want direct edge from Open", transition)
			}
		case env.StatusID3:
			if transition.FromAllStatuses {
				doneFromAll++
			}
		}
	}
	if inProgressCount != 1 || inProgressFromAll != 0 {
		t.Fatalf("In Progress appears %d times (%d from-all), want the single direct edge; transitions=%#v",
			inProgressCount, inProgressFromAll, transitions)
	}
	if doneFromAll != 1 {
		t.Fatalf("Done from-all appearances = %d, want 1; transitions=%#v", doneFromAll, transitions)
	}
}

func TestTransitionMatrixIncludesFromAllOptionsForEveryStatus(t *testing.T) {
	db := createWorkflowTestDB(t)
	env := setupWorkflowTestEnv(t, db)

	insertFromAllTransition(t, db, env.WorkflowID, env.StatusID3, 10)

	matrix, err := NewTransitionMatrixService(db).Load(context.Background(), env.WorkspaceID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(matrix.ByItemType) == 0 {
		t.Fatal("matrix has no item types")
	}

	for itemTypeID, byStatus := range matrix.ByItemType {
		countTargets := func(statusID int, targetID int) int {
			count := 0
			for _, option := range byStatus[statusID] {
				if option.StatusID == targetID {
					count++
				}
			}
			return count
		}

		// Open gains Done through the from-all row.
		if got := countTargets(env.StatusID1, env.StatusID3); got != 1 {
			t.Fatalf("item type %d: Open bucket lists Done %d times, want 1", itemTypeID, got)
		}
		// In Progress already has a direct edge to Done; the from-all row must
		// not duplicate it.
		if got := countTargets(env.StatusID2, env.StatusID3); got != 1 {
			t.Fatalf("item type %d: In Progress bucket lists Done %d times, want 1", itemTypeID, got)
		}
		// Done itself only offers staying in Done.
		if got := countTargets(env.StatusID3, env.StatusID1); got != 0 {
			t.Fatalf("item type %d: Done bucket lists Open %d times, want 0", itemTypeID, got)
		}
		if got := countTargets(env.StatusID3, env.StatusID2); got != 0 {
			t.Fatalf("item type %d: Done bucket lists In Progress %d times, want 0", itemTypeID, got)
		}
	}
}
