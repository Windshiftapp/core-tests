//go:build test

package repository

import (
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

func setupFromAllWorkflowFixture(t *testing.T) (db database.Database, workflowID, openID, doneID int) {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db = tdb.GetDatabase()

	insertID := func(query string, args ...interface{}) int {
		t.Helper()
		return testutils.InsertID(t, db, query, args...)
	}

	categoryID := insertID(`INSERT INTO status_categories (name, color) VALUES ('From All Test', '#123456')`)
	openID = insertID(
		`INSERT INTO statuses (name, description, category_id) VALUES ('From All Open', '', ?)`,
		categoryID,
	)
	doneID = insertID(
		`INSERT INTO statuses (name, description, category_id) VALUES ('From All Done', '', ?)`,
		categoryID,
	)
	workflowID = insertID(`INSERT INTO workflows (name, description) VALUES ('From All Workflow', '')`)
	return db, workflowID, openID, doneID
}

func TestWorkflowRepositoryReplaceTransitionsPersistsFromAllRows(t *testing.T) {
	db, workflowID, openID, doneID := setupFromAllWorkflowFixture(t)

	repo := NewWorkflowRepository(db)
	bogusFrom := openID + 1000
	if _, err := repo.ReplaceTransitions(workflowID, []models.WorkflowTransition{
		{WorkflowID: workflowID, FromStatusID: nil, ToStatusID: openID, DisplayOrder: -1},
		{WorkflowID: workflowID, FromStatusID: &openID, ToStatusID: doneID, DisplayOrder: 1},
		// A from-all row that also carries a from status must be normalized to NULL.
		{WorkflowID: workflowID, FromStatusID: &bogusFrom, ToStatusID: openID, FromAllStatuses: true, DisplayOrder: 2},
	}); err != nil {
		t.Fatalf("ReplaceTransitions() error = %v", err)
	}

	transitions, err := repo.ListTransitions(workflowID)
	if err != nil {
		t.Fatalf("ListTransitions() error = %v", err)
	}

	var initial, direct, fromAll *models.WorkflowTransition
	for i := range transitions {
		switch {
		case transitions[i].FromAllStatuses:
			fromAll = &transitions[i]
		case transitions[i].FromStatusID == nil:
			initial = &transitions[i]
		default:
			direct = &transitions[i]
		}
	}
	if initial == nil || initial.ToStatusID != openID {
		t.Fatalf("initial transition = %#v, want to_status %d", initial, openID)
	}
	if direct == nil || direct.ToStatusID != doneID {
		t.Fatalf("direct transition = %#v, want to_status %d", direct, doneID)
	}
	if fromAll == nil {
		t.Fatalf("from-all transition missing; transitions=%#v", transitions)
	}
	if fromAll.FromStatusID != nil {
		t.Fatalf("from-all transition from_status_id = %d, want NULL", *fromAll.FromStatusID)
	}
	if fromAll.ToStatusID != openID {
		t.Fatalf("from-all transition to_status_id = %d, want %d", fromAll.ToStatusID, openID)
	}

	// Replacing without the from-all row deletes it while keeping the others.
	if _, err := repo.ReplaceTransitions(workflowID, []models.WorkflowTransition{
		{WorkflowID: workflowID, FromStatusID: nil, ToStatusID: openID, DisplayOrder: -1},
		{WorkflowID: workflowID, FromStatusID: &openID, ToStatusID: doneID, DisplayOrder: 1},
	}); err != nil {
		t.Fatalf("ReplaceTransitions() second call error = %v", err)
	}

	transitions, err = repo.ListTransitions(workflowID)
	if err != nil {
		t.Fatalf("ListTransitions() second call error = %v", err)
	}
	if len(transitions) != 2 {
		t.Fatalf("transition count = %d, want 2; transitions=%#v", len(transitions), transitions)
	}
	for _, transition := range transitions {
		if transition.FromAllStatuses {
			t.Fatalf("from-all row survived replacement; transitions=%#v", transitions)
		}
	}
}

func TestWorkflowRepositoryListAvailableTransitionsIncludesFromAllRows(t *testing.T) {
	db, workflowID, openID, doneID := setupFromAllWorkflowFixture(t)

	if _, err := db.Exec(
		`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, display_order) VALUES (?, NULL, ?, -1)`,
		workflowID, openID,
	); err != nil {
		t.Fatalf("insert initial transition: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, display_order) VALUES (?, ?, ?, 1)`,
		workflowID, openID, doneID,
	); err != nil {
		t.Fatalf("insert direct transition: %v", err)
	}
	// A from-all row targeting the same status as the direct edge must not
	// duplicate it, and a from-all row targeting another status must appear.
	otherID := testutils.InsertID(t, db,
		`INSERT INTO statuses (name, description, category_id) VALUES ('From All Other', '', (SELECT id FROM status_categories WHERE name = 'From All Test'))`)
	if _, err := db.Exec(
		`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, from_all_statuses, display_order) VALUES (?, NULL, ?, TRUE, 2)`,
		workflowID, doneID,
	); err != nil {
		t.Fatalf("insert duplicate from-all transition: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, from_all_statuses, display_order) VALUES (?, NULL, ?, TRUE, 3)`,
		workflowID, otherID,
	); err != nil {
		t.Fatalf("insert from-all transition: %v", err)
	}

	transitions, err := NewWorkflowRepository(db).ListAvailableTransitions(workflowID, openID)
	if err != nil {
		t.Fatalf("ListAvailableTransitions() error = %v", err)
	}

	var sawDone, sawOther, sawDuplicate bool
	for _, transition := range transitions {
		switch transition.ToStatusID {
		case doneID:
			if transition.FromAllStatuses {
				sawDuplicate = true
			}
			sawDone = true
		case otherID:
			if !transition.FromAllStatuses {
				t.Fatalf("other transition = %#v, want from_all flag", transition)
			}
			sawOther = true
		}
	}
	if !sawDone || sawDuplicate {
		t.Fatalf("direct transition lost or duplicated; sawDone=%v sawDuplicate=%v transitions=%#v", sawDone, sawDuplicate, transitions)
	}
	if !sawOther {
		t.Fatalf("from-all target missing from available transitions; transitions=%#v", transitions)
	}
}
