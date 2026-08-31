package repoprep_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"windshift/internal/repoprep"
)

// seedOrigin creates a non-bare git repo with one commit on main and returns
// its path (used as a file:// RemoteURL) plus the tip SHA.
func seedOrigin(t *testing.T) (string, string) {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin")
	run(t, "", "git", "init", "-b", "main", origin)
	run(t, origin, "git", "config", "user.email", "seed@windshift.local")
	run(t, origin, "git", "config", "user.name", "seed")
	if err := os.WriteFile(filepath.Join(origin, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, origin, "git", "add", "-A")
	run(t, origin, "git", "commit", "-q", "-m", "initial")
	return origin, strings.TrimSpace(out(t, origin, "git", "rev-parse", "main"))
}

func newPreparer(t *testing.T) *repoprep.Preparer {
	t.Helper()
	p, err := repoprep.New(repoprep.Options{RootDir: t.TempDir(), AllowFileURL: true})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPrepare_PrivateObjectStore(t *testing.T) {
	origin, tip := seedOrigin(t)
	p := newPreparer(t)

	pr, err := p.Prepare(context.Background(), repoprep.RepoSpec{
		WorkspaceID: 7,
		RepoSlug:    "acme/widget",
		RemoteURL:   origin,
		BaseRef:     "main",
	}, 1)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// The checkout is a real, populated repo.
	if _, err := os.Stat(filepath.Join(pr.Path, ".git")); err != nil {
		t.Fatalf("checkout has no .git: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(pr.Path, "README.md")); err != nil || string(b) != "hello\n" {
		t.Fatalf("README not materialized: %q err=%v", string(b), err)
	}

	// The whole point: a PRIVATE object store. A local clone must not borrow
	// the cache's objects via alternates.
	if _, err := os.Stat(filepath.Join(pr.Path, ".git", "objects", "info", "alternates")); !os.IsNotExist(err) {
		t.Errorf("checkout has git alternates -> shared object store (want private); err=%v", err)
	}

	// Branch + base commit are what we asked for.
	if pr.Branch != "agent-runs/run-1" {
		t.Errorf("branch=%q want agent-runs/run-1", pr.Branch)
	}
	if pr.BaseCommit != tip {
		t.Errorf("base commit=%q want %q", pr.BaseCommit, tip)
	}
	if got := strings.TrimSpace(out(t, pr.Path, "git", "rev-parse", "--abbrev-ref", "HEAD")); got != "agent-runs/run-1" {
		t.Errorf("checked-out branch=%q want agent-runs/run-1", got)
	}
	// origin points at the real (tokenless) remote, not the host-local cache.
	if got := strings.TrimSpace(out(t, pr.Path, "git", "remote", "get-url", "origin")); got != origin {
		t.Errorf("origin=%q want %q", got, origin)
	}
}

func TestPrepareCommitPush_RoundTrips(t *testing.T) {
	origin, _ := seedOrigin(t)
	p := newPreparer(t)
	ctx := context.Background()

	pr, err := p.Prepare(ctx, repoprep.RepoSpec{
		WorkspaceID: 7, RepoSlug: "acme/widget", RemoteURL: origin, BaseRef: "main",
	}, 42)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Agent edits + commits locally on the run branch.
	run(t, pr.Path, "git", "config", "user.email", "agent@windshift.local")
	run(t, pr.Path, "git", "config", "user.name", "windshift-agent")
	if err := os.WriteFile(filepath.Join(pr.Path, "NEW.txt"), []byte("from agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, pr.Path, "git", "add", "-A")
	run(t, pr.Path, "git", "commit", "-q", "-m", "agent change")
	want := strings.TrimSpace(out(t, pr.Path, "git", "rev-parse", "HEAD"))

	// Runner pushes the single run branch back (no token for the local origin).
	if err := p.Push(ctx, pr, ""); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if got := strings.TrimSpace(out(t, origin, "git", "rev-parse", "agent-runs/run-42")); got != want {
		t.Errorf("origin run branch=%q want %q", got, want)
	}
}

// TestPush_SkipsCommitlessRun: a run branch whose head still equals the base
// commit (the agent committed nothing) is not pushed at all — the remote
// never grows an empty branch.
func TestPush_SkipsCommitlessRun(t *testing.T) {
	origin, _ := seedOrigin(t)
	p := newPreparer(t)
	ctx := context.Background()

	pr, err := p.Prepare(ctx, repoprep.RepoSpec{
		WorkspaceID: 7, RepoSlug: "acme/widget", RemoteURL: origin, BaseRef: "main",
	}, 43)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if err := p.Push(ctx, pr, ""); !errors.Is(err, repoprep.ErrNoNewCommits) {
		t.Fatalf("want ErrNoNewCommits for a commit-less checkout, got %v", err)
	}
	if _, err := exec.Command("git", "-C", origin, "rev-parse", "--verify", "refs/heads/agent-runs/run-43").Output(); err == nil {
		t.Error("origin must not have the run branch after a skipped push")
	}
}

func TestEvictIdle_RemovesUnreferencedCache(t *testing.T) {
	origin, _ := seedOrigin(t)
	p := newPreparer(t)
	ctx := context.Background()

	pr, err := p.Prepare(ctx, repoprep.RepoSpec{
		WorkspaceID: 7, RepoSlug: "acme/widget", RemoteURL: origin, BaseRef: "main",
	}, 1)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// While a checkout is live, nothing is evicted.
	if n, err := p.EvictIdle(0, time.Now()); err != nil || n != 0 {
		t.Fatalf("EvictIdle with live checkout: n=%d err=%v (want 0)", n, err)
	}

	// After cleanup, the idle cache is evicted.
	if err := p.Cleanup(ctx, pr); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(pr.Path); !os.IsNotExist(err) {
		t.Errorf("checkout still present after Cleanup: err=%v", err)
	}
	if n, err := p.EvictIdle(0, time.Now()); err != nil || n != 1 {
		t.Fatalf("EvictIdle after cleanup: n=%d err=%v (want 1)", n, err)
	}
}

func TestPrepare_RejectsBadSlug(t *testing.T) {
	p := newPreparer(t)
	_, err := p.Prepare(context.Background(), repoprep.RepoSpec{
		WorkspaceID: 7, RepoSlug: "../escape", RemoteURL: "https://example.com/x",
	}, 1)
	if err == nil || !strings.Contains(err.Error(), "slug") {
		t.Fatalf("want slug rejection, got %v", err)
	}
}

// prepareWithAgentCommit prepares a per-run checkout and makes one local commit
// on the run branch, standing in for the (untrusted) agent's edits. Returns the
// prepared checkout.
func prepareWithAgentCommit(t *testing.T, p *repoprep.Preparer, origin string, runID int) *repoprep.Prepared {
	t.Helper()
	pr, err := p.Prepare(context.Background(), repoprep.RepoSpec{
		WorkspaceID: 7, RepoSlug: "acme/widget", RemoteURL: origin, BaseRef: "main",
	}, runID)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	run(t, pr.Path, "git", "config", "user.email", "agent@windshift.local")
	run(t, pr.Path, "git", "config", "user.name", "windshift-agent")
	if err := os.WriteFile(filepath.Join(pr.Path, "NEW.txt"), []byte("from agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, pr.Path, "git", "add", "-A")
	run(t, pr.Path, "git", "commit", "-q", "-m", "agent change")
	return pr
}

// TestPushBranch_DisablesAgentPrePushHook proves the host-side push does not
// execute a pre-push hook the agent dropped into the checkout's .git/hooks
// (security Phase 1, WI-238). The hook would touch a sentinel and exit 1; with
// hooks disabled the push succeeds and the sentinel never appears.
func TestPushBranch_DisablesAgentPrePushHook(t *testing.T) {
	origin, _ := seedOrigin(t)
	p := newPreparer(t)
	pr := prepareWithAgentCommit(t, p, origin, 42)

	sentinel := filepath.Join(t.TempDir(), "hook-ran")
	hook := "#!/bin/sh\ntouch " + sentinel + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(pr.Path, ".git", "hooks", "pre-push"), []byte(hook), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := p.Push(context.Background(), pr, ""); err != nil {
		t.Fatalf("Push: %v (a running pre-push hook would fail the push)", err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("pre-push hook executed (sentinel present); host push must ignore agent hooks")
	}
}

// TestPushBranch_OverridesAgentHooksPathConfig proves an agent-written
// core.hooksPath in the checkout's .git/config cannot redirect git to an
// attacker-controlled hooks dir during the host-side push: the orchestrator's
// `-c core.hooksPath=/dev/null` wins (security Phase 1, WI-238).
func TestPushBranch_OverridesAgentHooksPathConfig(t *testing.T) {
	origin, _ := seedOrigin(t)
	p := newPreparer(t)
	pr := prepareWithAgentCommit(t, p, origin, 43)

	evilDir := filepath.Join(t.TempDir(), "evil-hooks")
	if err := os.MkdirAll(evilDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "evil-hook-ran")
	hook := "#!/bin/sh\ntouch " + sentinel + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(evilDir, "pre-push"), []byte(hook), 0o700); err != nil {
		t.Fatal(err)
	}
	run(t, pr.Path, "git", "config", "core.hooksPath", evilDir)

	if err := p.Push(context.Background(), pr, ""); err != nil {
		t.Fatalf("Push: %v (an executed evil hook would fail the push)", err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("agent core.hooksPath was honored (sentinel present); -c override must win")
	}
}

// TestPushBranch_RewrittenOriginDoesNotRedirect proves that when the agent
// rewrites remote.origin.url in the checkout config, an explicit
// server-derived RemoteURL still governs where the branch lands (security
// Phase 1, WI-238).
func TestPushBranch_RewrittenOriginDoesNotRedirect(t *testing.T) {
	origin, _ := seedOrigin(t)
	p := newPreparer(t)
	pr := prepareWithAgentCommit(t, p, origin, 44)
	want := strings.TrimSpace(out(t, pr.Path, "git", "rev-parse", "HEAD"))

	// Agent points origin at a bogus path.
	run(t, pr.Path, "git", "remote", "set-url", "origin", filepath.Join(t.TempDir(), "evil.git"))

	// Host pushes with the server-derived RemoteURL (the git-proxy transport).
	if _, err := repoprep.PushBranch(context.Background(), repoprep.PushOptions{
		Dest:         pr.Path,
		Branch:       pr.Branch,
		RemoteURL:    origin,
		AllowFileURL: true,
	}); err != nil {
		t.Fatalf("PushBranch: %v", err)
	}
	if got := strings.TrimSpace(out(t, origin, "git", "rev-parse", "agent-runs/run-44")); got != want {
		t.Fatalf("branch landed on wrong remote: origin has %q want %q", got, want)
	}
}

// TestPrepareAndPush_NoSystemTempDir proves checkout prep and push survive a
// missing system temp dir (a scratch container without a /tmp tmpfs): the
// askpass helper and the sanitized push repo live under the preparer's root
// (preferred over the system temp dir, which in scratch deploys is absent or
// noexec) instead of failing with "stat /tmp: no such file or directory".
func TestPrepareAndPush_NoSystemTempDir(t *testing.T) {
	origin, _ := seedOrigin(t)
	rootDir := t.TempDir()
	p, err := repoprep.New(repoprep.Options{RootDir: rootDir, AllowFileURL: true})
	if err != nil {
		t.Fatal(err)
	}
	realTmp := t.TempDir()
	missing := filepath.Join(realTmp, "missing")
	// Break the temp dir only around the orchestrator calls — the simulated
	// agent commit below runs in the runner container in prod, which always
	// mounts a /tmp tmpfs.
	t.Setenv("TMPDIR", missing)

	// A non-empty token forces the askpass path even though the file:// origin
	// never prompts for credentials.
	pr, err := p.Prepare(context.Background(), repoprep.RepoSpec{
		WorkspaceID: 7, RepoSlug: "acme/widget", RemoteURL: origin, BaseRef: "main",
		Token: "dummy-token",
	}, 45)
	if err != nil {
		t.Fatalf("Prepare without system temp dir: %v", err)
	}

	os.Setenv("TMPDIR", realTmp)
	run(t, pr.Path, "git", "config", "user.email", "agent@windshift.local")
	run(t, pr.Path, "git", "config", "user.name", "windshift-agent")
	if err := os.WriteFile(filepath.Join(pr.Path, "NEW.txt"), []byte("from agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, pr.Path, "git", "add", "-A")
	run(t, pr.Path, "git", "commit", "-q", "-m", "agent change")
	os.Setenv("TMPDIR", missing)

	if err := p.Push(context.Background(), pr, "dummy-token"); err != nil {
		t.Fatalf("Push without system temp dir: %v", err)
	}

	// The fallback dirs are per-invocation and removed by their creators.
	if entries, err := os.ReadDir(filepath.Join(rootDir, ".tmp")); err != nil {
		t.Fatalf("fallback root not created under RootDir: %v", err)
	} else if len(entries) != 0 {
		t.Errorf("fallback temp dirs leaked: %d entries left in .tmp", len(entries))
	}
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	c := exec.Command(name, args...)
	if dir != "" {
		c.Dir = dir
	}
	if b, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, b)
	}
}

func out(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	c := exec.Command(name, args...)
	if dir != "" {
		c.Dir = dir
	}
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, b)
	}
	return string(b)
}
