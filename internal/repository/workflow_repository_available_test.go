//go:build test

package repository

import (
	"testing"

	"windshift/internal/testutils"
)

func TestWorkflowRepositoryListAvailableTransitionsExcludesInitialTransition(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	insertID := func(query string, args ...interface{}) int {
		t.Helper()
		return testutils.InsertID(t, tdb.GetDatabase(), query, args...)
	}

	categoryID := insertID(`INSERT INTO status_categories (name, color) VALUES ('Available Test', '#123456')`)
	openID := insertID(
		`INSERT INTO statuses (name, description, category_id) VALUES ('Available Open', '', ?)`,
		categoryID,
	)
	doneID := insertID(
		`INSERT INTO statuses (name, description, category_id) VALUES ('Available Done', '', ?)`,
		categoryID,
	)
	workflowID := insertID(`INSERT INTO workflows (name, description) VALUES ('Available Workflow', '')`)
	if _, err := tdb.Exec(
		`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, display_order) VALUES (?, NULL, ?, 1)`,
		workflowID,
		openID,
	); err != nil {
		t.Fatalf("insert initial transition: %v", err)
	}
	if _, err := tdb.Exec(
		`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, display_order) VALUES (?, ?, ?, 2)`,
		workflowID,
		openID,
		doneID,
	); err != nil {
		t.Fatalf("insert directed transition: %v", err)
	}

	transitions, err := NewWorkflowRepository(tdb.GetDatabase()).ListAvailableTransitions(workflowID, openID)
	if err != nil {
		t.Fatalf("ListAvailableTransitions() error = %v", err)
	}
	if len(transitions) != 1 {
		t.Fatalf("transition count = %d, want 1; transitions=%#v", len(transitions), transitions)
	}
	if transitions[0].FromStatusID == nil || *transitions[0].FromStatusID != openID ||
		transitions[0].ToStatusID != doneID {
		t.Fatalf("available transition = %#v, want %d -> %d", transitions[0], openID, doneID)
	}
}
