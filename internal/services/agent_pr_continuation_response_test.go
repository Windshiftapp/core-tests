package services

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

func newContinuationResponseService(t *testing.T) (*AgentPRService, database.Database, *PRCommentRequest, int, int64) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s/continuation-response.db?mode=memory&cache=shared", t.TempDir())
	db, err := database.NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO workspaces(id,name,key) VALUES (1,'Windshift','WI')`,
		`INSERT INTO users(id,email,username,first_name,last_name,is_agent) VALUES (1,'admin@example.test','admin','Admin','User',FALSE)`,
		`INSERT INTO users(id,email,username,first_name,last_name,is_agent) VALUES (2,'agent@example.test','agent','Coding','Agent',TRUE)`,
		`INSERT INTO scm_providers(id,slug,name,provider_type,auth_method,enabled) VALUES (1,'gh','GitHub','github','pat',TRUE)`,
		`INSERT INTO workspace_scm_connections(id,workspace_id,scm_provider_id,enabled) VALUES (2,1,1,TRUE)`,
		`INSERT INTO workspace_repositories(id,workspace_scm_connection_id,repository_external_id,repository_name,repository_url,default_branch,is_active) VALUES (5,2,'5','acme/repo','https://example.test/acme/repo','main',true)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	// Create the item through the production path (rank, defaults), then pin
	// its id so the hand-written agent_runs / review-event fixture rows below
	// can reference a stable item id.
	createdItemID, err := CreateItem(db, ItemCreationParams{WorkspaceID: 1, Title: "Continuation response"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE items SET id = 10 WHERE id = ?`, int(createdItemID)); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO workspace_agent_bindings(id,workspace_id,acting_user_id,acting_user_kind,repo_slug,repo_base_ref,scm_connection_id,created_by_user_id) VALUES (3,1,2,'agent','acme/repo','main',2,1)`,
		`INSERT INTO workspace_agent_binding_repos(binding_id,scm_connection_id,repo_slug,repo_base_ref,is_primary) VALUES (3,2,'acme/repo','main',true)`,
		`INSERT INTO agent_runs(id,workspace_id,item_id,binding_id,status,triggered_by_user_id) VALUES (42,1,10,3,'running',1)`,
		`INSERT INTO agent_pr_review_events(id,workspace_repository_id,workspace_id,item_id,pr_number,event_kind,external_id,body,status,agent_run_id) VALUES (9,5,1,10,7,'issue_comment',77,'@agent fix this','running',42)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var captured PRCommentRequest
	service, err := NewAgentPRService(AgentPRServiceOptions{
		Bindings: repository.NewWorkspaceAgentBindingRepository(db), DB: db,
		OpenPR:    func(context.Context, OpenPRRequest) (*OpenedPR, error) { return nil, fmt.Errorf("unexpected fresh PR") },
		CommentPR: func(_ context.Context, request PRCommentRequest) error { captured = request; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, db, &captured, 10, 9
}

func TestAgentPRContinuation_FailurePostsTerminalResponse(t *testing.T) {
	service, db, captured, itemID, eventID := newContinuationResponseService(t)
	service.AfterRun(context.Background(), PostRunInfo{
		RunID: 42, WorkspaceID: 1, ItemID: &itemID, BindingID: 3,
		Status: models.AgentRunStatusFailed, Error: "runner exited", TriggeredByUserID: 1,
		Trigger: &models.RunTrigger{ContinuePRNumber: 7, ContinueRepoSlug: "acme/repo", ContinueHeadBranch: "agent-runs/run-42", ContinueEventID: eventID},
	})
	if captured.Number != 7 || !strings.Contains(captured.Body, "could not complete") || !strings.Contains(captured.Body, "runner exited") {
		t.Fatalf("unexpected reply: %+v", *captured)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM agent_pr_review_events WHERE id=9`).Scan(&status); err != nil || status != "replied" {
		t.Fatalf("status=%q err=%v", status, err)
	}
}

func TestAgentPRContinuation_NoChangesStillReplies(t *testing.T) {
	service, _, captured, itemID, eventID := newContinuationResponseService(t)
	service.AfterRun(context.Background(), PostRunInfo{
		RunID: 42, WorkspaceID: 1, ItemID: &itemID, BindingID: 3,
		Status: models.AgentRunStatusSucceeded, Summary: "The requested behavior is already covered.", TriggeredByUserID: 1,
		Trigger: &models.RunTrigger{ContinuePRNumber: 7, ContinueRepoSlug: "acme/repo", ContinueHeadBranch: "agent-runs/run-42", ContinueEventID: eventID},
	})
	if !strings.Contains(captured.Body, "no code changes were needed") || !strings.Contains(captured.Body, "already covered") {
		t.Fatalf("unexpected reply: %s", captured.Body)
	}
}

func TestAgentPRService_RecordsStablePROwnership(t *testing.T) {
	service, db, _, itemID, _ := newContinuationResponseService(t)
	service.openPR = func(_ context.Context, request OpenPRRequest) (*OpenedPR, error) {
		return &OpenedPR{ID: "700", Number: 7, URL: "https://example.test/acme/repo/pulls/7", Title: request.Title, State: "open", Author: "admin"}, nil
	}
	service.AfterRun(context.Background(), PostRunInfo{
		RunID: 42, WorkspaceID: 1, ItemID: &itemID, BindingID: 3,
		Status: models.AgentRunStatusSucceeded, Branch: "agent-runs/run-42", BaseCommit: "abc", TriggeredByUserID: 1,
	})
	var runID, bindingID, principal int
	var headRepo, headBranch string
	if err := db.QueryRow(`SELECT agent_run_id,binding_id,triggered_by_user_id,head_repo,head_branch FROM agent_pr_ownerships WHERE workspace_repository_id=5 AND pr_number=7`).Scan(&runID, &bindingID, &principal, &headRepo, &headBranch); err != nil {
		t.Fatal(err)
	}
	if runID != 42 || bindingID != 3 || principal != 1 || headRepo != "acme/repo" || headBranch != "agent-runs/run-42" {
		t.Fatalf("ownership run=%d binding=%d principal=%d head=%s:%s", runID, bindingID, principal, headRepo, headBranch)
	}
}
