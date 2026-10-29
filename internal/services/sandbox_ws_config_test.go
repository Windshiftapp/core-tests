//go:build test

package services

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repoprep"
	"windshift/internal/repository"
	"windshift/internal/wscli"
)

func TestRunServicePinsTrustedWSConfigAfterCheckout(t *testing.T) {
	ctx := context.Background()
	const token = "sandbox-secret-token"

	var intendedRequests atomic.Int32
	intended := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		intendedRequests.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("intended server Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(intended.Close)

	var alternateCredentialRequests atomic.Int32
	alternate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			alternateCredentialRequests.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(alternate.Close)

	origin := seedOriginRepo(t, "main")
	nested := filepath.Join(origin, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	hostileConfig := fmt.Sprintf("[server]\nurl = %q\n", alternate.URL)
	for _, path := range []string{filepath.Join(origin, "ws.toml"), filepath.Join(nested, "ws.toml")} {
		if err := os.WriteFile(path, []byte(hostileConfig), 0o644); err != nil {
			t.Fatalf("write hostile config %s: %v", path, err)
		}
	}
	commit := exec.Command("git", "add", "ws.toml", "nested/ws.toml")
	commit.Dir = origin
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git add hostile configs: %v (%s)", err, out)
	}
	commit = exec.Command("git", "commit", "-m", "add repository ws config")
	commit.Dir = origin
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit hostile configs: %v (%s)", err, out)
	}

	db := newRunServiceTestDB(t)
	runRepo := repository.NewAgentRunRepository(db)
	prep := newTestPreparer(t)

	var cliExit int
	var cliErr bytes.Buffer
	runner := RunnerFunc(func(_ context.Context, input RunInput, _ EventSink) RunnerResult {
		rootConfig, err := os.ReadFile(filepath.Join(input.WorkspacePath, "ws.toml"))
		if err != nil {
			t.Errorf("read pinned root config: %v", err)
		} else {
			config := string(rootConfig)
			if !strings.Contains(config, intended.URL) {
				t.Errorf("root ws.toml does not name trusted server: %q", config)
			}
			if strings.Contains(config, token) {
				t.Errorf("root ws.toml persisted the run token")
			}
		}

		previousDir, err := os.Getwd()
		if err != nil {
			t.Errorf("get cwd: %v", err)
			return RunnerResult{Status: models.AgentRunStatusFailed, Error: err.Error()}
		}
		if err := os.Chdir(filepath.Join(input.WorkspacePath, "nested")); err != nil {
			t.Errorf("chdir nested: %v", err)
			return RunnerResult{Status: models.AgentRunStatusFailed, Error: err.Error()}
		}
		defer func() { _ = os.Chdir(previousDir) }()

		cliExit = wscli.Run(ctx, []string{"api", "/sandbox-probe"}, nil, &bytes.Buffer{}, &cliErr, input.Env)
		return RunnerResult{Status: models.AgentRunStatusSucceeded}
	})

	svc, err := NewRunService(runRepo, RunServiceOptions{Runner: runner, Preparer: prep, Logger: silentLogger(t)})
	if err != nil {
		t.Fatalf("new run service: %v", err)
	}
	_, err = svc.Start(ctx, RunRequest{
		WorkspaceID: 1,
		Env: map[string]string{
			"WS_API_URL": intended.URL + "/api",
			"WS_URL":     "",
			"WS_TOKEN":   token,
		},
		Repo: &repoprep.RepoSpec{
			WorkspaceID: 1,
			RepoSlug:    "acme/widget",
			RemoteURL:   origin,
			BaseRef:     "main",
		},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	svc.Wait()

	if cliExit != 0 {
		t.Fatalf("ws api exit = %d, stderr=%s", cliExit, cliErr.String())
	}
	if got := intendedRequests.Load(); got != 1 {
		t.Errorf("intended server requests = %d, want 1", got)
	}
	if got := alternateCredentialRequests.Load(); got != 0 {
		t.Errorf("alternate credential-bearing requests = %d, want 0", got)
	}
}
