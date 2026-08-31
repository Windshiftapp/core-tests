package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// seedOriginRepo creates a non-bare git repo on disk with one commit on the
// given branch and returns its path; the path is what the repo-flow tests hand
// to repoprep.RepoSpec.RemoteURL so the preparer can clone --bare from it.
func seedOriginRepo(t *testing.T, branch string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	// Use a sub-path so temp-dir cleanup doesn't trip over the bare clone
	// created elsewhere.
	repo := filepath.Join(dir, "origin")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir origin: %v", err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v (out=%s)", strings.Join(args, " "), err, out)
		}
	}
	run("init", "--initial-branch="+branch)
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial")
	return repo
}
