//go:build test

package wscli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// countingServer records every request so a test can prove a host was never
// contacted.
type countingServer struct {
	*httptest.Server
	mu    sync.Mutex
	hits  int
	auths []string
}

func newCountingServer(t *testing.T) *countingServer {
	t.Helper()
	cs := &countingServer{}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.mu.Lock()
		cs.hits++
		cs.auths = append(cs.auths, r.Header.Get("Authorization"))
		cs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"id":42}}`)
	}))
	t.Cleanup(cs.Server.Close)
	return cs
}

func (c *countingServer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits
}

func (c *countingServer) authorization(i int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i >= len(c.auths) {
		return ""
	}
	return c.auths[i]
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeGlobalConfig(t *testing.T, home, url, token string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "ws")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir global config: %v", err)
	}
	writeFile(t, filepath.Join(dir, "config.toml"), fmt.Sprintf("[server]\nurl = %q\ntoken = %q\n", url, token))
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.email=test@example.com", "-c", "user.name=Test"}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitWorktree creates an empty worktree. A file written afterwards is
// untracked.
func gitWorktree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	return dir
}

// gitRepoWithTrackedFile commits name so it is tracked, like a file that
// arrived with a checkout.
func gitRepoWithTrackedFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	writeFile(t, filepath.Join(dir, name), content)
	runGit(t, dir, "add", name)
	runGit(t, dir, "commit", "-q", "-m", "add "+name)
	return dir
}

// emptyEnv neutralizes WS_* so a test only exercises file and global config.
func emptyEnv() map[string]string {
	return map[string]string{"WS_URL": "", "WS_TOKEN": "", "WS_WORKSPACE": ""}
}

// A ws.toml that shipped with the checkout must not be able to point the
// user's token at another host.
func TestTrackedWsTomlCannotRedirectCredentials(t *testing.T) {
	trusted := newCountingServer(t)
	evil := newCountingServer(t)

	home := t.TempDir()
	writeGlobalConfig(t, home, trusted.URL, "crw_global")
	repo := gitRepoWithTrackedFile(t, "ws.toml",
		fmt.Sprintf("[server]\nurl = %q\ntoken = \"crw_committed\"\n", evil.URL))

	t.Setenv("HOME", home)
	t.Chdir(repo)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"api", "/items/42"}, strings.NewReader(""), &out, &errOut, emptyEnv())
	if code != 0 {
		t.Fatalf("ws api returned %d: %s", code, errOut.String())
	}

	if got := evil.count(); got != 0 {
		t.Fatalf("evil server received %d requests, want 0", got)
	}
	if got := trusted.count(); got != 1 {
		t.Fatalf("trusted server received %d requests, want 1", got)
	}
	if got := trusted.authorization(0); got != "Bearer crw_global" {
		t.Fatalf("trusted Authorization = %q, want the global token", got)
	}
	if !strings.Contains(errOut.String(), "committed to git") {
		t.Fatalf("stderr = %q, want a committed-to-git warning", errOut.String())
	}
}

// A file the user created in a worktree but has not committed is their own.
func TestUntrackedWsTomlInWorktreeIsUsed(t *testing.T) {
	server := newCountingServer(t)
	repo := gitWorktree(t)
	writeFile(t, filepath.Join(repo, "ws.toml"),
		fmt.Sprintf("[server]\nurl = %q\ntoken = \"crw_untracked\"\n", server.URL))

	t.Setenv("HOME", t.TempDir())
	t.Chdir(repo)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"api", "/items/42"}, strings.NewReader(""), &out, &errOut, emptyEnv())
	if code != 0 {
		t.Fatalf("ws api returned %d: %s", code, errOut.String())
	}
	if got := server.count(); got != 1 {
		t.Fatalf("server received %d requests, want 1", got)
	}
	if got := server.authorization(0); got != "Bearer crw_untracked" {
		t.Fatalf("Authorization = %q, want the untracked token", got)
	}
}

// Outside a worktree there is no way to tell where the file came from, so it
// is trusted to keep local directories working.
func TestNonGitWsTomlIsUsed(t *testing.T) {
	server := newCountingServer(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ws.toml"),
		fmt.Sprintf("[server]\nurl = %q\ntoken = \"crw_local\"\n", server.URL))

	t.Setenv("HOME", t.TempDir())
	t.Chdir(dir)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"api", "/items/42"}, strings.NewReader(""), &out, &errOut, emptyEnv())
	if code != 0 {
		t.Fatalf("ws api returned %d: %s", code, errOut.String())
	}
	if got := server.count(); got != 1 {
		t.Fatalf("server received %d requests, want 1", got)
	}
}

// Without git the classifier cannot run; the file is trusted rather than
// failing the command.
func TestGitUnavailableTrustsDiscoveredWsToml(t *testing.T) {
	server := newCountingServer(t)
	repo := gitRepoWithTrackedFile(t, "ws.toml",
		fmt.Sprintf("[server]\nurl = %q\ntoken = \"crw_tracked\"\n", server.URL))

	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "")
	t.Chdir(repo)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"api", "/items/42"}, strings.NewReader(""), &out, &errOut, emptyEnv())
	if code != 0 {
		t.Fatalf("ws api returned %d: %s", code, errOut.String())
	}
	if got := server.count(); got != 1 {
		t.Fatalf("server received %d requests, want 1", got)
	}
}

// The first-run browser flow must not be seeded by a tracked ws.toml, or the
// repo could phish the user's credentials.
func TestInitGlobalIgnoresTrackedWsTomlUrl(t *testing.T) {
	evil := newCountingServer(t)
	repo := gitRepoWithTrackedFile(t, "ws.toml", fmt.Sprintf("[server]\nurl = %q\n", evil.URL))

	t.Setenv("HOME", t.TempDir())
	t.Chdir(repo)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"init", "--global"}, strings.NewReader(""), &out, &errOut, emptyEnv())
	if code == 0 {
		t.Fatalf("init --global succeeded with only a tracked ws.toml URL: %s", out.String())
	}
	if got := evil.count(); got != 0 {
		t.Fatalf("evil server received %d requests, want 0", got)
	}
}
