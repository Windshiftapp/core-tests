package services

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repoprep"
	"windshift/internal/repository"
)

// TestBindingService_StartTestRun_Guards covers the synchronous guards before a
// test run is dispatched: a binding with no repo, cross-workspace / missing
// ids, and a server with no coding-agent runner wired.
func TestBindingService_StartTestRun_Guards(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false) // no RunService → runs is nil
	st.BS.scmCreds = &fakeSCMCreds{token: "t", providerType: "gitea", baseURL: "https://gitea.example.com"}

	llmConn := validTestLLMConnectionID
	noRepo, err := st.BS.Create(ctx, CreateBindingRequest{
		WorkspaceID: 1, ActingUserID: st.AgentID, CreatedByUserID: st.AdminID, LLMConnectionID: &llmConn,
	})
	if err != nil {
		t.Fatalf("create no-repo binding: %v", err)
	}
	if _, err := st.BS.StartTestRun(ctx, noRepo.ID, 1, st.AdminID); !errors.Is(err, ErrBindingNoRepo) {
		t.Errorf("no-repo: want ErrBindingNoRepo, got %v", err)
	}
	if _, err := st.BS.StartTestRun(ctx, noRepo.ID, 999, st.AdminID); !errors.Is(err, ErrBindingNotFound) {
		t.Errorf("cross-workspace: want ErrBindingNotFound, got %v", err)
	}
	if _, err := st.BS.StartTestRun(ctx, noRepo.ID+999, 1, st.AdminID); !errors.Is(err, ErrBindingNotFound) {
		t.Errorf("missing: want ErrBindingNotFound, got %v", err)
	}

	// Repo-backed binding, but no RunService on this stack.
	if _, err := st.DB.Exec(`INSERT INTO scm_providers(slug, name, provider_type, auth_method, base_url) VALUES ('test-gitea', 'Test Gitea', 'gitea', 'oauth', 'https://gitea.example.com')`); err != nil {
		t.Fatalf("seed scm provider: %v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO workspace_scm_connections(workspace_id, scm_provider_id) VALUES (1, 1)`); err != nil {
		t.Fatalf("seed scm connection: %v", err)
	}
	var scmConn int
	if err := st.DB.QueryRow(`SELECT id FROM workspace_scm_connections LIMIT 1`).Scan(&scmConn); err != nil {
		t.Fatalf("read connection id: %v", err)
	}
	repoBindingID, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID: 1, ActingUserID: st.SvcUserID, ActingUserKind: ActingIdentityKindAgent,
		RepoSlug: "acme/widget", SCMConnectionID: &scmConn, TokenTTLMinutes: 15, CreatedByUserID: st.AdminID,
	})
	if err != nil {
		t.Fatalf("seed repo binding: %v", err)
	}
	if _, err := st.BS.StartTestRun(ctx, repoBindingID, 1, st.AdminID); !errors.Is(err, ErrBindingRunnerNotConfigured) {
		t.Errorf("no runner: want ErrBindingRunnerNotConfigured, got %v", err)
	}

	// The same binding targeting a remote runner pool uses the durable remote
	// queue, even when this server has no local runner. The persisted ephemeral
	// marker prevents the remote terminal report from invoking the PR hook.
	if _, err := st.DB.Exec(`UPDATE workspace_agent_bindings SET target_pool_id = 7 WHERE id = ?`, repoBindingID); err != nil {
		t.Fatalf("set target pool: %v", err)
	}
	runService, err := NewRunService(repository.NewAgentRunRepository(st.DB), RunServiceOptions{
		Logger: silentLogger(t),
	})
	if err != nil {
		t.Fatalf("remote-only run service: %v", err)
	}
	st.BS.runs = runService
	runID, err := st.BS.StartTestRun(ctx, repoBindingID, 1, st.AdminID)
	if err != nil {
		t.Fatalf("remote pool test run: %v", err)
	}
	queued, err := repository.NewAgentRunRepository(st.DB).Get(ctx, runID)
	if err != nil {
		t.Fatalf("load queued remote test: %v", err)
	}
	if queued.Status != models.AgentRunStatusQueued || queued.TargetPoolID == nil || *queued.TargetPoolID != 7 {
		t.Fatalf("queued remote test = %+v", queued)
	}
	if !queued.IsEphemeral {
		t.Fatal("remote verification run must persist its ephemeral safety marker")
	}
}

// originHasBranch reports whether the origin repo has a local ref for branch.
func originHasBranch(t *testing.T, originPath, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", originPath, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

// TestRunService_EphemeralRunSkipsPushAndHook pins the load-bearing safety
// property of the binding "test run": an Ephemeral run executes the agent
// against a real worktree but must NOT push its branch to the remote or fire
// the post-run PR hook. The control (non-ephemeral) run proves the setup
// otherwise does both, so the suppression is meaningful. It also asserts the
// per-run InitialPrompt overrides the service default.
func TestRunService_EphemeralRunSkipsPushAndHook(t *testing.T) {
	ctx := context.Background()
	origin := seedOriginRepo(t, "main")

	run := func(ephemeral bool, prompt string) (int, bool, string) {
		db := newRunServiceTestDB(t)
		repoDB := repository.NewAgentRunRepository(db)

		var seenPrompt string
		runner := RunnerFunc(func(_ context.Context, in RunInput, _ EventSink) RunnerResult {
			seenPrompt = in.InitialPrompt
			// Commit something: a commit-less run legitimately skips the
			// push (see run_service_no_commit_test.go), and this test is
			// about EPHEMERAL suppression, which must hold even when there
			// is something to push.
			gitIn(t, in.WorkspacePath, "config", "user.email", "agent@windshift.local")
			gitIn(t, in.WorkspacePath, "config", "user.name", "windshift-agent")
			gitIn(t, in.WorkspacePath, "commit", "-q", "--allow-empty", "-m", "agent change")
			return RunnerResult{Status: models.AgentRunStatusSucceeded}
		})
		var hookCalls int
		hook := PostRunHookFunc(func(_ context.Context, _ PostRunInfo) { hookCalls++ })

		svc, err := NewRunService(repoDB, RunServiceOptions{
			Runner:        runner,
			Preparer:      newTestPreparer(t),
			PostRunHook:   hook,
			InitialPrompt: "service-default-prompt",
			Logger:        silentLogger(t),
		})
		if err != nil {
			t.Fatalf("new service: %v", err)
		}
		runID, err := svc.Start(ctx, RunRequest{
			WorkspaceID:   1,
			BindingID:     7,
			Ephemeral:     ephemeral,
			InitialPrompt: prompt,
			Repo: &repoprep.RepoSpec{
				WorkspaceID: 1, RepoSlug: "acme/widget", RemoteURL: origin, BaseRef: "main",
			},
		})
		if err != nil {
			t.Fatalf("start (ephemeral=%v): %v", ephemeral, err)
		}
		svc.Wait()
		return runID, hookCalls > 0, seenPrompt
	}

	// Ephemeral: no push, no hook, and the custom prompt reached the runner.
	runID, hookFired, prompt := run(true, DefaultTestRunPrompt)
	if hookFired {
		t.Error("ephemeral run must not fire the post-run PR hook")
	}
	if originHasBranch(t, origin, "agent-runs/run-"+itoa(runID)) {
		t.Error("ephemeral run must not push its run branch to the remote")
	}
	if prompt != DefaultTestRunPrompt {
		t.Errorf("per-run InitialPrompt should reach the runner: got %q", prompt)
	}

	// Control: a normal run does push + fire the hook, so the suppression above
	// is a real difference, not a broken setup.
	ctrlID, ctrlHook, _ := run(false, "")
	if !ctrlHook {
		t.Error("non-ephemeral run should fire the post-run hook")
	}
	if !originHasBranch(t, origin, "agent-runs/run-"+itoa(ctrlID)) {
		t.Error("non-ephemeral run should push its run branch to the remote")
	}
}

// itoa avoids pulling strconv into the test for a single conversion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
