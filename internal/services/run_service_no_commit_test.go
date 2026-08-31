//go:build test

package services

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repoprep"
	"windshift/internal/repository"
)

// Local-path twin of the triage runner's commit-less contract: a succeeded
// run in which the agent committed nothing pushes no branch, opens no PR
// (the hook sees an empty Branch), and records a no_changes lifecycle event
// — while a run that does commit still pushes and reports its branch.

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func runLocalRepoRun(t *testing.T, commit bool) (hookInfo PostRunInfo, origin string, runID int, repoDB *repository.AgentRunRepository) {
	t.Helper()
	ctx := context.Background()
	db := newRunServiceTestDB(t)
	repoDB = repository.NewAgentRunRepository(db)
	origin = seedOriginRepo(t, "main")
	prep := newTestPreparer(t)

	runner := RunnerFunc(func(_ context.Context, input RunInput, _ EventSink) RunnerResult {
		if commit {
			gitIn(t, input.WorkspacePath, "config", "user.email", "agent@windshift.local")
			gitIn(t, input.WorkspacePath, "config", "user.name", "windshift-agent")
			if out, err := exec.Command("sh", "-c", "echo change > "+input.WorkspacePath+"/CHANGE.txt").CombinedOutput(); err != nil {
				t.Errorf("write change: %v %s", err, out)
			}
			gitIn(t, input.WorkspacePath, "add", "-A")
			gitIn(t, input.WorkspacePath, "commit", "-q", "-m", "agent change")
		}
		return RunnerResult{Status: models.AgentRunStatusSucceeded}
	})

	hookCh := make(chan PostRunInfo, 1)
	svc, err := NewRunService(repoDB, RunServiceOptions{
		Runner:   runner,
		Preparer: prep,
		Logger:   silentLogger(t),
		PostRunHook: PostRunHookFunc(func(_ context.Context, info PostRunInfo) {
			hookCh <- info
		}),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	runID, err = svc.Start(ctx, RunRequest{
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
	select {
	case hookInfo = <-hookCh:
	default:
		t.Fatal("post-run hook was not invoked")
	}
	return hookInfo, origin, runID, repoDB
}

func TestRunService_CommitlessRunSkipsPushAndPR(t *testing.T) {
	info, origin, runID, repoDB := runLocalRepoRun(t, false /* commit */)

	if info.Status != models.AgentRunStatusSucceeded {
		t.Errorf("commit-less run must stay succeeded, got %q", info.Status)
	}
	if info.Branch != "" || info.BaseCommit != "" {
		t.Errorf("hook must see no branch for a commit-less run, got %q %q", info.Branch, info.BaseCommit)
	}
	if _, err := exec.Command("git", "-C", origin, "rev-parse", "--verify", "refs/heads/agent-runs/run-1").Output(); err == nil {
		t.Error("origin must not have the run branch after a commit-less run")
	}

	events, err := repoDB.ListEvents(context.Background(), runID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.Type == "lifecycle" && strings.Contains(ev.PayloadJSON, "no_changes") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a no_changes lifecycle event, got %+v", events)
	}
}

func TestRunService_CommittingRunPushesAndReportsBranch(t *testing.T) {
	info, origin, runID, _ := runLocalRepoRun(t, true /* commit */)

	wantBranch := "agent-runs/run-1"
	if info.Branch != wantBranch {
		t.Errorf("hook branch: want %q, got %q (run %d)", wantBranch, info.Branch, runID)
	}
	if info.BaseCommit == "" {
		t.Error("hook must carry the base commit for a pushed run")
	}
	head := gitIn(t, origin, "rev-parse", "refs/heads/"+wantBranch)
	if head == info.BaseCommit {
		t.Error("pushed head must differ from base (the agent committed)")
	}
}
