package services

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

type approvalChannelFixture struct {
	db         database.Database
	channelA   int
	channelB   int
	requestA   int
	requestB   int
	userID     int
	customerID int
}

func setupApprovalChannelFixture(t *testing.T) approvalChannelFixture {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "approval-channel-security.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	var userID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('channel-approver@example.test', 'channel-approver', 'Channel', 'Approver', true)
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	var customerID int
	if err := db.QueryRow(`
		INSERT INTO portal_customers (name, email, user_id)
		VALUES ('Shared customer', 'shared-customer@example.test', ?)
		RETURNING id
	`, userID).Scan(&customerID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Approval channels', 'ACS') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}

	insertChannel := func(slug string) int {
		t.Helper()
		var id int
		config := fmt.Sprintf(`{"portal_slug":%q,"portal_workspace_ids":[%d]}`, slug, workspaceID)
		if err := db.QueryRow(`
			INSERT INTO channels (name, type, direction, status, config, public_slug)
			VALUES (?, 'portal', 'inbound', 'enabled', ?, ?) RETURNING id
		`, slug, config, slug).Scan(&id); err != nil {
			t.Fatalf("insert channel %s: %v", slug, err)
		}
		return id
	}
	channelA := insertChannel("approval-a")
	channelB := insertChannel("approval-b")

	var categoryID int
	if err := db.QueryRow(`SELECT id FROM status_categories ORDER BY id LIMIT 1`).Scan(&categoryID); err != nil {
		t.Fatalf("load status category: %v", err)
	}
	insertStatus := func(name string) int {
		t.Helper()
		var id int
		if err := db.QueryRow(`INSERT INTO statuses (name, category_id) VALUES (?, ?) RETURNING id`, name, categoryID).Scan(&id); err != nil {
			t.Fatalf("insert status %s: %v", name, err)
		}
		return id
	}
	statusID := insertStatus("Approval channel review")
	terminalStatusID := insertStatus("Approval channel terminal")

	var workflowID int
	if err := db.QueryRow(`INSERT INTO workflows (name) VALUES ('Approval channel workflow') RETURNING id`).Scan(&workflowID); err != nil {
		t.Fatalf("insert workflow: %v", err)
	}
	insertTransition := func(fromID, toID int) int {
		t.Helper()
		var id int
		if err := db.QueryRow(`
			INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id)
			VALUES (?, ?, ?) RETURNING id
		`, workflowID, fromID, toID).Scan(&id); err != nil {
			t.Fatalf("insert transition: %v", err)
		}
		return id
	}
	approveTransitionID := insertTransition(statusID, terminalStatusID)
	denyTransitionID := insertTransition(terminalStatusID, statusID)

	var approvalSetID int
	if err := db.QueryRow(`
		INSERT INTO approval_sets (name, workflow_id) VALUES ('Approval channel set', ?) RETURNING id
	`, workflowID).Scan(&approvalSetID); err != nil {
		t.Fatalf("insert approval set: %v", err)
	}
	var approvalSetStatusID int
	if err := db.QueryRow(`
		INSERT INTO approval_set_statuses
			(approval_set_id, status_id, approve_transition_id, deny_transition_id, step_mode)
		VALUES (?, ?, ?, ?, 'sequential') RETURNING id
	`, approvalSetID, statusID, approveTransitionID, denyTransitionID).Scan(&approvalSetStatusID); err != nil {
		t.Fatalf("insert approval set status: %v", err)
	}
	var approvalStepID int
	if err := db.QueryRow(`
		INSERT INTO approval_steps
			(approval_set_status_id, display_order, name, approver_source, allow_self_approval, on_leave_strategy)
		VALUES (?, 0, 'Shared approvers', 'creator', true, 'keep') RETURNING id
	`, approvalSetStatusID).Scan(&approvalStepID); err != nil {
		t.Fatalf("insert approval step: %v", err)
	}

	insertRequest := func(channelID, itemNumber int) int {
		t.Helper()
		// The production path assigns the workspace item number; itemNumber
		// is kept so call sites document the intended sequencing.
		itemID64, err := CreateItem(db, ItemCreationParams{
			WorkspaceID:             workspaceID,
			Title:                   fmt.Sprintf("Channel %d approval", channelID),
			StatusID:                &statusID,
			ChannelID:               &channelID,
			CreatorID:               &userID,
			CreatorPortalCustomerID: &customerID,
		})
		if err != nil {
			t.Fatalf("insert item: %v", err)
		}
		itemID := int(itemID64)
		var requestID int
		if err := db.QueryRow(`
			INSERT INTO approval_requests
				(item_id, approval_set_status_id, status_id, triggered_by_user_id, status)
			VALUES (?, ?, ?, ?, 'pending') RETURNING id
		`, itemID, approvalSetStatusID, statusID, userID).Scan(&requestID); err != nil {
			t.Fatalf("insert request: %v", err)
		}
		var stepInstanceID int
		if err := db.QueryRow(`
			INSERT INTO approval_step_instances
				(approval_request_id, approval_step_id, display_order, status, started_at)
			VALUES (?, ?, 0, 'pending', CURRENT_TIMESTAMP) RETURNING id
		`, requestID, approvalStepID).Scan(&stepInstanceID); err != nil {
			t.Fatalf("insert step instance: %v", err)
		}
		if _, err := db.ExecWrite(`
			INSERT INTO approval_step_approvers (approval_step_instance_id, user_id, is_active)
			VALUES (?, ?, true)
		`, stepInstanceID, userID); err != nil {
			t.Fatalf("insert user approver: %v", err)
		}
		if _, err := db.ExecWrite(`
			INSERT INTO approval_step_approvers (approval_step_instance_id, portal_customer_id, is_active)
			VALUES (?, ?, true)
		`, stepInstanceID, customerID); err != nil {
			t.Fatalf("insert customer approver: %v", err)
		}
		return requestID
	}

	return approvalChannelFixture{
		db:         db,
		channelA:   channelA,
		channelB:   channelB,
		requestA:   insertRequest(channelA, 1),
		requestB:   insertRequest(channelB, 2),
		userID:     userID,
		customerID: customerID,
	}
}

func TestApprovalQueriesScopeSharedActorsToChannel(t *testing.T) {
	fx := setupApprovalChannelFixture(t)
	ctx := context.Background()
	repo := repository.NewApprovalRepository(fx.db)

	for _, tc := range []struct {
		name        string
		actorColumn string
		actorID     int
	}{
		{name: "shared portal customer", actorColumn: "portal_customer_id", actorID: fx.customerID},
		{name: "linked internal user", actorColumn: "user_id", actorID: fx.userID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotA, err := repo.FindRequestIDsForActorInChannel(ctx, tc.actorColumn, tc.actorID, "pending", fx.channelA)
			if err != nil {
				t.Fatalf("channel A query: %v", err)
			}
			if want := []int{fx.requestA}; !reflect.DeepEqual(gotA, want) {
				t.Fatalf("channel A requests = %v, want %v", gotA, want)
			}
			gotB, err := repo.FindRequestIDsForActorInChannel(ctx, tc.actorColumn, tc.actorID, "pending", fx.channelB)
			if err != nil {
				t.Fatalf("channel B query: %v", err)
			}
			if want := []int{fx.requestB}; !reflect.DeepEqual(gotB, want) {
				t.Fatalf("channel B requests = %v, want %v", gotB, want)
			}
		})
	}

	if _, err := repo.FindFullRequestByIDInChannel(ctx, fx.requestB, fx.channelA); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("cross-channel detail error = %v, want repository.ErrNotFound", err)
	}
}

func TestApprovalDecisionsEnforceChannelInsideTransaction(t *testing.T) {
	fx := setupApprovalChannelFixture(t)
	ctx := context.Background()
	svc := NewApprovalService(fx.db, nil, nil)

	for _, tc := range []struct {
		name     string
		decision string
		decide   func(DecideOptions) error
	}{
		{
			name:     "customer approve",
			decision: models.ApprovalDecisionApprove,
			decide: func(opts DecideOptions) error {
				_, _, err := svc.DecideAsCustomer(ctx, fx.requestB, fx.customerID, models.ApprovalDecisionApprove, "blocked", opts)
				return err
			},
		},
		{
			name:     "customer reject",
			decision: models.ApprovalDecisionReject,
			decide: func(opts DecideOptions) error {
				_, _, err := svc.DecideAsCustomer(ctx, fx.requestB, fx.customerID, models.ApprovalDecisionReject, "blocked", opts)
				return err
			},
		},
		{
			name:     "internal user comment",
			decision: models.ApprovalDecisionComment,
			decide: func(opts DecideOptions) error {
				_, _, err := svc.Decide(ctx, fx.requestB, fx.userID, models.ApprovalDecisionComment, "blocked", opts)
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.decide(DecideOptions{ChannelID: &fx.channelA}); err == nil {
				t.Fatalf("cross-channel %s unexpectedly succeeded", tc.decision)
			}
		})
	}

	var crossChannelDecisions int
	if err := fx.db.QueryRow(`SELECT COUNT(*) FROM approval_decisions WHERE approval_request_id = ?`, fx.requestB).Scan(&crossChannelDecisions); err != nil {
		t.Fatalf("count cross-channel decisions: %v", err)
	}
	if crossChannelDecisions != 0 {
		t.Fatalf("cross-channel decisions = %d, want 0", crossChannelDecisions)
	}

	decision, _, err := svc.DecideAsCustomer(
		ctx,
		fx.requestA,
		fx.customerID,
		models.ApprovalDecisionComment,
		"same-channel comment",
		DecideOptions{ChannelID: &fx.channelA},
	)
	if err != nil {
		t.Fatalf("same-channel comment: %v", err)
	}
	if decision.ActorPortalCustomerID == nil || *decision.ActorPortalCustomerID != fx.customerID || decision.Comment != "same-channel comment" {
		t.Fatalf("same-channel decision = %+v", decision)
	}
}
