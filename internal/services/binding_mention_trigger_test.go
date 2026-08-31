//go:build test

package services

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
)

type recordingStandardDispatcher struct {
	starts  atomic.Int32
	cancels atomic.Int32
}

func (d *recordingStandardDispatcher) StartItemRun(context.Context, *models.WorkspaceAgentBinding, int, int, int, *models.RunTrigger) error {
	d.starts.Add(1)
	return nil
}

func (d *recordingStandardDispatcher) CancelForBinding(context.Context, int) error {
	d.cancels.Add(1)
	return nil
}

// WI-264: @mentioning a binding's acting user in an item comment starts a
// run — without changing assignee or status. These cover the trigger's
// guards: self-mention skip, non-agent no-op, per-item active-run dedup,
// in-comment dedup, and multi-agent fan-out.

// seedMentionBinding inserts a repo-less binding for the given acting user
// (the RepoSpec threading is covered by the assignee-trigger tests).
func seedMentionBinding(t *testing.T, st *bindingTestStack, actingUserID int) int {
	t.Helper()
	id, err := st.Bindings.Insert(context.Background(), &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    actingUserID,
		ActingUserKind:  ActingIdentityKindAgent,
		TokenScopes:     []string{"items:read"},
		TokenTTLMinutes: 15,
		CreatedByUserID: st.AdminID,
	})
	if err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	return id
}

func TestBindingService_MentionTrigger_StartsRunForMentionedAgent(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	seedMentionBinding(t, st, st.AgentID)
	itemID := seedItem(t, st.DB, 1)

	const instruction = "please rename the Foo button to Bar"
	if err := st.BS.MaybeStartRunsForMentions(ctx, 1, itemID, []int{st.AgentID}, st.AdminID, instruction, 4242); err != nil {
		t.Fatalf("mention trigger: %v", err)
	}
	st.BS.runs.Wait()

	if got := atomic.LoadInt32(st.RunCalls); got != 1 {
		t.Fatalf("expected 1 runner invocation, got %d", got)
	}

	// The run is attributed to the comment author (the WI-275 credential
	// principal) and linked to the commented item.
	var triggeredBy, runItemID int
	if err := st.DB.QueryRow(`SELECT triggered_by_user_id, item_id FROM agent_runs LIMIT 1`).Scan(&triggeredBy, &runItemID); err != nil {
		t.Fatalf("read run: %v", err)
	}
	if triggeredBy != st.AdminID {
		t.Errorf("triggered_by_user_id: want comment author %d, got %d", st.AdminID, triggeredBy)
	}
	if runItemID != itemID {
		t.Errorf("item_id: want %d, got %d", itemID, runItemID)
	}

	// The @mentioning comment is persisted as the run's instruction (trigger_json)
	// so it survives the queue→claim hop and reaches the agent's prompt.
	run, err := repository.NewAgentRunRepository(st.DB).Get(ctx, 1)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Trigger == nil {
		t.Fatalf("run.Trigger: want persisted mention trigger, got nil")
	}
	if run.Trigger.Kind != "mention" || run.Trigger.Instruction != instruction {
		t.Errorf("run.Trigger: got kind=%q instruction=%q", run.Trigger.Kind, run.Trigger.Instruction)
	}
	if run.Trigger.CommentID != 4242 || run.Trigger.AuthorID != st.AdminID {
		t.Errorf("run.Trigger refs: got comment=%d author=%d", run.Trigger.CommentID, run.Trigger.AuthorID)
	}
}

func TestBindingService_MentionTrigger_SelfMentionSkipped(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	seedMentionBinding(t, st, st.AgentID)
	itemID := seedItem(t, st.DB, 1)

	// The agent's own comment mentioning itself must not loop.
	if err := st.BS.MaybeStartRunsForMentions(ctx, 1, itemID, []int{st.AgentID}, st.AgentID, "", 0); err != nil {
		t.Fatalf("mention trigger: %v", err)
	}
	st.BS.runs.Wait()
	if got := atomic.LoadInt32(st.RunCalls); got != 0 {
		t.Fatalf("self-mention must not start a run, got %d invocations", got)
	}
}

func TestBindingService_MentionTrigger_NonAgentMentionIsNoOp(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	itemID := seedItem(t, st.DB, 1)

	// Mentioning a regular human (no binding) is the notification
	// pipeline's business, not ours.
	if err := st.BS.MaybeStartRunsForMentions(ctx, 1, itemID, []int{st.AdminID, st.SvcUserID}, st.AdminID, "", 0); err != nil {
		t.Fatalf("mention trigger: %v", err)
	}
	if got := atomic.LoadInt32(st.RunCalls); got != 0 {
		t.Fatalf("expected no runs for non-agent mentions, got %d", got)
	}
}

func TestBindingService_MentionTrigger_DedupSkipsActiveRunOnItem(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	bindingID := seedMentionBinding(t, st, st.AgentID)
	itemID := seedItem(t, st.DB, 1)

	// An agent already working the item: a fresh mention is a nudge, not a
	// second job.
	if _, err := st.DB.Exec(`INSERT INTO agent_runs(workspace_id, item_id, binding_id, status) VALUES (1, ?, ?, ?)`,
		itemID, bindingID, models.AgentRunStatusRunning); err != nil {
		t.Fatalf("seed active run: %v", err)
	}

	if err := st.BS.MaybeStartRunsForMentions(ctx, 1, itemID, []int{st.AgentID}, st.AdminID, "", 0); err != nil {
		t.Fatalf("mention trigger: %v", err)
	}
	if got := atomic.LoadInt32(st.RunCalls); got != 0 {
		t.Fatalf("mention with an active run on the item must dedup, got %d invocations", got)
	}

	// A mention on a DIFFERENT item is not deduped by the first item's run.
	otherItem := seedItem(t, st.DB, 1)
	if err := st.BS.MaybeStartRunsForMentions(ctx, 1, otherItem, []int{st.AgentID}, st.AdminID, "", 0); err != nil {
		t.Fatalf("mention trigger (other item): %v", err)
	}
	st.BS.runs.Wait()
	if got := atomic.LoadInt32(st.RunCalls); got != 1 {
		t.Fatalf("expected 1 run on the other item, got %d", got)
	}

	// Once the original run finishes, a later mention triggers again.
	if _, err := st.DB.Exec(`UPDATE agent_runs SET status = ? WHERE item_id = ? AND binding_id = ?`,
		models.AgentRunStatusSucceeded, itemID, bindingID); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	if err := st.BS.MaybeStartRunsForMentions(ctx, 1, itemID, []int{st.AgentID}, st.AdminID, "", 0); err != nil {
		t.Fatalf("mention trigger (after finish): %v", err)
	}
	st.BS.runs.Wait()
	if got := atomic.LoadInt32(st.RunCalls); got != 2 {
		t.Fatalf("expected a new run after the previous one finished, got %d total", got)
	}
}

func TestBindingService_StandardMentionsQueueEveryEligibleTrigger(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	bindingID := seedMentionBinding(t, st, st.AgentID)
	if _, err := st.DB.Exec(`
		UPDATE workspace_agent_bindings
		SET profile_type = 'standard', lifecycle = 'ready'
		WHERE id = ?
	`, bindingID); err != nil {
		t.Fatal(err)
	}
	itemID := seedItem(t, st.DB, 1)
	if _, err := st.DB.Exec(`
		INSERT INTO agent_runs(workspace_id, item_id, binding_id, status, job_kind)
		VALUES (1, ?, ?, 'running', 'standard_agent')
	`, itemID, bindingID); err != nil {
		t.Fatal(err)
	}
	dispatcher := &recordingStandardDispatcher{}
	st.BS.SetStandardRunDispatcher(dispatcher)

	for commentID := 1; commentID <= 2; commentID++ {
		if err := st.BS.MaybeStartRunsForMentions(ctx, 1, itemID, []int{st.AgentID},
			st.AdminID, "another eligible trigger", commentID); err != nil {
			t.Fatalf("mention %d: %v", commentID, err)
		}
	}
	if got := dispatcher.starts.Load(); got != 2 {
		t.Fatalf("Standard mentions must queue every eligible trigger, got %d dispatches", got)
	}
}

func TestBindingService_AgentTriggerCannotEnterCodingOrLegacyRunner(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	bindingID := seedMentionBinding(t, st, st.AgentID)
	binding, err := st.Bindings.Get(ctx, bindingID)
	if err != nil {
		t.Fatal(err)
	}
	itemID := seedItem(t, st.DB, 1)
	err = st.BS.startRunForBinding(ctx, binding, 1, itemID, st.AgentID,
		&models.RunTrigger{Kind: "mention", AuthorID: st.AgentID})
	if !errors.Is(err, ErrAgentChainUnsupported) {
		t.Fatalf("agent-triggered Legacy run error = %v, want ErrAgentChainUnsupported", err)
	}
	if got := atomic.LoadInt32(st.RunCalls); got != 0 {
		t.Fatalf("agent trigger reached the Coding/Legacy runner %d time(s)", got)
	}
}

func TestBindingService_ArchiveCancelsStandardRuns(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	bindingID := seedMentionBinding(t, st, st.AgentID)
	if _, err := st.DB.Exec(`
		UPDATE workspace_agent_bindings
		SET profile_type = 'standard', lifecycle = 'ready'
		WHERE id = ?
	`, bindingID); err != nil {
		t.Fatal(err)
	}
	dispatcher := &recordingStandardDispatcher{}
	st.BS.SetStandardRunDispatcher(dispatcher)

	affected, err := st.BS.Delete(ctx, bindingID, 1, st.AdminID)
	if err != nil {
		t.Fatalf("archive Standard profile: %v", err)
	}
	if affected != 1 || dispatcher.cancels.Load() != 1 {
		t.Fatalf("archive affected=%d Standard cancels=%d", affected, dispatcher.cancels.Load())
	}
}

func TestBindingService_MentionTrigger_MultipleAgentsFanOut(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	owner := st.AdminID
	secondAgent := seedIdentityUser(t, st.DB, "bob-agent@agents.local", "bob-agent", "Bob", "Agent", true, &owner, true)
	seedMentionBinding(t, st, st.AgentID)
	seedMentionBinding(t, st, secondAgent)
	itemID := seedItem(t, st.DB, 1)

	// Two distinct agents in one comment each get a run; the repeated
	// mention of the first agent is deduplicated within the comment.
	if err := st.BS.MaybeStartRunsForMentions(ctx, 1, itemID, []int{st.AgentID, secondAgent, st.AgentID}, st.AdminID, "", 0); err != nil {
		t.Fatalf("mention trigger: %v", err)
	}
	st.BS.runs.Wait()
	if got := atomic.LoadInt32(st.RunCalls); got != 2 {
		t.Fatalf("expected 2 runs (one per distinct agent), got %d", got)
	}

	repo := repository.NewAgentRunRepository(st.DB)
	runs, err := repo.ListForWorkspace(ctx, 1, 10, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 persisted runs, got %d", len(runs))
	}
}

// renderInstruction frames the @mentioning comment as the run's directive.
// A trigger with no instruction (e.g. a bare assignment change) renders
// nothing so the static prompt stands alone.
func TestRenderInstruction(t *testing.T) {
	if got := renderInstruction(nil); got != "" {
		t.Errorf("nil trigger: want empty, got %q", got)
	}
	if got := renderInstruction(&models.RunTrigger{Kind: "assignee"}); got != "" {
		t.Errorf("instruction-less trigger: want empty, got %q", got)
	}
	got := renderInstruction(&models.RunTrigger{Kind: "mention", Instruction: "line one\nline two"})
	if !strings.Contains(got, "## Your instruction for this run") {
		t.Errorf("missing instruction heading: %q", got)
	}
	if !strings.Contains(got, "> line one") || !strings.Contains(got, "> line two") {
		t.Errorf("comment not blockquoted verbatim: %q", got)
	}
}

// The remote claim path re-derives the prompt at claim time from the run's
// persisted Trigger: ResolveRunInputs must fold the instruction into the
// prompt suffix so it reaches the agent on a pool-backed run.
func TestBindingService_ResolveRunInputs_FoldsInstructionIntoPrompt(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	bindingID := seedMentionBinding(t, st, st.AgentID)
	itemID := seedItem(t, st.DB, 1)

	repo := repository.NewAgentRunRepository(st.DB)
	pool := 7
	runID, err := repo.Insert(ctx, &models.AgentRun{
		WorkspaceID:  1,
		ItemID:       &itemID,
		BindingID:    &bindingID,
		TargetPoolID: &pool,
		JobKind:      models.JobKindCodingAgent,
		Status:       models.AgentRunStatusQueued,
		Trigger:      &models.RunTrigger{Kind: "mention", Instruction: "rename Foo to Bar"},
	})
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
	run, err := repo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	inputs, err := st.BS.ResolveRunInputs(ctx, run)
	if err != nil {
		t.Fatalf("resolve run inputs: %v", err)
	}
	if inputs == nil {
		t.Fatal("resolve run inputs: nil")
	}
	if !strings.Contains(inputs.PromptSuffix, "## Your instruction for this run") ||
		!strings.Contains(inputs.PromptSuffix, "> rename Foo to Bar") {
		t.Errorf("instruction not folded into prompt suffix: %q", inputs.PromptSuffix)
	}
}

func TestMentionService_ResolveMentionedUserIDs(t *testing.T) {
	st := newBindingTestStack(t, false)
	ms := NewMentionService(st.DB, nil, nil)

	// "alice" (username) + "Alice Agent" (display name of alice-agent) +
	// an unknown handle + a duplicate. seedIdentityUser data comes from
	// newBindingTestStack.
	ids, err := ms.ResolveMentionedUserIDs(`Hey @alice and @"Alice Agent", please review (cc @alice, @nobody-here)`)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := []int{st.AdminID, st.AgentID}
	if len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] {
		t.Errorf("resolved ids: want %v (alice, alice-agent), got %v", want, ids)
	}
}

func TestMentionService_ResolveMentionedUserIDsTrimsDisplayName(t *testing.T) {
	st := newBindingTestStack(t, false)
	ms := NewMentionService(st.DB, nil, nil)

	if _, err := st.DB.Exec(`
		UPDATE users
		SET first_name = 'Luna Coder', last_name = ''
		WHERE id = ?
	`, st.AgentID); err != nil {
		t.Fatalf("set agent display name: %v", err)
	}

	ids, err := ms.ResolveMentionedUserIDs(`@"Luna Coder" please create this as a page`)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(ids) != 1 || ids[0] != st.AgentID {
		t.Fatalf("resolved ids = %v, want [%d]", ids, st.AgentID)
	}
}
