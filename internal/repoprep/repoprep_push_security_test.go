package repoprep

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPushBranchUsesTrustedRemoteAndIgnoresCheckoutConfig(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	badRemote := filepath.Join(root, "bad.git")
	marker := filepath.Join(root, "credential-helper-ran")
	runGit(t, ctx, root, "init", "--bare", remote)
	runGit(t, ctx, root, "init", "--bare", badRemote)
	runGit(t, ctx, root, "clone", remote, work)
	runGit(t, ctx, work, "config", "user.email", "agent@example.invalid")
	runGit(t, ctx, work, "config", "user.name", "Agent")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, ctx, work, "add", "README.md")
	runGit(t, ctx, work, "commit", "-m", "initial")
	runGit(t, ctx, work, "checkout", "-b", "agent-runs/run-1")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, ctx, work, "commit", "-am", "agent change")
	wantSHA := gitOutput(t, ctx, work, "rev-parse", "refs/heads/agent-runs/run-1")

	// Malicious checkout-local config: redirect the trusted URL, replace origin,
	// and install a credential helper. A vulnerable host-side push from this
	// checkout would either push to badRemote or execute the helper.
	runGit(t, ctx, work, "config", "url."+pathToGitURL(badRemote)+".insteadOf", pathToGitURL(remote))
	runGit(t, ctx, work, "remote", "set-url", "origin", pathToGitURL(badRemote))
	helpName := helperName(t, root, marker)
	runGit(t, ctx, work, "config", "credential.helper", helpName)
	if hooksDir := filepath.Join(work, ".git", "hooks"); true {
		prePush := filepath.Join(hooksDir, "pre-push")
		body := []byte("#!/bin/sh\necho hook-ran > '" + filepath.Join(root, "hook-ran") + "'\nexit 1\n")
		if runtime.GOOS == "windows" {
			body = []byte("#!/bin/sh\necho hook-ran > /tmp/windshift-hook-ran\nexit 1\n")
		}
		if err := os.WriteFile(prePush, body, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	got, err := PushBranch(ctx, PushOptions{
		Dest:         work,
		Branch:       "agent-runs/run-1",
		RemoteURL:    pathToGitURL(remote),
		GitBinary:    "git",
		AllowFileURL: true,
	})
	if err != nil {
		t.Fatalf("PushBranch: %v", err)
	}
	if got != wantSHA {
		t.Fatalf("pushed sha = %s, want %s", got, wantSHA)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("credential helper marker exists or stat failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "hook-ran")); !os.IsNotExist(err) {
		t.Fatalf("hook marker exists or stat failed: %v", err)
	}
	remoteSHA := gitOutput(t, ctx, remote, "rev-parse", "refs/heads/agent-runs/run-1")
	if remoteSHA != wantSHA {
		t.Fatalf("trusted remote sha = %s, want %s", remoteSHA, wantSHA)
	}
	badRefs := gitOutputAllowError(t, ctx, badRemote, "show-ref")
	if badRefs != "" {
		t.Fatalf("bad remote received refs: %s", badRefs)
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func runGit(t *testing.T, ctx context.Context, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func gitOutput(t *testing.T, ctx context.Context, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return stringTrimSpace(string(out))
}

func gitOutputAllowError(t *testing.T, ctx context.Context, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, _ := cmd.CombinedOutput()
	return stringTrimSpace(string(out))
}

func pathToGitURL(path string) string {
	return "file://" + filepath.ToSlash(path)
}

func helperName(t *testing.T, dir, marker string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell credential-helper test is unix-only")
	}
	path := filepath.Join(dir, "evil-credential-helper.sh")
	body := []byte("#!/bin/sh\necho ran > '" + marker + "'\nexit 1\n")
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatal(err)
	}
	return "!" + path
}

func stringTrimSpace(s string) string {
	for len(s) > 0 && (s[0] == '\n' || s[0] == '\r' || s[0] == '\t' || s[0] == ' ') {
		s = s[1:]
	}
	for len(s) > 0 {
		c := s[len(s)-1]
		if c != '\n' && c != '\r' && c != '\t' && c != ' ' {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}
