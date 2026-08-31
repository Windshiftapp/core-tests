//go:build test

package services

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type postCommitTransitionTestEnv struct {
	db              database.Database
	workflowService *WorkflowService
	itemID          int
	actorUserID     int
	oldStatusID     int
	newStatusID     int
}

func newPostCommitTransitionTestEnv(t *testing.T) postCommitTransitionTestEnv {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	db := tdb.DB

	insertID := func(query string, args ...any) int {
		t.Helper()
		return testutils.InsertID(t, db, query, args...)
	}

	workspaceID := insertID(`INSERT INTO workspaces (name, key, active, is_personal) VALUES ('Post-commit hooks', 'PCH', true, false)`)
	actorUserID := insertID(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('post-commit@example.com', 'post-commit', 'Post', 'Commit')
	`)
	var categoryID int
	if err := db.QueryRow(`SELECT id FROM status_categories LIMIT 1`).Scan(&categoryID); err != nil {
		t.Fatalf("load status category: %v", err)
	}
	oldStatusID := insertID(`INSERT INTO statuses (name, category_id) VALUES ('Post-commit old', ?)`, categoryID)
	newStatusID := insertID(`INSERT INTO statuses (name, category_id) VALUES ('Post-commit new', ?)`, categoryID)
	workflowID := insertID(`INSERT INTO workflows (name, description, is_default) VALUES ('post-commit', '', false)`)
	insertID(`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id) VALUES (?, ?, ?)`, workflowID, oldStatusID, newStatusID)
	configurationSetID := insertID(`INSERT INTO configuration_sets (name, workflow_id) VALUES ('post-commit', ?)`, workflowID)
	insertID(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, workspaceID, configurationSetID)
	// The workspace now resolves a config set, so the item type the item is
	// created with must be linked into it; reuse the global default type.
	var defaultItemTypeID int
	if err := db.QueryRow(`SELECT id FROM item_types WHERE is_default = true LIMIT 1`).Scan(&defaultItemTypeID); err != nil {
		t.Fatalf("load default item type: %v", err)
	}
	insertID(`INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id) VALUES (?, ?)`, configurationSetID, defaultItemTypeID)
	itemID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: workspaceID,
		ItemTypeID:  &defaultItemTypeID,
		Title:       "Post-commit item",
		StatusID:    &oldStatusID,
		CreatorID:   &actorUserID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	itemID := int(itemID64)

	return postCommitTransitionTestEnv{
		db:              db,
		workflowService: NewWorkflowService(db),
		itemID:          itemID,
		actorUserID:     actorUserID,
		oldStatusID:     oldStatusID,
		newStatusID:     newStatusID,
	}
}

type failingTransitionApprovalService struct {
	pending       *models.ApprovalRequest
	gatingErr     error
	getPendingErr error
	cancelErr     error
	openErr       error
	cancelCalls   int
	openCalls     int
}

func (s *failingTransitionApprovalService) IsTransitionGatedByApproval(context.Context, int, int, int) (*int, error) {
	return nil, s.gatingErr
}

func (s *failingTransitionApprovalService) GetPendingForItem(context.Context, int) (*models.ApprovalRequest, error) {
	return s.pending, s.getPendingErr
}

func (s *failingTransitionApprovalService) Cancel(context.Context, int, int, string, string) error {
	s.cancelCalls++
	return s.cancelErr
}

func (s *failingTransitionApprovalService) MaybeOpenForStatusEntry(context.Context, int, int, int, int) (*models.ApprovalRequest, error) {
	s.openCalls++
	return nil, s.openErr
}

func TestPerformTransitionReturnsCommittedResultWhenApprovalHookFails(t *testing.T) {
	tests := []struct {
		name            string
		hook            string
		errorPrefix     string
		configure       func(*failingTransitionApprovalService, error)
		wantCancelCalls int
		wantOpenCalls   int
	}{
		{
			name:        "load pending approval",
			hook:        "get_pending",
			errorPrefix: "get pending approval after commit",
			configure: func(service *failingTransitionApprovalService, err error) {
				service.getPendingErr = err
			},
		},
		{
			name:        "cancel pending approval",
			hook:        "cancel",
			errorPrefix: "cancel approval after commit",
			configure: func(service *failingTransitionApprovalService, err error) {
				service.pending = &models.ApprovalRequest{ID: 42}
				service.cancelErr = err
			},
			wantCancelCalls: 1,
		},
		{
			name:        "open destination approval",
			hook:        "maybe_open",
			errorPrefix: "open approval after commit",
			configure: func(service *failingTransitionApprovalService, err error) {
				service.openErr = err
			},
			wantOpenCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newPostCommitTransitionTestEnv(t)
			hookErr := errors.New("hook database unavailable")
			approvalService := &failingTransitionApprovalService{}
			tt.configure(approvalService, hookErr)

			var logs bytes.Buffer
			previousLogger := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previousLogger) })

			result, err := env.workflowService.PerformTransition(context.Background(), PerformTransitionRequest{
				ItemID:      env.itemID,
				ToStatusID:  env.newStatusID,
				ActorUserID: env.actorUserID,
			}, repository.NewItemRepository(env.db), nil, approvalService)

			if !errors.Is(err, hookErr) {
				t.Fatalf("error = %v, want wrapped hook error", err)
			}
			if want := tt.errorPrefix + ": " + hookErr.Error(); err.Error() != want {
				t.Fatalf("error = %q, want %q", err, want)
			}
			if result == nil || result.Item == nil {
				t.Fatalf("result = %#v, want committed transition result", result)
			}
			if result.NoOp {
				t.Fatal("NoOp = true, want false")
			}
			if result.OldStatusID == nil || *result.OldStatusID != env.oldStatusID {
				t.Fatalf("OldStatusID = %v, want %d", result.OldStatusID, env.oldStatusID)
			}
			if result.NewStatusID == nil || *result.NewStatusID != env.newStatusID {
				t.Fatalf("NewStatusID = %v, want %d", result.NewStatusID, env.newStatusID)
			}
			if result.Item.StatusID == nil || *result.Item.StatusID != env.newStatusID {
				t.Fatalf("result item status = %v, want %d", result.Item.StatusID, env.newStatusID)
			}
			var storedStatusID int
			if queryErr := env.db.QueryRow("SELECT status_id FROM items WHERE id = ?", env.itemID).Scan(&storedStatusID); queryErr != nil {
				t.Fatalf("load stored status: %v", queryErr)
			}
			if storedStatusID != env.newStatusID {
				t.Fatalf("stored status = %d, want committed status %d", storedStatusID, env.newStatusID)
			}
			if approvalService.cancelCalls != tt.wantCancelCalls {
				t.Fatalf("Cancel calls = %d, want %d", approvalService.cancelCalls, tt.wantCancelCalls)
			}
			if approvalService.openCalls != tt.wantOpenCalls {
				t.Fatalf("MaybeOpenForStatusEntry calls = %d, want %d", approvalService.openCalls, tt.wantOpenCalls)
			}

			logOutput := logs.String()
			for _, want := range []string{
				`"level":"ERROR"`,
				`"msg":"post-commit approval hook failed"`,
				`"hook":"` + tt.hook + `"`,
				`"item_id":`,
				`"error":"hook database unavailable"`,
			} {
				if !strings.Contains(logOutput, want) {
					t.Errorf("log output %q does not contain %q", logOutput, want)
				}
			}
		})
	}
}
