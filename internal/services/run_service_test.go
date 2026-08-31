package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/auth"
	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repoprep"
	"windshift/internal/repository"
)

// newTestPreparer builds a repoprep.Preparer for tests. AllowFileURL is set
// because seedOriginRepo creates a local repo git treats as file://, which
// production blocks by default.
func newTestPreparer(t *testing.T) *repoprep.Preparer {
	t.Helper()
	p, err := repoprep.New(repoprep.Options{
		RootDir:      t.TempDir(),
		Logger:       silentLogger(t),
		AllowFileURL: true,
	})
	if err != nil {
		t.Fatalf("new repo preparer: %v", err)
	}
	return p
}

func newRunServiceTestDB(t *testing.T) database.Database {
	t.Helper()
	dsn := fmt.Sprintf("file:%s/run_service.db?mode=memory&cache=shared", t.TempDir())
	db, err := database.NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (1, 'ws', 'WS', true)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	return db
}

func silentLogger(t *testing.T) *log.Logger {
	t.Helper()
	return log.New(&strings.Builder{}, "", 0)
}

func TestRunService_CancelForBindingCancelsQueuedRuns(t *testing.T) {
	ctx := context.Background()
	db := newRunServiceTestDB(t)
	repo := repository.NewAgentRunRepository(db)
	svc, err := NewRunService(repo, RunServiceOptions{Logger: silentLogger(t)})
	if err != nil {
		t.Fatalf("new run service: %v", err)
	}

	bindingID := 42
	runID, err := repo.Insert(ctx, &models.AgentRun{
		WorkspaceID: 1,
		BindingID:   &bindingID,
		Status:      models.AgentRunStatusQueued,
	})
	if err != nil {
		t.Fatalf("insert queued run: %v", err)
	}
	if err := svc.CancelForBinding(ctx, bindingID); err != nil {
		t.Fatalf("cancel binding runs: %v", err)
	}
	run, err := repo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if run.Status != models.AgentRunStatusCanceled {
		t.Fatalf("run status = %q, want canceled", run.Status)
	}
	events, err := repo.ListEventsAfter(ctx, runID, 0, 10)
	if err != nil {
		t.Fatalf("list lifecycle events: %v", err)
	}
	if len(events) != 1 || !strings.Contains(events[0].PayloadJSON, "agent profile archived") {
		t.Fatalf("archive cancellation event = %+v", events)
	}
}

// TestRunService_FinalizeRemoteValidatesSCMRefs pins WI-197 (finding 6): a
// remote runner self-prepares its worktree and reports the branch + base
// commit it pushed, and FinalizeRemote feeds them to the PR hook. The branch
// must be this run's canonical push ref (agent-runs/run-<id>) and the base
// commit must be a git object id; anything else is dropped before the hook can
// open a PR from an unverified ref. The hook still fires (the run finalized) —
// it just receives scrubbed values — and rejections leave a warning event.
func TestRunService_FinalizeRemoteValidatesSCMRefs(t *testing.T) {
	ctx := context.Background()
	const sha1 = "0123456789abcdef0123456789abcdef01234567" // 40 hex
	sha256 := strings.Repeat("a", 64)                       // 64 hex

	cases := []struct {
		name           string
		branch         string
		base           string
		wantBranch     string
		wantBase       string
		wantRejections int
	}{
		{"valid branch + sha1 base", "agent-runs/run-%d", sha1, "agent-runs/run-%d", sha1, 0},
		{"valid branch + sha256 base", "agent-runs/run-%d", sha256, "agent-runs/run-%d", sha256, 0},
		{"empty branch + empty base (no PR)", "", "", "", "", 0},
		{"foreign branch dropped with base", "main", sha1, "", "", 1},
		{"other run's branch dropped", "agent-runs/run-999999", sha1, "", "", 1},
		{"valid branch + short base dropped", "agent-runs/run-%d", "deadbeef", "agent-runs/run-%d", "", 1},
		{"valid branch + non-hex base dropped", "agent-runs/run-%d", strings.Repeat("z", 40), "agent-runs/run-%d", "", 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newRunServiceTestDB(t)
			repo := repository.NewAgentRunRepository(db)

			var got PostRunInfo
			var calls int
			hook := PostRunHookFunc(func(_ context.Context, info PostRunInfo) {
				calls++
				got = info
			})
			svc, err := NewRunService(repo, RunServiceOptions{
				Runner:      RunnerFunc(func(context.Context, RunInput, EventSink) RunnerResult { return RunnerResult{} }),
				PostRunHook: hook,
				Logger:      silentLogger(t),
			})
			if err != nil {
				t.Fatalf("new svc: %v", err)
			}

			runID, err := repo.Insert(ctx, &models.AgentRun{
				WorkspaceID: 1,
				JobKind:     models.JobKindCodingAgent,
				Status:      models.AgentRunStatusRunning,
			})
			if err != nil {
				t.Fatalf("insert running run: %v", err)
			}

			subst := func(s string) string {
				if strings.Contains(s, "%d") {
					return fmt.Sprintf(s, runID)
				}
				return s
			}
			branch, base := subst(tc.branch), tc.base
			wantBranch, wantBase := subst(tc.wantBranch), tc.wantBase

			if err := svc.FinalizeRemote(ctx, runID,
				RunnerResult{Status: models.AgentRunStatusSucceeded}, branch, base); err != nil {
				t.Fatalf("finalize remote: %v", err)
			}

			if calls != 1 {
				t.Fatalf("post-run hook should fire exactly once, got %d", calls)
			}
			if got.Branch != wantBranch {
				t.Errorf("Branch passed to hook: want %q, got %q", wantBranch, got.Branch)
			}
			if got.BaseCommit != wantBase {
				t.Errorf("BaseCommit passed to hook: want %q, got %q", wantBase, got.BaseCommit)
			}

			events, err := repo.ListEvents(ctx, runID)
			if err != nil {
				t.Fatalf("list events: %v", err)
			}
			rejections := 0
			for _, ev := range events {
				if strings.Contains(ev.PayloadJSON, "scm_ref_rejected") {
					rejections++
				}
			}
			if rejections != tc.wantRejections {
				t.Errorf("scm_ref_rejected events: want %d, got %d", tc.wantRejections, rejections)
			}
		})
	}
}

func TestRunService_FinalizeRemoteEphemeralSkipsPostRunMutation(t *testing.T) {
	ctx := context.Background()
	db := newRunServiceTestDB(t)
	repo := repository.NewAgentRunRepository(db)
	calls := 0
	svc, err := NewRunService(repo, RunServiceOptions{
		PostRunHook: PostRunHookFunc(func(context.Context, PostRunInfo) {
			calls++
		}),
		Logger: silentLogger(t),
	})
	if err != nil {
		t.Fatalf("new run service: %v", err)
	}
	runID, err := repo.Insert(ctx, &models.AgentRun{
		WorkspaceID: 1,
		JobKind:     models.JobKindCodingAgent,
		Status:      models.AgentRunStatusRunning,
		IsEphemeral: true,
	})
	if err != nil {
		t.Fatalf("insert ephemeral run: %v", err)
	}

	if err := svc.FinalizeRemote(ctx, runID, RunnerResult{
		Status: models.AgentRunStatusSucceeded,
	}, fmt.Sprintf("agent-runs/run-%d", runID), strings.Repeat("a", 40)); err != nil {
		t.Fatalf("finalize ephemeral run: %v", err)
	}
	if calls != 0 {
		t.Fatalf("post-run mutation hook calls = %d, want 0", calls)
	}
	run, err := repo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("reload ephemeral run: %v", err)
	}
	if run.Status != models.AgentRunStatusSucceeded || !run.IsEphemeral {
		t.Fatalf("finalized ephemeral run = %+v", run)
	}
}

// TestRunService_SkeletonHappyPath verifies that Start kicks off a run, the
// stub runner gets invoked, lifecycle + runner-emitted events land in the
// agent_run_events stream, and the run row is finalized as succeeded with
// the container_id stamped.
func TestRunService_SkeletonHappyPath(t *testing.T) {
	ctx := context.Background()
	db := newRunServiceTestDB(t)
	repo := repository.NewAgentRunRepository(db)

	runner := RunnerFunc(func(ctx context.Context, input RunInput, emit EventSink) RunnerResult {
		_ = emit("stdout", `{"line":"hello from stub"}`)
		_ = emit("stdout", `{"line":"second line"}`)
		return RunnerResult{
			ContainerID: "fake-container-xyz",
			Status:      models.AgentRunStatusSucceeded,
		}
	})

	svc, err := NewRunService(repo, RunServiceOptions{
		GlobalCap: 4,
		Runner:    runner,
		Logger:    silentLogger(t),
	})
	if err != nil {
		t.Fatalf("new run service: %v", err)
	}

	runID, err := svc.Start(ctx, RunRequest{WorkspaceID: 1})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	svc.Wait()

	got, err := repo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != models.AgentRunStatusSucceeded {
		t.Fatalf("status: want succeeded, got %q (err=%q)", got.Status, got.Error)
	}
	if got.ContainerID != "fake-container-xyz" {
		t.Fatalf("container_id: want fake-container-xyz, got %q", got.ContainerID)
	}
	if got.StartedAt == nil || got.EndedAt == nil {
		t.Fatalf("started_at and ended_at must be set; got started=%v ended=%v", got.StartedAt, got.EndedAt)
	}

	events, err := repo.ListEvents(ctx, runID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	// Expect: lifecycle/queued, lifecycle/running, stdout, stdout, lifecycle/succeeded
	wantTypes := []string{"lifecycle", "lifecycle", "stdout", "stdout", "lifecycle"}
	if len(events) != len(wantTypes) {
		t.Fatalf("event count: want %d, got %d (%+v)", len(wantTypes), len(events), events)
	}
	for i, ev := range events {
		if ev.Type != wantTypes[i] {
			t.Errorf("event[%d].type: want %q, got %q (payload=%s)", i, wantTypes[i], ev.Type, ev.PayloadJSON)
		}
	}
	last := events[len(events)-1].PayloadJSON
	if !strings.Contains(last, `"succeeded"`) {
		t.Errorf("terminal lifecycle payload: want succeeded marker, got %q", last)
	}
}

// TestRunService_NonTerminalRunnerStatusBecomesFailed pins the invariant
// that a runner returning a non-terminal status (e.g. "running") gets
// normalized to failed with a descriptive error rather than leaving the
// run row in an inconsistent state.
func TestRunService_NonTerminalRunnerStatusBecomesFailed(t *testing.T) {
	ctx := context.Background()
	db := newRunServiceTestDB(t)
	repo := repository.NewAgentRunRepository(db)

	runner := RunnerFunc(func(ctx context.Context, input RunInput, emit EventSink) RunnerResult {
		return RunnerResult{Status: "totally-bogus"}
	})

	svc, err := NewRunService(repo, RunServiceOptions{Runner: runner, Logger: silentLogger(t)})
	if err != nil {
		t.Fatalf("new run service: %v", err)
	}
	runID, err := svc.Start(ctx, RunRequest{WorkspaceID: 1})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	svc.Wait()

	got, err := repo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != models.AgentRunStatusFailed {
		t.Fatalf("status: want failed, got %q", got.Status)
	}
	if !strings.Contains(got.Error, "totally-bogus") {
		t.Errorf("error must mention bogus status, got %q", got.Error)
	}
}

// TestRunService_AdmissionCapsConcurrency ensures the global semaphore
// actually caps in-flight runs. With a cap of 2 and 5 launches, the
// runner should never see more than 2 concurrent executions.
func TestRunService_AdmissionCapsConcurrency(t *testing.T) {
	ctx := context.Background()
	db := newRunServiceTestDB(t)
	repo := repository.NewAgentRunRepository(db)

	const cap = 2
	const total = 5

	var inflight int32
	var peak int32
	gate := make(chan struct{})
	entered := make(chan struct{}, cap)

	runner := RunnerFunc(func(ctx context.Context, input RunInput, emit EventSink) RunnerResult {
		n := atomic.AddInt32(&inflight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		// Best-effort latch: the first `cap` runners signal they're in.
		// Later runners are still gated behind the semaphore.
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
		atomic.AddInt32(&inflight, -1)
		return RunnerResult{Status: models.AgentRunStatusSucceeded}
	})

	svc, err := NewRunService(repo, RunServiceOptions{
		GlobalCap: cap,
		Runner:    runner,
		Logger:    silentLogger(t),
	})
	if err != nil {
		t.Fatalf("new run service: %v", err)
	}

	for i := 0; i < total; i++ {
		if _, err := svc.Start(ctx, RunRequest{WorkspaceID: 1}); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}

	// Wait until `cap` runners are inside the runner body.
	timeout := time.After(2 * time.Second)
	for i := 0; i < cap; i++ {
		select {
		case <-entered:
		case <-timeout:
			t.Fatalf("timed out waiting for %d runners to enter admission (got %d)", cap, i)
		}
	}

	close(gate)
	svc.Wait()

	if peak > cap {
		t.Fatalf("peak concurrency exceeded cap: got %d, want <= %d", peak, cap)
	}
}

// TestRunService_WithRepoPreparesWorktree threads a RepoSpec through Start
// and asserts (a) the runner sees a populated WorkspacePath, (b) a
// "worktree_ready" lifecycle event is emitted with branch + base commit
// data, and (c) the worktree is cleaned up after the run finishes.
func TestRunService_WithRepoPreparesWorktree(t *testing.T) {
	ctx := context.Background()
	db := newRunServiceTestDB(t)
	repoDB := repository.NewAgentRunRepository(db)

	origin := seedOriginRepo(t, "main")
	prep := newTestPreparer(t)

	var observedPath string
	runner := RunnerFunc(func(ctx context.Context, input RunInput, emit EventSink) RunnerResult {
		observedPath = input.WorkspacePath
		return RunnerResult{Status: models.AgentRunStatusSucceeded}
	})

	svc, err := NewRunService(repoDB, RunServiceOptions{
		Runner:   runner,
		Preparer: prep,
		Logger:   silentLogger(t),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	runID, err := svc.Start(ctx, RunRequest{
		WorkspaceID: 1,
		Repo: &repoprep.RepoSpec{
			WorkspaceID: 1,
			RepoSlug:    "acme/widget",
			RemoteURL:   origin,
			BaseRef:     "main",
		},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	svc.Wait()

	if observedPath == "" {
		t.Fatal("runner saw empty WorkspacePath; worktree prep didn't flow through")
	}
	if _, err := os.Stat(observedPath); !os.IsNotExist(err) {
		t.Errorf("worktree dir must be cleaned up after run, stat err=%v", err)
	}

	events, err := repoDB.ListEvents(ctx, runID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	foundReady := false
	for _, ev := range events {
		if ev.Type == "lifecycle" && strings.Contains(ev.PayloadJSON, "worktree_ready") {
			foundReady = true
			if !strings.Contains(ev.PayloadJSON, `"branch":"agent-runs/run-`) {
				t.Errorf("worktree_ready payload missing branch info: %s", ev.PayloadJSON)
			}
		}
	}
	if !foundReady {
		t.Errorf("expected a worktree_ready lifecycle event, got events=%+v", events)
	}
}

// TestRunService_RepoWithoutManagerErrors verifies that asking for a Repo
// without configuring a WorktreeManager fails fast at Start time — better
// to surface the misconfiguration synchronously than write a queued row
// that will never advance.
func TestRunService_RepoWithoutManagerErrors(t *testing.T) {
	ctx := context.Background()
	db := newRunServiceTestDB(t)
	repoDB := repository.NewAgentRunRepository(db)

	runner := RunnerFunc(func(ctx context.Context, input RunInput, emit EventSink) RunnerResult {
		return RunnerResult{Status: models.AgentRunStatusSucceeded}
	})
	svc, err := NewRunService(repoDB, RunServiceOptions{Runner: runner, Logger: silentLogger(t)})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	_, err = svc.Start(ctx, RunRequest{
		WorkspaceID: 1,
		Repo:        &repoprep.RepoSpec{WorkspaceID: 1, RepoSlug: "acme/widget", RemoteURL: "ignored"},
	})
	if err == nil {
		t.Fatal("expected error when Repo is set without WorktreeManager, got nil")
	}
}

// TestRunService_TokenAndEnvFlowThrough exercises WI-86 wiring: a
// RunRequest with a TokenSpec and a caller Env reaches the runner with
// WS_TOKEN populated from RunTokenService.Mint, caller Env preserved,
// and a "token_minted" lifecycle event recorded.
func TestRunService_TokenAndEnvFlowThrough(t *testing.T) {
	ctx := context.Background()
	db, actingUserID := newTokenTestDB(t)
	tm := auth.NewTokenManager(db, nil)

	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (1, 'ws', 'WS', true)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	repoDB := repository.NewAgentRunRepository(db)
	tokens, err := NewRunTokenService(tm)
	if err != nil {
		t.Fatalf("new token svc: %v", err)
	}

	var observed RunInput
	runner := RunnerFunc(func(ctx context.Context, input RunInput, emit EventSink) RunnerResult {
		observed = input
		return RunnerResult{Status: models.AgentRunStatusSucceeded}
	})

	svc, err := NewRunService(repoDB, RunServiceOptions{
		Runner: runner,
		Tokens: tokens,
		Logger: silentLogger(t),
	})
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}

	runID, err := svc.Start(ctx, RunRequest{
		WorkspaceID: 1,
		Token: &TokenSpec{
			ActingUserID: actingUserID,
			TTL:          5 * time.Minute,
			Name:         "agent-run:phase3",
		},
		Env: map[string]string{
			"WS_API_URL":        "https://windshift.test",
			"WS_WORKSPACE_ID":   "1",
			"WINDSHIFT_ITEM_ID": "WI-71",
		},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	svc.Wait()

	if observed.Env["WS_TOKEN"] == "" {
		t.Fatal("runner did not see WS_TOKEN in env")
	}
	if got := observed.Env["WS_API_URL"]; got != "https://windshift.test" {
		t.Errorf("WS_API_URL: want https://windshift.test, got %q", got)
	}
	if got := observed.Env["WINDSHIFT_ITEM_ID"]; got != "WI-71" {
		t.Errorf("WINDSHIFT_ITEM_ID: want WI-71, got %q", got)
	}

	// Confirm the minted token actually round-trips through TokenManager
	// (i.e. it's a real token, not a placeholder).
	user, _, validateErr := tm.ValidateToken(observed.Env["WS_TOKEN"])
	if validateErr != nil {
		t.Fatalf("validate minted token: %v", validateErr)
	}
	if user.ID != actingUserID {
		t.Errorf("token actor: want user %d, got %d", actingUserID, user.ID)
	}

	events, err := repoDB.ListEvents(ctx, runID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	foundMinted := false
	for _, ev := range events {
		if ev.Type == "lifecycle" && strings.Contains(ev.PayloadJSON, "token_minted") {
			foundMinted = true
			if !strings.Contains(ev.PayloadJSON, `"token_id":`) {
				t.Errorf("token_minted payload missing token_id: %s", ev.PayloadJSON)
			}
		}
	}
	if !foundMinted {
		t.Errorf("expected a token_minted lifecycle event, got events=%+v", events)
	}
}

// TestRunService_TokenWithoutManagerErrors mirrors the worktree
// misconfig case: asking for a Token without a RunTokenService fails at
// Start so the caller learns immediately rather than persisting a queued
// row that will never run.
func TestRunService_TokenWithoutManagerErrors(t *testing.T) {
	ctx := context.Background()
	db := newRunServiceTestDB(t)
	repoDB := repository.NewAgentRunRepository(db)

	runner := RunnerFunc(func(ctx context.Context, input RunInput, emit EventSink) RunnerResult {
		return RunnerResult{Status: models.AgentRunStatusSucceeded}
	})
	svc, err := NewRunService(repoDB, RunServiceOptions{Runner: runner, Logger: silentLogger(t)})
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}
	_, err = svc.Start(ctx, RunRequest{
		WorkspaceID: 1,
		Token:       &TokenSpec{ActingUserID: 99},
	})
	if err == nil {
		t.Fatal("expected error when Token is set without RunTokenService, got nil")
	}
}

// TestRunService_PostRunHookReceivesTerminalStatus pins the WI-90 hook
// shape: the callback fires once with the terminal status, the
// caller-provided BindingID, and (when a worktree was prepared) the
// run's branch + base commit so the PR-creation hook can act on them.
func TestRunService_PostRunHookReceivesTerminalStatus(t *testing.T) {
	ctx := context.Background()
	db := newRunServiceTestDB(t)
	repoDB := repository.NewAgentRunRepository(db)

	runner := RunnerFunc(func(ctx context.Context, _ RunInput, _ EventSink) RunnerResult {
		return RunnerResult{Status: models.AgentRunStatusSucceeded}
	})
	var infos []PostRunInfo
	hook := PostRunHookFunc(func(_ context.Context, info PostRunInfo) {
		infos = append(infos, info)
	})

	svc, err := NewRunService(repoDB, RunServiceOptions{
		Runner:      runner,
		PostRunHook: hook,
		Logger:      silentLogger(t),
	})
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}
	runID, err := svc.Start(ctx, RunRequest{WorkspaceID: 1, BindingID: 99})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	svc.Wait()
	if len(infos) != 1 {
		t.Fatalf("post-run hook should fire exactly once, got %d", len(infos))
	}
	got := infos[0]
	if got.RunID != runID {
		t.Errorf("RunID: want %d, got %d", runID, got.RunID)
	}
	if got.BindingID != 99 {
		t.Errorf("BindingID: want 99, got %d", got.BindingID)
	}
	if got.Status != models.AgentRunStatusSucceeded {
		t.Errorf("Status: want succeeded, got %q", got.Status)
	}
	if got.Branch != "" || got.BaseCommit != "" {
		t.Errorf("Branch / BaseCommit should be empty when no worktree was prepared; got %+v", got)
	}
}

// TestRunService_PostRunHookPanicIsContained verifies a misbehaving hook
// cannot wedge the worker goroutine.
func TestRunService_PostRunHookPanicIsContained(t *testing.T) {
	ctx := context.Background()
	db := newRunServiceTestDB(t)
	repoDB := repository.NewAgentRunRepository(db)

	runner := RunnerFunc(func(ctx context.Context, _ RunInput, _ EventSink) RunnerResult {
		return RunnerResult{Status: models.AgentRunStatusSucceeded}
	})
	svc, err := NewRunService(repoDB, RunServiceOptions{
		Runner: runner,
		PostRunHook: PostRunHookFunc(func(context.Context, PostRunInfo) {
			panic("kaboom")
		}),
		Logger: silentLogger(t),
	})
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}
	if _, err := svc.Start(ctx, RunRequest{WorkspaceID: 1}); err != nil {
		t.Fatalf("start: %v", err)
	}
	svc.Wait() // would hang if the panic escaped
}

// TestRunService_CancelInFlightRun verifies Cancel() reaches the
// running worker via the per-run cancel registry and the runner
// observes ctx.Done().
func TestRunService_CancelInFlightRun(t *testing.T) {
	ctx := context.Background()
	db := newRunServiceTestDB(t)
	repoDB := repository.NewAgentRunRepository(db)

	gate := make(chan struct{})
	saw := make(chan struct{})
	runner := RunnerFunc(func(ctx context.Context, _ RunInput, _ EventSink) RunnerResult {
		close(saw)
		select {
		case <-gate:
			return RunnerResult{Status: models.AgentRunStatusSucceeded}
		case <-ctx.Done():
			return RunnerResult{Status: models.AgentRunStatusCanceled, Error: ctx.Err().Error()}
		}
	})

	svc, err := NewRunService(repoDB, RunServiceOptions{Runner: runner, Logger: silentLogger(t)})
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}
	runID, err := svc.Start(ctx, RunRequest{WorkspaceID: 1})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	<-saw // wait until the runner is inside

	if ok := svc.Cancel(runID); !ok {
		t.Fatalf("Cancel(%d) should return true for in-flight run", runID)
	}
	svc.Wait()
	close(gate)

	got, err := repoDB.Get(ctx, runID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != models.AgentRunStatusCanceled {
		t.Errorf("status: want canceled, got %q", got.Status)
	}

	// Cancel on an already-finished run returns false (not in inflight map).
	if ok := svc.Cancel(runID); ok {
		t.Errorf("Cancel on already-finished run should return false")
	}
}

// TestRunService_ShutdownRejectsNewWork confirms Start returns
// ErrShuttingDown after Shutdown has been initiated.
func TestRunService_ShutdownRejectsNewWork(t *testing.T) {
	ctx := context.Background()
	db := newRunServiceTestDB(t)
	repo := repository.NewAgentRunRepository(db)

	runner := RunnerFunc(func(ctx context.Context, input RunInput, emit EventSink) RunnerResult {
		return RunnerResult{Status: models.AgentRunStatusSucceeded}
	})
	svc, err := NewRunService(repo, RunServiceOptions{Runner: runner, Logger: silentLogger(t)})
	if err != nil {
		t.Fatalf("new run service: %v", err)
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := svc.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if _, err := svc.Start(ctx, RunRequest{WorkspaceID: 1}); err == nil {
		t.Fatal("expected Start to fail after Shutdown, got nil")
	}
}

// fakeBindingInputs is a stand-in BindingInputsResolver: it returns canned
// token spec + grants + env so PrepareRemoteClaim can be exercised without a
// real binding row / SCM + LLM connections.
type fakeBindingInputs struct {
	spec         *TokenSpec
	grants       *models.RunGrants
	repo         *JobRepo
	env          map[string]string
	promptSuffix string
}

func (f *fakeBindingInputs) ResolveRunInputs(_ context.Context, _ *models.AgentRun) (*RunInputs, error) {
	return &RunInputs{
		Token:        f.spec,
		Grants:       f.grants,
		Repo:         f.repo,
		Env:          f.env,
		PromptSuffix: f.promptSuffix,
	}, nil
}

// TestRunService_PrepareRemoteClaimEnriches verifies WI-195 findings 1 & 7:
// a remote claim mints the per-run token, persists the grants bound to it
// (git ref pinned to the run-branch namespace), and returns a JobSpec whose
// Env carries WS_TOKEN + AGENT_RUN_ID + the resolver's context env.
func TestRunService_PrepareRemoteClaimEnriches(t *testing.T) {
	ctx := context.Background()
	db, actingUserID := newTokenTestDB(t)
	tm := auth.NewTokenManager(db, nil)
	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (1, 'ws', 'WS', true)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	repo := repository.NewAgentRunRepository(db)
	tokens, err := NewRunTokenService(tm)
	if err != nil {
		t.Fatalf("new token svc: %v", err)
	}
	runner := RunnerFunc(func(_ context.Context, _ RunInput, _ EventSink) RunnerResult {
		return RunnerResult{Status: models.AgentRunStatusSucceeded}
	})
	svc, err := NewRunService(repo, RunServiceOptions{Runner: runner, Tokens: tokens, Logger: silentLogger(t)})
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}
	svc.SetBindingInputsResolver(&fakeBindingInputs{
		spec: &TokenSpec{ActingUserID: actingUserID, TTL: 5 * time.Minute, Name: "agent-run:remote"},
		grants: &models.RunGrants{
			Git: &models.GitGrant{Repo: "owner/repo", ConnectionID: 7},
			LLM: &models.LLMGrant{ConnectionID: 9},
		},
		env: map[string]string{"WS_WORKSPACE_KEY": "WS"},
	})

	// A queued remote run for a pool, binding-backed.
	// ItemID is left nil to avoid the items FK — the fake resolver supplies
	// context env directly.
	pool := 42
	binding := 3
	runID, err := repo.Insert(ctx, &models.AgentRun{
		WorkspaceID:  1,
		BindingID:    &binding,
		TargetPoolID: &pool,
		JobKind:      models.JobKindCodingAgent,
		Status:       models.AgentRunStatusQueued,
	})
	if err != nil {
		t.Fatalf("insert remote run: %v", err)
	}
	run, err := repo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	spec, err := svc.PrepareRemoteClaim(ctx, run)
	if err != nil {
		t.Fatalf("prepare remote claim: %v", err)
	}

	if spec.Env["WS_TOKEN"] == "" {
		t.Fatal("expected WS_TOKEN in enriched JobSpec env")
	}
	if got := spec.Env["AGENT_RUN_ID"]; got != fmt.Sprintf("%d", runID) {
		t.Errorf("AGENT_RUN_ID: want %d, got %q", runID, got)
	}
	if got := spec.Env["WS_WORKSPACE_KEY"]; got != "WS" {
		t.Errorf("context env not carried through: WS_WORKSPACE_KEY=%q", got)
	}
	user, _, vErr := tm.ValidateToken(spec.Env["WS_TOKEN"])
	if vErr != nil || user.ID != actingUserID {
		t.Fatalf("minted token did not validate to acting user: err=%v user=%+v", vErr, user)
	}

	tokenID, _, grants, _, err := repo.GetRunAuthz(ctx, runID)
	if err != nil {
		t.Fatalf("get run authz: %v", err)
	}
	if tokenID == 0 {
		t.Fatal("expected run_token_id bound after remote claim")
	}
	if grants == nil || grants.Git == nil || grants.LLM == nil {
		t.Fatalf("expected git+llm grants persisted, got %+v", grants)
	}
	if want := fmt.Sprintf("agent-runs/run-%d", runID); grants.Git.Ref != want {
		t.Errorf("git ref: want %q, got %q", want, grants.Git.Ref)
	}
	if grants.Git.Repo != "owner/repo" || grants.LLM.ConnectionID != 9 {
		t.Errorf("grants not derived as expected: %+v", grants)
	}
}

func TestRunService_PrepareRemoteEphemeralClaimDeniesPush(t *testing.T) {
	ctx := context.Background()
	db, actingUserID := newTokenTestDB(t)
	tm := auth.NewTokenManager(db, nil)
	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (1, 'ws', 'WS', true)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	repo := repository.NewAgentRunRepository(db)
	tokens, err := NewRunTokenService(tm)
	if err != nil {
		t.Fatalf("new token service: %v", err)
	}
	svc, err := NewRunService(repo, RunServiceOptions{Tokens: tokens, Logger: silentLogger(t)})
	if err != nil {
		t.Fatalf("new run service: %v", err)
	}
	svc.SetBindingInputsResolver(&fakeBindingInputs{
		spec: &TokenSpec{
			ActingUserID: actingUserID,
			Scopes:       []string{auth.ScopeItemsRead, auth.ScopeItemsWrite, auth.ScopePagesWrite},
			TTL:          5 * time.Minute,
			Name:         "private-test",
		},
		grants: &models.RunGrants{
			Git: &models.GitGrant{Repo: "owner/repo", ConnectionID: 7},
			LLM: &models.LLMGrant{ConnectionID: 9},
		},
		promptSuffix: renderInstruction(&models.RunTrigger{
			Kind:        "test",
			Instruction: "Summarize the repository layout.",
		}),
	})
	poolID, bindingID := 42, 3
	runID, err := repo.Insert(ctx, &models.AgentRun{
		WorkspaceID:  1,
		BindingID:    &bindingID,
		TargetPoolID: &poolID,
		JobKind:      models.JobKindCodingAgent,
		Status:       models.AgentRunStatusQueued,
		IsEphemeral:  true,
		Trigger:      &models.RunTrigger{Kind: "test", Instruction: "Summarize the repository layout."},
	})
	if err != nil {
		t.Fatalf("insert ephemeral remote run: %v", err)
	}
	run, err := repo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("load ephemeral remote run: %v", err)
	}
	spec, err := svc.PrepareRemoteClaim(ctx, run)
	if err != nil {
		t.Fatalf("prepare ephemeral claim: %v", err)
	}
	if !strings.Contains(spec.InitialPrompt, DefaultTestRunPrompt) ||
		!strings.Contains(spec.InitialPrompt, "Summarize the repository layout.") {
		t.Fatalf("initial prompt = %q, want bounded prompt plus private request", spec.InitialPrompt)
	}
	tokenID, _, grants, _, err := repo.GetRunAuthz(ctx, runID)
	if err != nil {
		t.Fatalf("load ephemeral grants: %v", err)
	}
	if grants == nil || grants.Git == nil {
		t.Fatalf("ephemeral grants = %+v", grants)
	}
	if grants.Git.Ref != "" || grants.AllowsGitPush("owner/repo", "refs/heads/main") {
		t.Fatalf("ephemeral git grant permits push: %+v", grants.Git)
	}
	var permissionsJSON string
	if err := db.QueryRow(`SELECT permissions FROM api_tokens WHERE id = ?`, tokenID).Scan(&permissionsJSON); err != nil {
		t.Fatalf("load ephemeral token scopes: %v", err)
	}
	var scopes []string
	if err := json.Unmarshal([]byte(permissionsJSON), &scopes); err != nil {
		t.Fatalf("decode ephemeral token scopes: %v", err)
	}
	if !slices.Contains(scopes, auth.ScopeItemsRead) {
		t.Fatalf("ephemeral token missing read scope: %v", scopes)
	}
	for _, forbidden := range []string{auth.ScopeItemsWrite, auth.ScopePagesWrite, auth.ScopeTimeWrite} {
		if slices.Contains(scopes, forbidden) {
			t.Fatalf("ephemeral token retained write scope %q: %v", forbidden, scopes)
		}
	}
}

// TestRunService_PrepareRemoteClaimNoBinding verifies a run with no binding
// (e.g. an action_container job) is returned without token/grant enrichment.
func TestRunService_PrepareRemoteClaimNoBinding(t *testing.T) {
	ctx := context.Background()
	db, _ := newTokenTestDB(t)
	tm := auth.NewTokenManager(db, nil)
	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (1, 'ws', 'WS', true)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	repo := repository.NewAgentRunRepository(db)
	tokens, _ := NewRunTokenService(tm)
	runner := RunnerFunc(func(_ context.Context, _ RunInput, _ EventSink) RunnerResult {
		return RunnerResult{Status: models.AgentRunStatusSucceeded}
	})
	svc, err := NewRunService(repo, RunServiceOptions{Runner: runner, Tokens: tokens, Logger: silentLogger(t)})
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}
	svc.SetBindingInputsResolver(&fakeBindingInputs{}) // would panic-free even if called

	pool := 1
	runID, err := repo.Insert(ctx, &models.AgentRun{
		WorkspaceID:  1,
		TargetPoolID: &pool,
		JobKind:      models.JobKindActionContainer,
		JobImage:     "ghcr.io/acme/ci:latest",
		Status:       models.AgentRunStatusQueued,
	})
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
	run, err := repo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	spec, err := svc.PrepareRemoteClaim(ctx, run)
	if err != nil {
		t.Fatalf("prepare remote claim: %v", err)
	}
	if spec.Env["WS_TOKEN"] != "" {
		t.Error("action_container run should not get a WS_TOKEN")
	}
	if spec.Image != "ghcr.io/acme/ci:latest" || spec.Kind != models.JobKindActionContainer {
		t.Errorf("job kind/image not passed through: %+v", spec)
	}
	if tokenID, _, _, _, _ := repo.GetRunAuthz(ctx, runID); tokenID != 0 {
		t.Error("no token should be bound for a binding-less run")
	}
}
