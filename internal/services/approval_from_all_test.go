//go:build test

package services

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
)

func TestApprovalSetServiceAcceptsFromAllTransitions(t *testing.T) {
	env := newApprovalTestEnv(t)
	approveTransitionID := env.insertFromAllTransition(env.statusApprovedID)
	denyTransitionID := env.insertFromAllTransition(env.statusRejectedID)

	created, err := NewApprovalSetService(env.db).Create(context.Background(), models.ApprovalSet{
		Name:       "from-all approval set",
		WorkflowID: env.workflowID,
		SetStatuses: []models.ApprovalSetStatus{{
			StatusID:            env.statusReviewID,
			ApproveTransitionID: approveTransitionID,
			DenyTransitionID:    denyTransitionID,
			StepMode:            models.ApprovalStepModeSequential,
			Steps: []models.ApprovalStep{{
				DisplayOrder:   0,
				Name:           "Decision",
				QuorumMode:     models.ApprovalQuorumModeAny,
				ApproverSource: models.ApprovalSourceUser,
				ApproverUserID: &env.approver1,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("create approval set with from-all transitions: %v", err)
	}
	if len(created.SetStatuses) != 1 {
		t.Fatalf("set statuses = %d, want 1", len(created.SetStatuses))
	}
	if got := created.SetStatuses[0].ApproveTransitionID; got != approveTransitionID {
		t.Fatalf("approve transition = %d, want %d", got, approveTransitionID)
	}
}

func TestApprovalFromAllTransitionIsGatedAndFinalizes(t *testing.T) {
	env := newApprovalTestEnv(t)
	approveTransitionID := env.insertFromAllTransition(env.statusApprovedID)
	denyTransitionID := env.insertFromAllTransition(env.statusRejectedID)
	env.createApprovalSet(approvalSetSpec{
		steps: []approvalStepSpec{userStep(0, "Decision", env.approver1)},
	})
	if _, err := env.db.Exec(`
		UPDATE approval_set_statuses
		SET approve_transition_id = ?, deny_transition_id = ?
		WHERE approval_set_id = (SELECT approval_set_id FROM configuration_sets WHERE id = ?)
	`, approveTransitionID, denyTransitionID, env.configurationSetID); err != nil {
		t.Fatalf("replace approval transitions with from-all rows: %v", err)
	}

	request, err := env.approvalService.RequestApproval(context.Background(), env.itemID, env.statusReviewID, env.statusOpenID, env.requestor)
	if err != nil {
		t.Fatalf("request approval: %v", err)
	}

	itemRepo := repository.NewItemRepository(env.db)
	_, err = env.workflowService.PerformTransition(context.Background(), PerformTransitionRequest{
		ItemID:      env.itemID,
		ToStatusID:  env.statusApprovedID,
		ActorUserID: env.requestor,
	}, itemRepo, nil, env.approvalService)
	var rejection *TransitionRejection
	if !errors.As(err, &rejection) || rejection.Code != "approval_must_decide" {
		t.Fatalf("direct approve transition error = %v, want approval_must_decide", err)
	}
	if got := env.itemStatusID(); got != env.statusReviewID {
		t.Fatalf("item status after blocked direct transition = %d, want %d", got, env.statusReviewID)
	}

	_, outcome, err := env.approvalService.Decide(context.Background(), request.ID, env.approver1, models.ApprovalDecisionApprove, "approved", DecideOptions{})
	if err != nil {
		t.Fatalf("approve request: %v", err)
	}
	if outcome.Status != models.ApprovalRequestStatusApproved {
		t.Fatalf("request status = %q, want approved", outcome.Status)
	}
	if got := env.itemStatusID(); got != env.statusApprovedID {
		t.Fatalf("item status after approval = %d, want %d", got, env.statusApprovedID)
	}
}

func (e *approvalTestEnv) insertFromAllTransition(toStatusID int) int {
	e.t.Helper()
	result, err := e.db.Exec(`
		INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, from_all_statuses)
		VALUES (?, NULL, ?, TRUE)
	`, e.workflowID, toStatusID)
	if err != nil {
		e.t.Fatalf("insert from-all transition: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		e.t.Fatalf("from-all transition id: %v", err)
	}
	return int(id)
}
