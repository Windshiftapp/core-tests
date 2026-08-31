package repoprep_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"windshift/internal/repoprep"
)

// seedBranchOnOrigin creates branch off main in the (non-bare) origin with one
// extra commit adding filename, then switches origin back to main so the branch
// is not the checked-out ref (otherwise a push to it is refused). Returns the
// branch head SHA — this is the "PR head" a continuation run cuts from.
func seedBranchOnOrigin(t *testing.T, origin, branch, filename string) string {
	t.Helper()
	run(t, origin, "git", "checkout", "-q", "-b", branch)
	if err := os.WriteFile(filepath.Join(origin, filename), []byte("pr work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, origin, "git", "add", "-A")
	run(t, origin, "git", "commit", "-q", "-m", "pr commit")
	head := strings.TrimSpace(out(t, origin, "git", "rev-parse", branch))
	run(t, origin, "git", "checkout", "-q", "main")
	return head
}

// TestPrepare_ContinuationChecksOutExistingBranch: with ContinueBranch set,
// Prepare cuts the checkout on that existing PR head (not main), keeps its name,
// and reports the PR head as the base commit.
func TestPrepare_ContinuationChecksOutExistingBranch(t *testing.T) {
	origin, mainTip := seedOrigin(t)
	prHead := seedBranchOnOrigin(t, origin, "agent-runs/run-7", "feature.txt")
	if prHead == mainTip {
		t.Fatal("seed bug: PR head equals main tip")
	}
	p := newPreparer(t)

	pr, err := p.Prepare(context.Background(), repoprep.RepoSpec{
		WorkspaceID:    7,
		RepoSlug:       "acme/widget",
		RemoteURL:      origin,
		BaseRef:        "main",
		ContinueBranch: "agent-runs/run-7",
	}, 99)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Branch name is the PR head's, NOT agent-runs/run-99.
	if pr.Branch != "agent-runs/run-7" {
		t.Errorf("branch=%q want agent-runs/run-7 (the continued PR head)", pr.Branch)
	}
	if got := strings.TrimSpace(out(t, pr.Path, "git", "rev-parse", "--abbrev-ref", "HEAD")); got != "agent-runs/run-7" {
		t.Errorf("checked-out branch=%q want agent-runs/run-7", got)
	}
	// Base commit is the PR head, so commits land on top of the PR's work.
	if pr.BaseCommit != prHead {
		t.Errorf("base commit=%q want PR head %q (not main %q)", pr.BaseCommit, prHead, mainTip)
	}
	// The PR's file is materialized — proof we're on the branch, not main.
	if _, err := os.Stat(filepath.Join(pr.Path, "feature.txt")); err != nil {
		t.Errorf("PR file not materialized in continuation checkout: %v", err)
	}
}

// TestContinuation_RoundTripsToSameBranch: a continuation run's commits push
// back onto the same PR head branch (fast-forward), growing the existing PR.
func TestContinuation_RoundTripsToSameBranch(t *testing.T) {
	origin, _ := seedOrigin(t)
	prHead := seedBranchOnOrigin(t, origin, "agent-runs/run-7", "feature.txt")
	p := newPreparer(t)
	ctx := context.Background()

	pr, err := p.Prepare(ctx, repoprep.RepoSpec{
		WorkspaceID: 7, RepoSlug: "acme/widget", RemoteURL: origin,
		BaseRef: "main", ContinueBranch: "agent-runs/run-7",
	}, 99)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	run(t, pr.Path, "git", "config", "user.email", "agent@windshift.local")
	run(t, pr.Path, "git", "config", "user.name", "windshift-agent")
	if err := os.WriteFile(filepath.Join(pr.Path, "MORE.txt"), []byte("more agent work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, pr.Path, "git", "add", "-A")
	run(t, pr.Path, "git", "commit", "-q", "-m", "continuation change")
	want := strings.TrimSpace(out(t, pr.Path, "git", "rev-parse", "HEAD"))

	if err := p.Push(ctx, pr, ""); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if got := strings.TrimSpace(out(t, origin, "git", "rev-parse", "agent-runs/run-7")); got != want {
		t.Errorf("origin PR branch=%q want %q (continuation commits not delivered)", got, want)
	}
	// The new head is a child of the original PR head (a true fast-forward).
	parent := strings.TrimSpace(out(t, origin, "git", "rev-parse", "agent-runs/run-7^"))
	if parent != prHead {
		t.Errorf("pushed head's parent=%q want original PR head %q", parent, prHead)
	}
}

// TestContinuation_NonFastForwardRejected: if the PR head branch advanced on the
// remote after the run cut from it, the run's push is rejected rather than
// force-pushing over the other commit — the safety the plan requires.
func TestContinuation_NonFastForwardRejected(t *testing.T) {
	origin, _ := seedOrigin(t)
	prHead := seedBranchOnOrigin(t, origin, "agent-runs/run-7", "feature.txt")
	p := newPreparer(t)
	ctx := context.Background()

	pr, err := p.Prepare(ctx, repoprep.RepoSpec{
		WorkspaceID: 7, RepoSlug: "acme/widget", RemoteURL: origin,
		BaseRef: "main", ContinueBranch: "agent-runs/run-7",
	}, 99)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Someone else pushes to the PR branch while the run is working.
	run(t, origin, "git", "checkout", "-q", "agent-runs/run-7")
	if err := os.WriteFile(filepath.Join(origin, "their.txt"), []byte("concurrent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, origin, "git", "add", "-A")
	run(t, origin, "git", "commit", "-q", "-m", "concurrent commit")
	advanced := strings.TrimSpace(out(t, origin, "git", "rev-parse", "agent-runs/run-7"))
	run(t, origin, "git", "checkout", "-q", "main")

	// The run commits on top of the OLD head and tries to push.
	run(t, pr.Path, "git", "config", "user.email", "agent@windshift.local")
	run(t, pr.Path, "git", "config", "user.name", "windshift-agent")
	if err := os.WriteFile(filepath.Join(pr.Path, "MORE.txt"), []byte("our work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, pr.Path, "git", "add", "-A")
	run(t, pr.Path, "git", "commit", "-q", "-m", "our change")

	err = p.Push(ctx, pr, "")
	if err == nil {
		t.Fatal("Push must fail on a non-fast-forward, but it succeeded (force-push?)")
	}
	if errors.Is(err, repoprep.ErrNoNewCommits) {
		t.Fatalf("want a non-fast-forward push error, got ErrNoNewCommits: %v", err)
	}
	// The remote branch is untouched — still the other person's commit.
	if got := strings.TrimSpace(out(t, origin, "git", "rev-parse", "agent-runs/run-7")); got != advanced {
		t.Errorf("origin PR branch=%q want %q (must not be overwritten)", got, advanced)
	}
	_ = prHead
}

// TestPrepare_RejectsBadContinuationBranch: a continuation branch that could be
// read as a git flag or smuggle refspec metacharacters is rejected up front.
func TestPrepare_RejectsBadContinuationBranch(t *testing.T) {
	origin, _ := seedOrigin(t)
	p := newPreparer(t)
	for _, bad := range []string{"-evil", "a b", "feat: off", "x..y"} {
		_, err := p.Prepare(context.Background(), repoprep.RepoSpec{
			WorkspaceID: 7, RepoSlug: "acme/widget", RemoteURL: origin,
			BaseRef: "main", ContinueBranch: bad,
		}, 99)
		if err == nil {
			t.Errorf("ContinueBranch %q: want rejection, got nil", bad)
		}
	}
}
