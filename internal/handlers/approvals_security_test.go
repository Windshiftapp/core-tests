//go:build test

package handlers

import (
	"net/http"
	"strconv"
	"testing"

	"windshift/internal/logger"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

func TestApprovalEscalateNowRequiresWorkspaceAdmin(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	editorID := approvalSecurityUser(t, tdb, "editor@example.com", "editor")
	currentApproverID := approvalSecurityUser(t, tdb, "approver@example.com", "approver")
	targetApproverID := approvalSecurityUser(t, tdb, "target@example.com", "target")
	var editorRoleID int
	if err := tdb.QueryRow(`SELECT id FROM workspace_roles WHERE name = 'Editor'`).Scan(&editorRoleID); err != nil {
		t.Fatalf("load editor role: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO user_workspace_roles (user_id, workspace_id, role_id) VALUES (?, ?, ?)`, editorID, seed.WorkspaceID, editorRoleID); err != nil {
		t.Fatalf("grant editor role: %v", err)
	}

	workflowID := approvalSecurityInsertID(t, tdb, `INSERT INTO workflows (name) VALUES ('Escalation security workflow') RETURNING id`)
	reviewStatusID := approvalSecurityInsertID(t, tdb, `INSERT INTO statuses (name, description, category_id) VALUES ('Escalation security review', '', ?) RETURNING id`, seed.StatusCategoryID)
	approvedStatusID := approvalSecurityInsertID(t, tdb, `INSERT INTO statuses (name, description, category_id) VALUES ('Escalation security approved', '', ?) RETURNING id`, seed.StatusCategoryID)
	rejectedStatusID := approvalSecurityInsertID(t, tdb, `INSERT INTO statuses (name, description, category_id) VALUES ('Escalation security rejected', '', ?) RETURNING id`, seed.StatusCategoryID)
	approveTransitionID := approvalSecurityInsertID(t, tdb, `INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id) VALUES (?, ?, ?) RETURNING id`, workflowID, reviewStatusID, approvedStatusID)
	denyTransitionID := approvalSecurityInsertID(t, tdb, `INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id) VALUES (?, ?, ?) RETURNING id`, workflowID, reviewStatusID, rejectedStatusID)
	itemTypeID := approvalSecurityInsertID(t, tdb, `INSERT INTO item_types (name, hierarchy_level) VALUES ('Escalation security item', 3) RETURNING id`)
	itemID, err := factory.NewTestFactory(db).CreateItem(factory.CreateItemOpts{
		WorkspaceID: seed.WorkspaceID,
		ItemTypeID:  &itemTypeID,
		Title:       "Admin-only escalation",
		StatusID:    &reviewStatusID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	approvalSetID := approvalSecurityInsertID(t, tdb, `INSERT INTO approval_sets (name, workflow_id) VALUES ('Escalation security set', ?) RETURNING id`, workflowID)
	approvalSetStatusID := approvalSecurityInsertID(t, tdb, `
		INSERT INTO approval_set_statuses
			(approval_set_id, status_id, approve_transition_id, deny_transition_id)
		VALUES (?, ?, ?, ?) RETURNING id
	`, approvalSetID, reviewStatusID, approveTransitionID, denyTransitionID)
	approvalStepID := approvalSecurityInsertID(t, tdb, `
		INSERT INTO approval_steps
			(approval_set_status_id, name, approver_source, approver_user_id,
			 escalation_action, escalation_target_source, escalation_target_user_id)
		VALUES (?, 'Escalate', 'user', ?, 'reassign', 'user', ?) RETURNING id
	`, approvalSetStatusID, currentApproverID, targetApproverID)
	requestID := approvalSecurityInsertID(t, tdb, `
		INSERT INTO approval_requests
			(item_id, approval_set_status_id, status_id, triggered_by_user_id)
		VALUES (?, ?, ?, ?) RETURNING id
	`, itemID, approvalSetStatusID, reviewStatusID, seed.UserID)
	stepInstanceID := approvalSecurityInsertID(t, tdb, `
		INSERT INTO approval_step_instances
			(approval_request_id, approval_step_id, status, started_at)
		VALUES (?, ?, 'pending', CURRENT_TIMESTAMP) RETURNING id
	`, requestID, approvalStepID)
	if _, err := tdb.Exec(`INSERT INTO approval_step_approvers (approval_step_instance_id, user_id, is_active) VALUES (?, ?, true)`, stepInstanceID, currentApproverID); err != nil {
		t.Fatalf("create active approver: %v", err)
	}

	permService, _, _ := createTestServices(t, *tdb)
	approvalService := services.NewApprovalService(db, repository.NewLeaveRepository(db), services.NewWorkflowService(db))
	handler := NewApprovalHandler(permService, approvalService, repository.NewItemRepository(db), logger.NewAuditor(db))
	req := testutils.CreateAuthenticatedJSONRequest(t, http.MethodPost, "/api/approvals/1/steps/1/escalate", nil, testutils.TestUserWithID(editorID))
	req.SetPathValue("id", strconv.Itoa(requestID))
	req.SetPathValue("step_id", strconv.Itoa(stepInstanceID))
	rr := testutils.ExecuteRequest(t, handler.EscalateNow, req)
	rr.AssertStatusCode(http.StatusNotFound)

	var activeApproverID int
	if err := tdb.QueryRow(`SELECT user_id FROM approval_step_approvers WHERE approval_step_instance_id = ? AND is_active = true`, stepInstanceID).Scan(&activeApproverID); err != nil {
		t.Fatalf("load active approver: %v", err)
	}
	if activeApproverID != currentApproverID {
		t.Fatalf("active approver = %d, want unchanged %d", activeApproverID, currentApproverID)
	}
}

func approvalSecurityUser(t *testing.T, tdb *testutils.TestDB, email, username string) int {
	t.Helper()
	return approvalSecurityInsertID(t, tdb, `INSERT INTO users (email, username, first_name, last_name, is_active) VALUES (?, ?, ?, 'User', true) RETURNING id`, email, username, username)
}

func approvalSecurityInsertID(t *testing.T, tdb *testutils.TestDB, query string, args ...any) int {
	t.Helper()
	var id int
	if err := tdb.QueryRow(query, args...).Scan(&id); err != nil {
		t.Fatalf("insert fixture: %v", err)
	}
	return id
}
