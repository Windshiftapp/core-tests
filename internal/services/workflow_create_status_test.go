package services

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"windshift/internal/database"
)

type createStatusFixture struct {
	db           database.Database
	workspaceID  int
	configSetID  int
	workflowID   int
	itemTypeID   int
	openID       int
	doneID       int
	transitionID int
}

func TestValidateCreateStatusOverride(t *testing.T) {
	fx := newCreateStatusFixture(t, "create-status.db")
	defer fx.db.Close()

	openCategoryID := mustQueryID(t, fx.db, `SELECT category_id FROM statuses WHERE name = 'Open'`)
	unreachableID := mustExecInsertID(t, fx.db, `INSERT INTO statuses (name, category_id) VALUES ('Unreachable', ?)`, openCategoryID)

	workflow := NewWorkflowService(fx.db)
	ctx := context.Background()

	if err := workflow.ValidateCreateStatusOverride(ctx, fx.workspaceID, &fx.itemTypeID, fx.openID); err != nil {
		t.Fatalf("initial status override rejected: %v", err)
	}
	if err := workflow.ValidateCreateStatusOverride(ctx, fx.workspaceID, &fx.itemTypeID, fx.doneID); err != nil {
		t.Fatalf("directly reachable status override rejected: %v", err)
	}

	err := workflow.ValidateCreateStatusOverride(ctx, fx.workspaceID, &fx.itemTypeID, unreachableID)
	assertTransitionRejectionCode(t, err, "workflow_invalid")
}

func TestValidateCreateStatusOverrideRejectsConditionedTransition(t *testing.T) {
	fx := newCreateStatusFixture(t, "create-status-condition.db")
	defer fx.db.Close()

	conditionSetID := mustExecInsertID(t, fx.db, `INSERT INTO condition_sets (name, workflow_id) VALUES ('Guarded', ?)`, fx.workflowID)
	conditionSetTransitionID := mustExecInsertID(t, fx.db, `INSERT INTO condition_set_transitions (condition_set_id, transition_id) VALUES (?, ?)`, conditionSetID, fx.transitionID)
	mustExec(t, fx.db, `INSERT INTO conditions (condition_set_transition_id, condition_type, config, mode) VALUES (?, 'field_value', '{}', 'validator')`, conditionSetTransitionID)
	mustExec(t, fx.db, `UPDATE configuration_sets SET condition_set_id = ? WHERE id = ?`, conditionSetID, fx.configSetID)

	err := NewWorkflowService(fx.db).ValidateCreateStatusOverride(context.Background(), fx.workspaceID, &fx.itemTypeID, fx.doneID)
	assertTransitionRejectionCode(t, err, "condition_blocked")
}

func TestValidateCreateStatusOverrideRejectsApprovalBoundStatus(t *testing.T) {
	fx := newCreateStatusFixture(t, "create-status-approval-bound.db")
	defer fx.db.Close()

	approvalSetID := mustExecInsertID(t, fx.db, `INSERT INTO approval_sets (name, workflow_id) VALUES ('Approvals', ?)`, fx.workflowID)
	approveTransitionID := mustExecInsertID(t, fx.db, `INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, display_order) VALUES (?, ?, ?, 100)`, fx.workflowID, fx.doneID, fx.openID)
	denyTransitionID := mustExecInsertID(t, fx.db, `INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, display_order) VALUES (?, ?, ?, 101)`, fx.workflowID, fx.doneID, fx.doneID)
	mustExec(t, fx.db, `INSERT INTO approval_set_statuses (approval_set_id, status_id, approve_transition_id, deny_transition_id) VALUES (?, ?, ?, ?)`, approvalSetID, fx.doneID, approveTransitionID, denyTransitionID)
	mustExec(t, fx.db, `UPDATE configuration_sets SET approval_set_id = ? WHERE id = ?`, approvalSetID, fx.configSetID)

	err := NewWorkflowService(fx.db).ValidateCreateStatusOverride(context.Background(), fx.workspaceID, &fx.itemTypeID, fx.doneID)
	assertTransitionRejectionCode(t, err, "approval_pending")
}

func TestValidateCreateStatusOverrideRejectsApprovalDecisionTransition(t *testing.T) {
	fx := newCreateStatusFixture(t, "create-status-approval-transition.db")
	defer fx.db.Close()

	approvalSetID := mustExecInsertID(t, fx.db, `INSERT INTO approval_sets (name, workflow_id) VALUES ('Approvals', ?)`, fx.workflowID)
	denyTransitionID := mustExecInsertID(t, fx.db, `INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, display_order) VALUES (?, ?, ?, 101)`, fx.workflowID, fx.doneID, fx.openID)
	mustExec(t, fx.db, `INSERT INTO approval_set_statuses (approval_set_id, status_id, approve_transition_id, deny_transition_id) VALUES (?, ?, ?, ?)`, approvalSetID, fx.openID, fx.transitionID, denyTransitionID)
	mustExec(t, fx.db, `UPDATE configuration_sets SET approval_set_id = ? WHERE id = ?`, approvalSetID, fx.configSetID)

	err := NewWorkflowService(fx.db).ValidateCreateStatusOverride(context.Background(), fx.workspaceID, &fx.itemTypeID, fx.doneID)
	assertTransitionRejectionCode(t, err, "approval_must_decide")
}

func newCreateStatusFixture(t *testing.T, name string) createStatusFixture {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	workspaceID := mustExecInsertID(t, db, `INSERT INTO workspaces (name, key) VALUES ('Test', 'TST')`)
	configSetID := mustQueryID(t, db, `SELECT id FROM configuration_sets WHERE is_default = true ORDER BY id LIMIT 1`)
	mustExec(t, db, `INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, workspaceID, configSetID)
	itemTypeID := mustQueryID(t, db, `SELECT id FROM item_types WHERE is_default = true ORDER BY id LIMIT 1`)
	workflowID := mustQueryID(t, db, `SELECT workflow_id FROM configuration_sets WHERE id = ?`, configSetID)
	openID := mustQueryID(t, db, `SELECT id FROM statuses WHERE name = 'Open'`)
	doneID := mustQueryID(t, db, `SELECT id FROM statuses WHERE name = 'Done'`)
	transitionID := mustQueryID(t, db, `SELECT id FROM workflow_transitions WHERE workflow_id = ? AND from_status_id = ? AND to_status_id = ?`, workflowID, openID, doneID)

	return createStatusFixture{
		db:           db,
		workspaceID:  workspaceID,
		configSetID:  configSetID,
		workflowID:   workflowID,
		itemTypeID:   itemTypeID,
		openID:       openID,
		doneID:       doneID,
		transitionID: transitionID,
	}
}

func assertTransitionRejectionCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s rejection, got nil", code)
	}
	var rejection *TransitionRejection
	if !errors.As(err, &rejection) || rejection.Code != code {
		t.Fatalf("error = %#v, want %s TransitionRejection", err, code)
	}
}

func mustQueryID(t *testing.T, db database.Database, query string, args ...interface{}) int {
	t.Helper()
	var id int
	if err := db.QueryRow(query, args...).Scan(&id); err != nil {
		t.Fatalf("query id %q: %v", query, err)
	}
	return id
}

func mustExec(t *testing.T, db database.Database, query string, args ...interface{}) {
	t.Helper()
	if _, err := db.ExecWrite(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func mustExecInsertID(t *testing.T, db database.Database, query string, args ...interface{}) int {
	t.Helper()
	result, err := db.ExecWrite(query, args...)
	if err != nil {
		t.Fatalf("exec insert %q: %v", query, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return int(id)
}
