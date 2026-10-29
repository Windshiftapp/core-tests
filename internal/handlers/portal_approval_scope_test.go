//go:build test

package handlers

import (
	"testing"
	"time"

	"windshift/internal/models"
)

func portalApprovalRequest(requestStatus, stepStatus string, approverActive, started bool) *models.ApprovalRequest {
	userID := 7
	step := models.ApprovalStepInstance{
		Status: stepStatus,
		Approvers: []models.ApprovalStepApprover{
			{UserID: &userID, IsActive: approverActive},
		},
	}
	if started {
		startedAt := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
		step.StartedAt = &startedAt
	}
	return &models.ApprovalRequest{
		Status:        requestStatus,
		StepInstances: []models.ApprovalStepInstance{step},
	}
}

// TestPortalActorCanViewRequestRequiresActiveApproverWhilePending pins finding
// 2: a revoked approver must not read a pending request, but former approvers
// keep read access to resolved history.
func TestPortalActorCanViewRequestRequiresActiveApproverWhilePending(t *testing.T) {
	userID := 7
	actor := portalApprovalActor{userID: &userID}

	if !portalActorCanViewRequest(actor, portalApprovalRequest(models.ApprovalRequestStatusPending, models.ApprovalStepStatusPending, true, true)) {
		t.Fatal("active approver must see the pending request")
	}
	if portalActorCanViewRequest(actor, portalApprovalRequest(models.ApprovalRequestStatusPending, models.ApprovalStepStatusPending, false, true)) {
		t.Fatal("revoked approver must not see the pending request")
	}
	if !portalActorCanViewRequest(actor, portalApprovalRequest(models.ApprovalRequestStatusApproved, models.ApprovalStepStatusApproved, false, true)) {
		t.Fatal("former approver must retain read access to resolved history")
	}
}

// TestPortalActorCanViewRequestRequiresStartedPendingStep covers WI-1422: an
// approver row on a completed or not-yet-started step must not grant read
// access while the request itself is still pending.
func TestPortalActorCanViewRequestRequiresStartedPendingStep(t *testing.T) {
	userID := 7
	actor := portalApprovalActor{userID: &userID}

	cases := []struct {
		name       string
		stepStatus string
		started    bool
		want       bool
	}{
		{name: "active pending step", stepStatus: models.ApprovalStepStatusPending, started: true, want: true},
		{name: "completed step", stepStatus: models.ApprovalStepStatusApproved, started: true, want: false},
		{name: "rejected step", stepStatus: models.ApprovalStepStatusRejected, started: true, want: false},
		{name: "skipped step", stepStatus: models.ApprovalStepStatusSkipped, started: true, want: false},
		{name: "pending step not started", stepStatus: models.ApprovalStepStatusPending, started: false, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := portalApprovalRequest(models.ApprovalRequestStatusPending, tc.stepStatus, true, tc.started)
			if got := portalActorCanViewRequest(actor, req); got != tc.want {
				t.Fatalf("portalActorCanViewRequest = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPortalActorCanViewRequestForPortalCustomer applies the same active-pool
// rule to portal customers, who have no internal user identity.
func TestPortalActorCanViewRequestForPortalCustomer(t *testing.T) {
	customerID := 42
	actor := portalApprovalActor{customerID: &customerID}
	startedAt := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	req := func(active bool) *models.ApprovalRequest {
		return &models.ApprovalRequest{
			Status: models.ApprovalRequestStatusPending,
			StepInstances: []models.ApprovalStepInstance{{
				Status:    models.ApprovalStepStatusPending,
				StartedAt: &startedAt,
				Approvers: []models.ApprovalStepApprover{{
					PortalCustomerID: &customerID,
					IsActive:         active,
				}},
			}},
		}
	}

	if !portalActorCanViewRequest(actor, req(true)) {
		t.Fatal("active customer approver must see the pending request")
	}
	if portalActorCanViewRequest(actor, req(false)) {
		t.Fatal("revoked customer approver must not see the pending request")
	}
}

// TestPortalActorCanViewRequestRejectsUnrelatedActor keeps the identity match
// honest: an approver row for another user must not grant access.
func TestPortalActorCanViewRequestRejectsUnrelatedActor(t *testing.T) {
	userID := 7
	actor := portalApprovalActor{userID: &userID}
	otherID := 99
	startedAt := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	req := &models.ApprovalRequest{
		Status: models.ApprovalRequestStatusPending,
		StepInstances: []models.ApprovalStepInstance{
			{
				Status:    models.ApprovalStepStatusPending,
				StartedAt: &startedAt,
				Approvers: []models.ApprovalStepApprover{{UserID: &otherID, IsActive: true}},
			},
		},
	}
	if portalActorCanViewRequest(actor, req) {
		t.Fatal("unrelated actor must not view the request")
	}
}

// TestPortalServedWorkspaceHonoursEmptyScope pins the served-workspace filter
// used by the approval list: an empty scope disables the restriction, and a
// non-empty scope admits only listed workspaces.
func TestPortalServedWorkspaceHonoursEmptyScope(t *testing.T) {
	if !portalServedWorkspace(nil, 5) {
		t.Fatal("empty served scope must disable the restriction")
	}
	if portalServedWorkspace([]int{1, 2}, 5) {
		t.Fatal("workspace outside the served scope must be rejected")
	}
	if !portalServedWorkspace([]int{1, 5}, 5) {
		t.Fatal("workspace inside the served scope must be admitted")
	}
}
