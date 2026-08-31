package standardagent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"windshift/internal/llm"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

type fixedResolver struct {
	client llm.Client
	err    error
}

func (r fixedResolver) Resolve(int) (llm.Client, error) {
	return r.client, r.err
}

type finalAnswerClient struct {
	answer string
}

func (c finalAnswerClient) Complete(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return &llm.CompletionResponse{Choices: []llm.Choice{{
		Message:      llm.Message{Role: "assistant", Content: c.answer},
		FinishReason: "stop",
	}}}, nil
}

func (finalAnswerClient) Health(context.Context) error { return nil }
func (finalAnswerClient) Available() bool              { return true }

type toolCapturingClient struct {
	tools []string
}

func (c *toolCapturingClient) Complete(_ context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	for _, tool := range req.Tools {
		c.tools = append(c.tools, tool.Function.Name)
	}
	return &llm.CompletionResponse{Choices: []llm.Choice{{
		Message:      llm.Message{Role: "assistant", Content: "Private test complete."},
		FinishReason: "stop",
	}}}, nil
}

func (*toolCapturingClient) Health(context.Context) error { return nil }
func (*toolCapturingClient) Available() bool              { return true }

type readThenAnswerClient struct {
	calls int
}

func (c *readThenAnswerClient) Complete(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
	c.calls++
	if c.calls == 1 {
		return &llm.CompletionResponse{Choices: []llm.Choice{{
			Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{
				ID:       "read-item",
				Type:     "function",
				Function: llm.FunctionCall{Name: "get_item", Arguments: `{"item_id":101}`},
			}}},
			FinishReason: "tool_calls",
		}}}, nil
	}
	return &llm.CompletionResponse{Choices: []llm.Choice{{
		Message:      llm.Message{Role: "assistant", Content: "Reviewed the item."},
		FinishReason: "stop",
	}}}, nil
}

func (*readThenAnswerClient) Health(context.Context) error { return nil }
func (*readThenAnswerClient) Available() bool              { return true }

func newStandardRuntime(t *testing.T, resolver LLMResolver) (*Dispatcher, *repository.AgentRunRepository, int, int, int) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	db := tdb.GetDatabase()
	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (1, 'Runtime', 'RUN', TRUE)`); err != nil {
		t.Fatal(err)
	}
	humanID := testutils.InsertID(t, db, `
		INSERT INTO users(email, username, first_name, last_name, is_agent, is_active)
		VALUES ('human@example.com', 'human', 'Human', 'User', false, true)
	`)
	agentID := testutils.InsertID(t, db, `
		INSERT INTO users(email, username, first_name, last_name, is_agent, is_active)
		VALUES ('standard@agents.local', 'standard-agent', 'Standard', 'Agent', true, true)
	`)
	if _, err := db.Exec(`
		INSERT INTO user_workspace_roles(user_id, workspace_id, role_id, granted_by, granted_at)
		VALUES (?, 1, (SELECT id FROM workspace_roles WHERE name = 'Viewer'), ?, CURRENT_TIMESTAMP)
	`, agentID, humanID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO items(id, workspace_id, workspace_item_number, title, description, is_task, frac_index, creator_id, created_at, updated_at)
		VALUES (101, 1, 1, 'Execute Standard agent', '', false, 'a0', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, humanID); err != nil {
		t.Fatal(err)
	}
	perm, err := services.NewPermissionService(db, services.PermissionCacheConfig{
		TTL:             time.Minute,
		MaxCacheSize:    32,
		WarmupOnStartup: false,
		PreWarmActive:   false,
		BatchSize:       10,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = perm.Close() })
	comments := services.NewCommentService(db)
	runs := repository.NewAgentRunRepository(db)
	dispatcher, err := New(Options{
		DB:          db,
		Runs:        runs,
		Bindings:    repository.NewWorkspaceAgentBindingRepository(db),
		LLMs:        resolver,
		Permissions: perm,
		Comments:    comments,
		RunTimeout:  time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dispatcher.Close(context.Background()) })
	return dispatcher, runs, humanID, agentID, 101
}

func insertRunningStandard(t *testing.T, dispatcher *Dispatcher, runs *repository.AgentRunRepository, humanID, agentID, itemID int, private bool) *models.AgentRun {
	t.Helper()
	db := dispatcher.opts.DB
	commentID := testutils.InsertID(t, db, `
		INSERT INTO comments(item_id, author_id, content, is_private, created_at, updated_at)
		VALUES (?, ?, 'please handle this', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, itemID, humanID, private)
	bindingID := 71
	snapshot := marshalSafe(ProfileSnapshot{
		BindingID:      bindingID,
		ProfileVersion: 4,
		ActingUserID:   agentID,
		ActingName:     "Standard Agent",
		Instructions:   "Handle the request.",
		ToolNames:      []string{},
	})
	runID, err := runs.Insert(context.Background(), &models.AgentRun{
		WorkspaceID:            1,
		ItemID:                 &itemID,
		BindingID:              &bindingID,
		JobKind:                models.JobKindStandardAgent,
		ActingUserID:           &agentID,
		RootInitiatorUserID:    &humanID,
		ImmediateTriggerUserID: &humanID,
		ProfileVersion:         4,
		ProfileSnapshotJSON:    snapshot,
		Trigger: &models.RunTrigger{
			Kind:        "mention",
			Instruction: "please handle this",
			CommentID:   commentID,
			AuthorID:    humanID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := runs.ClaimNextStandard(context.Background(), bindingID, itemID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if run == nil || run.ID != runID {
		t.Fatalf("claimed run = %#v, want %d", run, runID)
	}
	return run
}

func TestDispatcherExecutePostsAgentAuthoredCommentWithInheritedPrivacy(t *testing.T) {
	dispatcher, runs, humanID, agentID, itemID := newStandardRuntime(t,
		fixedResolver{client: finalAnswerClient{answer: "Completed the requested review."}})
	run := insertRunningStandard(t, dispatcher, runs, humanID, agentID, itemID, true)

	dispatcher.execute(run)

	got, err := runs.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.AgentRunStatusSucceeded {
		t.Fatalf("run status = %q, want succeeded (error=%q)", got.Status, got.Error)
	}
	var authorID int
	var content string
	var private bool
	if err := dispatcher.opts.DB.QueryRow(`
		SELECT author_id, content, is_private FROM comments
		WHERE item_id = ? ORDER BY id DESC LIMIT 1
	`, itemID).Scan(&authorID, &content, &private); err != nil {
		t.Fatal(err)
	}
	if authorID != agentID || content != "Completed the requested review." || !private {
		t.Fatalf("final comment = author:%d private:%v content:%q", authorID, private, content)
	}
}

func TestDispatcherRunPrivateTestUsesOnlyReadToolsAndPersistsNoRunOrComment(t *testing.T) {
	client := &toolCapturingClient{}
	dispatcher, runs, humanID, agentID, itemID := newStandardRuntime(t,
		fixedResolver{client: client})
	llmID := 1
	result, err := dispatcher.RunPrivateTest(context.Background(), &models.WorkspaceAgentBinding{
		ID:               71,
		WorkspaceID:      1,
		ActingUserID:     agentID,
		ProfileType:      models.AgentProfileStandard,
		Lifecycle:        models.AgentLifecycleDraft,
		ProfileVersion:   3,
		LLMConnectionID:  &llmID,
		Purpose:          "Review workspace work",
		Instructions:     "Use evidence.",
		CapabilityGroups: []string{"issue_management"},
	}, 1, humanID, "Inspect the workspace without changing it.")
	if err != nil {
		t.Fatalf("run private test: %v", err)
	}
	if result.Answer != "Private test complete." || result.Iterations != 1 {
		t.Fatalf("private result = %+v", result)
	}
	if len(client.tools) == 0 {
		t.Fatal("private test exposed no read tools")
	}
	for _, forbidden := range []string{"create_item", "update_item", "delete_item", "add_comment"} {
		if slices.Contains(client.tools, forbidden) {
			t.Fatalf("private test exposed mutating tool %q in %v", forbidden, client.tools)
		}
	}
	var comments int
	if err := dispatcher.opts.DB.QueryRow(`SELECT COUNT(*) FROM comments WHERE item_id = ?`, itemID).Scan(&comments); err != nil {
		t.Fatal(err)
	}
	if comments != 0 {
		t.Fatalf("private test persisted %d comment(s)", comments)
	}
	recent, err := runs.ListForWorkspace(context.Background(), 1, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 0 {
		t.Fatalf("private test persisted runs: %+v", recent)
	}
}

func TestDispatcherExecuteFailureStoresSafeErrorAndPostsNoFinalComment(t *testing.T) {
	const providerSecret = "sk-test-secret-value"
	dispatcher, runs, humanID, agentID, itemID := newStandardRuntime(t,
		fixedResolver{err: errors.New("provider rejected " + providerSecret)})
	run := insertRunningStandard(t, dispatcher, runs, humanID, agentID, itemID, false)

	dispatcher.execute(run)

	got, err := runs.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.AgentRunStatusFailed {
		t.Fatalf("run status = %q, want failed", got.Status)
	}
	if got.Error != "The Standard agent could not complete this run." || strings.Contains(got.Error, providerSecret) {
		t.Fatalf("unsafe persisted error: %q", got.Error)
	}
	var comments int
	if err := dispatcher.opts.DB.QueryRow(`SELECT COUNT(*) FROM comments WHERE item_id = ?`, itemID).Scan(&comments); err != nil {
		t.Fatal(err)
	}
	if comments != 1 {
		t.Fatalf("failure posted a misleading final comment: comment count=%d", comments)
	}
	events, err := runs.ListEvents(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if strings.Contains(event.PayloadJSON, providerSecret) {
			t.Fatalf("event leaked provider detail: %s", event.PayloadJSON)
		}
	}
}

func TestDispatcherToolEventsPersistOnlyNameAndStatus(t *testing.T) {
	client := &readThenAnswerClient{}
	dispatcher, runs, humanID, agentID, itemID := newStandardRuntime(t,
		fixedResolver{client: client})
	const privateItemDetail = "customer-secret-detail"
	if _, err := dispatcher.opts.DB.Exec(`UPDATE items SET description = ? WHERE id = ?`, privateItemDetail, itemID); err != nil {
		t.Fatal(err)
	}
	run := insertRunningStandard(t, dispatcher, runs, humanID, agentID, itemID, false)
	var snapshot ProfileSnapshot
	if err := json.Unmarshal([]byte(run.ProfileSnapshotJSON), &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.ToolNames = []string{"get_item"}
	run.ProfileSnapshotJSON = marshalSafe(snapshot)

	dispatcher.execute(run)

	events, err := runs.ListEvents(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundTool := false
	for _, event := range events {
		if strings.Contains(event.PayloadJSON, privateItemDetail) ||
			strings.Contains(event.PayloadJSON, `"item_id":101`) {
			t.Fatalf("tool event persisted raw arguments or results: %s", event.PayloadJSON)
		}
		if event.Type == "tool" {
			foundTool = true
			var payload map[string]string
			if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
				t.Fatalf("decode tool event: %v", err)
			}
			if len(payload) != 2 || payload["name"] != "get_item" || payload["status"] != "succeeded" {
				t.Fatalf("tool event = %s", event.PayloadJSON)
			}
		}
	}
	if !foundTool {
		t.Fatal("missing sanitized tool event")
	}
}

func TestDispatcherRejectsAgentChainBeyondEightHops(t *testing.T) {
	dispatcher, runs, humanID, agentID, itemID := newStandardRuntime(t,
		fixedResolver{client: finalAnswerClient{answer: "unused"}})
	if _, err := dispatcher.opts.DB.Exec(`
		INSERT INTO user_workspace_roles(user_id, workspace_id, role_id, granted_by, granted_at)
		VALUES (?, 1, (SELECT id FROM workspace_roles WHERE name = 'Editor'), ?, CURRENT_TIMESTAMP)
	`, agentID, humanID); err != nil {
		t.Fatal(err)
	}
	bindingID := 70
	parentID, err := runs.Insert(context.Background(), &models.AgentRun{
		WorkspaceID:            1,
		ItemID:                 &itemID,
		BindingID:              &bindingID,
		JobKind:                models.JobKindStandardAgent,
		ActingUserID:           &agentID,
		RootInitiatorUserID:    &humanID,
		ImmediateTriggerUserID: &agentID,
		ChainDepth:             8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runs.MarkRunningIfQueued(context.Background(), parentID, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	llmID := 1
	err = dispatcher.StartItemRun(context.Background(), &models.WorkspaceAgentBinding{
		ID:              71,
		WorkspaceID:     1,
		ActingUserID:    agentID,
		ProfileType:     models.AgentProfileStandard,
		Lifecycle:       models.AgentLifecycleReady,
		ProfileVersion:  1,
		LLMConnectionID: &llmID,
	}, 1, itemID, agentID, &models.RunTrigger{Kind: "mention", AuthorID: agentID})
	if !errors.Is(err, ErrChainLimit) {
		t.Fatalf("chain limit error = %v, want ErrChainLimit", err)
	}
	var queued int
	if err := dispatcher.opts.DB.QueryRow(`
		SELECT COUNT(*) FROM agent_runs WHERE parent_run_id = ?
	`, parentID).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Fatalf("chain-depth rejection queued %d child run(s)", queued)
	}
}

func TestDispatcherRequiresTriggerActorItemEdit(t *testing.T) {
	dispatcher, _, humanID, agentID, itemID := newStandardRuntime(t,
		fixedResolver{client: finalAnswerClient{answer: "unused"}})
	if _, err := dispatcher.opts.DB.Exec(`
		INSERT INTO user_workspace_roles(user_id, workspace_id, role_id, granted_by, granted_at)
		VALUES (?, 1, (SELECT id FROM workspace_roles WHERE name = 'Viewer'), ?, CURRENT_TIMESTAMP)
	`, humanID, humanID); err != nil {
		t.Fatal(err)
	}
	llmID := 1
	err := dispatcher.StartItemRun(context.Background(), &models.WorkspaceAgentBinding{
		ID:              71,
		WorkspaceID:     1,
		ActingUserID:    agentID,
		ProfileType:     models.AgentProfileStandard,
		Lifecycle:       models.AgentLifecycleReady,
		ProfileVersion:  1,
		LLMConnectionID: &llmID,
	}, 1, itemID, humanID, &models.RunTrigger{Kind: "assignee"})
	if !errors.Is(err, ErrAdmissionDenied) {
		t.Fatalf("admission error = %v, want ErrAdmissionDenied", err)
	}
}

func TestDispatcherQueuesHumanTriggerWithImmutableProfileSnapshot(t *testing.T) {
	dispatcher, runs, humanID, agentID, itemID := newStandardRuntime(t,
		fixedResolver{client: finalAnswerClient{answer: "unused"}})
	if _, err := dispatcher.opts.DB.Exec(`
		INSERT INTO user_workspace_roles(user_id, workspace_id, role_id, granted_by, granted_at)
		VALUES (?, 1, (SELECT id FROM workspace_roles WHERE name = 'Editor'), ?, CURRENT_TIMESTAMP)
	`, humanID, humanID); err != nil {
		t.Fatal(err)
	}
	dispatcher.mu.Lock()
	dispatcher.closed = true
	dispatcher.mu.Unlock()
	t.Cleanup(dispatcher.cancel)
	llmID := 1
	binding := &models.WorkspaceAgentBinding{
		ID:               71,
		WorkspaceID:      1,
		ActingUserID:     agentID,
		ProfileType:      models.AgentProfileStandard,
		Lifecycle:        models.AgentLifecycleReady,
		ProfileVersion:   4,
		LLMConnectionID:  &llmID,
		Purpose:          "Review work",
		Instructions:     "Use the workspace evidence.",
		CapabilityGroups: []string{"issue_management"},
	}
	if err := dispatcher.StartItemRun(context.Background(), binding, 1, itemID, humanID,
		&models.RunTrigger{Kind: "assignee"}); err != nil {
		t.Fatalf("queue Standard run: %v", err)
	}
	queued, err := runs.LatestForBindingItem(context.Background(), binding.ID, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if queued == nil || queued.Status != models.AgentRunStatusQueued ||
		queued.JobKind != models.JobKindStandardAgent ||
		queued.ActingUserID == nil || *queued.ActingUserID != agentID ||
		queued.RootInitiatorUserID == nil || *queued.RootInitiatorUserID != humanID ||
		queued.ImmediateTriggerUserID == nil || *queued.ImmediateTriggerUserID != humanID ||
		queued.ParentRunID != nil || queued.ChainDepth != 0 {
		t.Fatalf("queued Standard contract = %#v", queued)
	}
	var snapshot ProfileSnapshot
	if err := json.Unmarshal([]byte(queued.ProfileSnapshotJSON), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.ProfileVersion != 4 || snapshot.Instructions != binding.Instructions ||
		!slices.Contains(snapshot.ToolNames, "update_item") ||
		slices.Contains(snapshot.ToolNames, "delete_item") {
		t.Fatalf("profile snapshot = %#v", snapshot)
	}
}
