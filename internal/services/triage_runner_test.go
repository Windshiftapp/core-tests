package services

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"windshift/internal/models"
)

func buildFakeTriage(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "faketriage")
	cmd := exec.Command("go", "build", "-o", out, "./testdata/faketriage/")
	cmd.Dir = "."
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build faketriage: %v\n%s", err, combined)
	}
	return out
}

// recordingRunner captures what the inner Runner saw so the test can assert
// TriageRunner prepared a checkout before running it.
type recordingRunner struct {
	status     string
	sawPath    string
	sawPrepped bool
	called     bool
}

func (r *recordingRunner) Run(_ context.Context, input RunInput, _ EventSink) RunnerResult {
	r.called = true
	r.sawPath = input.WorkspacePath
	if input.WorkspacePath != "" {
		if _, err := os.Stat(filepath.Join(input.WorkspacePath, "PREPARED")); err == nil {
			r.sawPrepped = true
		}
	}
	return RunnerResult{Status: r.status}
}

func noopEmit(string, string) error { return nil }

func repoInput(runID int) RunInput {
	return RunInput{
		RunID: runID,
		Env:   map[string]string{"WS_TOKEN": "run-tok"},
		Repo:  &JobRepo{WorkspaceID: 3, Slug: "acme/widget", BaseRef: "main"},
	}
}

func TestTriageRunner_PreparesRunsAndPushesOnSuccess(t *testing.T) {
	inner := &recordingRunner{status: models.AgentRunStatusSucceeded}
	tr := &TriageRunner{
		Inner:     inner,
		TriageBin: buildFakeTriage(t),
		CacheRoot: t.TempDir(),
		APIBase:   "http://orch.local/api/v1",
	}
	res := tr.Run(context.Background(), repoInput(7), noopEmit)

	if res.Status != models.AgentRunStatusSucceeded {
		t.Fatalf("status=%q err=%q", res.Status, res.Error)
	}
	if !inner.called || !inner.sawPrepped {
		t.Fatalf("inner runner did not see a prepared checkout (called=%v prepped=%v path=%q)", inner.called, inner.sawPrepped, inner.sawPath)
	}
	// Push ran: faketriage drops a sibling marker that survives the checkout's
	// post-run cleanup.
	if _, err := os.Stat(inner.sawPath + ".pushed"); err != nil {
		t.Errorf("expected push marker for %s: %v", inner.sawPath, err)
	}
	// Checkout was reclaimed.
	if _, err := os.Stat(inner.sawPath); !os.IsNotExist(err) {
		t.Errorf("checkout not cleaned up: %v", err)
	}
	// The pushed branch + base ride on the result — that is what the
	// orchestrator's FinalizeRemote hands to the PR hook.
	if res.Branch != "agent-runs/run-7" || res.BaseCommit != "base123" {
		t.Errorf("result branch/base: want agent-runs/run-7 base123, got %q %q", res.Branch, res.BaseCommit)
	}
}

// TestTriageRunner_NoCommitsSkipsPush pins the commit-less-success contract:
// the push is skipped (no branch lands on the remote) and the result carries
// no Branch, so the orchestrator opens no PR.
func TestTriageRunner_NoCommitsSkipsPush(t *testing.T) {
	t.Setenv("FAKETRIAGE_NO_COMMITS", "1")
	inner := &recordingRunner{status: models.AgentRunStatusSucceeded}
	tr := &TriageRunner{
		Inner:     inner,
		TriageBin: buildFakeTriage(t),
		CacheRoot: t.TempDir(),
		APIBase:   "http://orch.local/api/v1",
	}
	res := tr.Run(context.Background(), repoInput(11), noopEmit)

	if res.Status != models.AgentRunStatusSucceeded {
		t.Fatalf("commit-less run must stay succeeded; status=%q err=%q", res.Status, res.Error)
	}
	if _, err := os.Stat(inner.sawPath + ".pushed"); !os.IsNotExist(err) {
		t.Errorf("push must be skipped without commits; marker err=%v", err)
	}
	if res.Branch != "" || res.BaseCommit != "" {
		t.Errorf("commit-less result must carry no branch, got %q %q", res.Branch, res.BaseCommit)
	}
}

func TestTriageRunner_NoPushOnFailure(t *testing.T) {
	inner := &recordingRunner{status: models.AgentRunStatusFailed}
	tr := &TriageRunner{
		Inner:     inner,
		TriageBin: buildFakeTriage(t),
		CacheRoot: t.TempDir(),
		APIBase:   "http://orch.local/api/v1",
	}
	res := tr.Run(context.Background(), repoInput(8), noopEmit)

	if res.Status != models.AgentRunStatusFailed {
		t.Fatalf("status=%q want failed", res.Status)
	}
	if !inner.sawPrepped {
		t.Fatalf("inner should still have run against a checkout")
	}
	if _, err := os.Stat(inner.sawPath + ".pushed"); !os.IsNotExist(err) {
		t.Errorf("push must not run on failure; marker err=%v", err)
	}
}

func TestTriageRunner_PassThroughWhenNoRepo(t *testing.T) {
	inner := &recordingRunner{status: models.AgentRunStatusSucceeded}
	// No TriageBin/CacheRoot/APIBase: a no-repo run must not touch triage.
	tr := &TriageRunner{Inner: inner}
	res := tr.Run(context.Background(), RunInput{RunID: 9, WorkspacePath: "/local/prepared"}, noopEmit)

	if res.Status != models.AgentRunStatusSucceeded || !inner.called {
		t.Fatalf("expected pass-through to inner; status=%q called=%v", res.Status, inner.called)
	}
	if inner.sawPath != "/local/prepared" {
		t.Errorf("pass-through must preserve WorkspacePath, got %q", inner.sawPath)
	}
}

func TestTriageRunner_FailsWithoutToken(t *testing.T) {
	inner := &recordingRunner{status: models.AgentRunStatusSucceeded}
	tr := &TriageRunner{Inner: inner, TriageBin: "/nonexistent", CacheRoot: t.TempDir(), APIBase: "http://o/api"}
	in := repoInput(10)
	in.Env = map[string]string{} // no WS_TOKEN
	res := tr.Run(context.Background(), in, noopEmit)
	if res.Status != models.AgentRunStatusFailed {
		t.Fatalf("want failed without token, got %q", res.Status)
	}
	if inner.called {
		t.Errorf("inner must not run when prep can't authenticate")
	}
}
