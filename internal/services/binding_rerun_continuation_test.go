//go:build test

package services

import (
	"context"
	"testing"

	"windshift/internal/models"
)

// applyContinuation is the shared seam that turns a trigger into a continuation
// when the binding's item has an open linked PR in a repo the binding writes
// to. These tests pin its guards directly, independent of any one trigger.
func TestBindingService_applyContinuation(t *testing.T) {
	ctx := context.Background()
	repoBinding := &models.WorkspaceAgentBinding{
		WorkspaceID: 1,
		Repos:       []models.BindingRepo{{RepoSlug: "acme/widget"}},
	}

	t.Run("open PR in a bound repo sets the continuation fields", func(t *testing.T) {
		stub := &stubContinuationResolver{Fn: func(int) (*ContinuationTarget, error) {
			return &ContinuationTarget{PRNumber: 7, RepoSlug: "acme/widget", HeadBranch: "agent-runs/run-7"}, nil
		}}
		bs := &BindingService{continuations: stub, logger: silentLogger(t)}
		trigger := &models.RunTrigger{Kind: "rerun"}

		bs.applyContinuation(ctx, trigger, repoBinding, 42)

		if !trigger.IsContinuation() {
			t.Fatal("expected trigger to become a continuation")
		}
		if trigger.ContinueHeadBranch != "agent-runs/run-7" || trigger.ContinuePRNumber != 7 || trigger.ContinueRepoSlug != "acme/widget" {
			t.Fatalf("continuation fields not set: branch=%q pr=%d repo=%q",
				trigger.ContinueHeadBranch, trigger.ContinuePRNumber, trigger.ContinueRepoSlug)
		}
	})

	t.Run("PR in an unbound repo is ignored", func(t *testing.T) {
		stub := &stubContinuationResolver{Fn: func(int) (*ContinuationTarget, error) {
			return &ContinuationTarget{PRNumber: 9, RepoSlug: "acme/other", HeadBranch: "x"}, nil
		}}
		bs := &BindingService{continuations: stub, logger: silentLogger(t)}
		trigger := &models.RunTrigger{Kind: "rerun"}

		bs.applyContinuation(ctx, trigger, repoBinding, 42)

		if trigger.IsContinuation() {
			t.Fatalf("PR in unbound repo must not continue, got branch=%q", trigger.ContinueHeadBranch)
		}
	})

	t.Run("no open PR is a no-op", func(t *testing.T) {
		stub := &stubContinuationResolver{} // Fn nil → no target
		bs := &BindingService{continuations: stub, logger: silentLogger(t)}
		trigger := &models.RunTrigger{Kind: "rerun"}

		bs.applyContinuation(ctx, trigger, repoBinding, 42)

		if trigger.IsContinuation() {
			t.Fatal("no target must leave a fresh-run trigger")
		}
	})

	t.Run("binding with no repo never consults the resolver", func(t *testing.T) {
		stub := &stubContinuationResolver{Fn: func(int) (*ContinuationTarget, error) {
			return &ContinuationTarget{PRNumber: 7, RepoSlug: "acme/widget", HeadBranch: "x"}, nil
		}}
		bs := &BindingService{continuations: stub, logger: silentLogger(t)}
		trigger := &models.RunTrigger{Kind: "rerun"}

		bs.applyContinuation(ctx, trigger, &models.WorkspaceAgentBinding{WorkspaceID: 1}, 42)

		if len(stub.Calls) != 0 {
			t.Fatalf("repo-less binding must short-circuit before the resolver, calls=%v", stub.Calls)
		}
		if trigger.IsContinuation() {
			t.Fatal("repo-less binding must not continue")
		}
	})
}

// Re-run on an item that still has an open PR continues that PR instead of
// forking a competing branch — the gap WI-451 closes (the @mention and PR-comment
// poller paths already continued; the manual Re-run button did not).
func TestBindingService_Rerun_ContinuesOpenPR(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)

	// A repo-bound binding: continuation only applies when the binding can push
	// to the PR's repo, so the binding needs that repo attached.
	conn := seedServiceSCMConnection(t, st, 1)
	bindingID := seedMentionBinding(t, st, st.AgentID)
	if err := st.Bindings.ReplaceBindingRepos(ctx, bindingID, []models.BindingRepo{
		{SCMConnectionID: &conn, RepoSlug: "acme/widget", IsPrimary: true},
	}); err != nil {
		t.Fatalf("attach repo: %v", err)
	}
	itemID := seedItem(t, st.DB, 1)
	if _, err := st.DB.Exec(`INSERT INTO agent_runs(workspace_id, item_id, binding_id, status) VALUES (1, ?, ?, ?)`,
		itemID, bindingID, models.AgentRunStatusSucceeded); err != nil {
		t.Fatalf("seed prior run: %v", err)
	}

	// The item has an open PR on this binding's repo.
	st.Continuations.Fn = func(int) (*ContinuationTarget, error) {
		return &ContinuationTarget{PRNumber: 7, RepoSlug: "acme/widget", HeadBranch: "agent-runs/run-7"}, nil
	}

	started, err := st.BS.RerunForItem(ctx, itemID, st.AdminID)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if !started {
		t.Fatal("want started=true")
	}
	st.BS.runs.Wait()
	// Re-run must have consulted the continuation resolver for this item — proof
	// it no longer unconditionally cuts a fresh branch. (The resolver is consulted
	// synchronously inside RerunForItem, before the run dispatches.)
	found := false
	for _, id := range st.Continuations.Calls {
		if id == itemID {
			found = true
		}
	}
	if !found {
		t.Fatalf("re-run did not consult the continuation resolver for item %d (calls=%v)", itemID, st.Continuations.Calls)
	}
}

// seedServiceSCMConnection inserts an scm_providers + workspace_scm_connections
// row and returns the connection id, so a binding's repo FK
// (scm_connection_id → workspace_scm_connections) resolves.
func seedServiceSCMConnection(t *testing.T, st *bindingTestStack, workspaceID int) int {
	t.Helper()
	pres, err := st.DB.Exec(`INSERT INTO scm_providers(slug, name, provider_type, auth_method, enabled) VALUES ('gh', 'GitHub', 'github', 'pat', TRUE)`)
	if err != nil {
		t.Fatalf("seed scm provider: %v", err)
	}
	providerID, _ := pres.LastInsertId()
	cres, err := st.DB.Exec(`INSERT INTO workspace_scm_connections(workspace_id, scm_provider_id, enabled) VALUES (?, ?, TRUE)`,
		workspaceID, providerID)
	if err != nil {
		t.Fatalf("seed scm connection: %v", err)
	}
	id, _ := cres.LastInsertId()
	return int(id)
}
