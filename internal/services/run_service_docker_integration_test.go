//go:build agent_e2e

// End-to-end smokes that exercise the real Docker container path of the
// coding-agent harness:
//   - TestRunService_DockerSmoke           (WI-84): bare lifecycle echo
//   - TestRunService_DockerWithWorktree    (WI-85): /workspace bind-mount
//   - TestRunService_DockerWithTokenAndEnv (WI-86): rendered ws.toml +
//     injected WS_TOKEN
//
// Skipped from the default `go test ./...` invocation via the agent_e2e
// build tag. Run manually with:
//
//	docker build -f deploy/coding-agent/Dockerfile \
//	  --build-arg INSTALL_PI=false \
//	  -t windshift/coding-agent:wi-86-skeleton .
//	go test -tags agent_e2e ./internal/services/... -run TestRunService_Docker -v
//
// INSTALL_PI=false skips the multi-MB npm install — fine for these tests
// because they don't invoke pi. Production builds default to INSTALL_PI=true.
package services

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"windshift/internal/auth"
	"windshift/internal/models"
	"windshift/internal/repoprep"
	"windshift/internal/repository"
)

const dockerSmokeImage = "windshift/coding-agent:wi-86-skeleton"

func TestRunService_DockerSmoke(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	// Confirm the image is present locally; we don't auto-build to keep
	// the test free of side effects.
	if err := exec.Command("docker", "image", "inspect", dockerSmokeImage).Run(); err != nil {
		t.Skipf("image %q not present locally — build it first (see file header)", dockerSmokeImage)
	}

	db := newRunServiceTestDB(t)
	repo := repository.NewAgentRunRepository(db)

	runner := &DockerRunner{
		Image: dockerSmokeImage,
		Env: map[string]string{
			"WS_WORKSPACE_ID":   "1",
			"WINDSHIFT_ITEM_ID": "WI-84",
		},
	}
	svc, err := NewRunService(repo, RunServiceOptions{
		GlobalCap: 2,
		Runner:    runner,
		Logger:    silentLogger(t),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runID, err := svc.Start(ctx, RunRequest{WorkspaceID: 1})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	svc.Wait()

	got, err := repo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != models.AgentRunStatusSucceeded {
		t.Fatalf("run status: want succeeded, got %q (err=%q)", got.Status, got.Error)
	}

	events, err := repo.ListEvents(ctx, runID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}

	foundEntrypointLine := false
	for _, ev := range events {
		if ev.Type != "stdout" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(ev.PayloadJSON), &payload); err != nil {
			// Non-JSON stdout lines get wrapped as {"line":...}; verify
			// the wrapper instead.
			if !strings.Contains(ev.PayloadJSON, `"line"`) {
				t.Errorf("stdout payload not JSON-shaped: %q", ev.PayloadJSON)
			}
			continue
		}
		if phase, _ := payload["phase"].(string); phase == "skeleton" {
			foundEntrypointLine = true
			if itemSent, _ := payload["item_id"].(string); itemSent != "WI-84" {
				t.Errorf("entrypoint saw item_id=%q, want WI-84", itemSent)
			}
		}
	}
	if !foundEntrypointLine {
		t.Errorf("expected entrypoint's skeleton lifecycle line in stdout events; got %d events", len(events))
		for _, ev := range events {
			t.Logf("  event: type=%s payload=%s", ev.Type, ev.PayloadJSON)
		}
	}
}

// TestRunService_DockerWithWorktree is the Phase 2 (WI-85) e2e: prepare a
// worktree from a local origin, mount it into the runner container via
// RunService → WorktreeManager → DockerRunner, and confirm the entrypoint
// sees /workspace with the expected file present.
func TestRunService_DockerWithWorktree(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	if err := exec.Command("docker", "image", "inspect", dockerSmokeImage).Run(); err != nil {
		t.Skipf("image %q not present locally — build it first (see file header)", dockerSmokeImage)
	}

	db := newRunServiceTestDB(t)
	repoDB := repository.NewAgentRunRepository(db)

	origin := seedOriginRepo(t, "main")
	prep := newTestPreparer(t)

	runner := &DockerRunner{Image: dockerSmokeImage}
	svc, err := NewRunService(repoDB, RunServiceOptions{
		Runner:   runner,
		Preparer: prep,
		Logger:   silentLogger(t),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

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

	got, err := repoDB.Get(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != models.AgentRunStatusSucceeded {
		t.Fatalf("status: want succeeded, got %q (err=%q)", got.Status, got.Error)
	}

	events, err := repoDB.ListEvents(ctx, runID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	sawWorkspace := false
	for _, ev := range events {
		if ev.Type != "stdout" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(ev.PayloadJSON), &payload); err != nil {
			continue
		}
		if kind, _ := payload["type"].(string); kind == "workspace" {
			sawWorkspace = true
			if mounted, _ := payload["mounted"].(bool); !mounted {
				t.Errorf("workspace event says not mounted: %s", ev.PayloadJSON)
			}
			if readme, _ := payload["readme"].(bool); !readme {
				t.Errorf("workspace event says README missing: %s", ev.PayloadJSON)
			}
		}
	}
	if !sawWorkspace {
		t.Errorf("expected a workspace event from the entrypoint; events=%+v", events)
	}
}

// TestRunService_DockerWithTokenAndEnv (WI-86) drives a real container
// with WS_TOKEN minted via RunTokenService and verifies the entrypoint's
// envsubst rendered ws.toml correctly — the api_url and workspace_key
// match what we injected and token_present is true.
func TestRunService_DockerWithTokenAndEnv(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	if err := exec.Command("docker", "image", "inspect", dockerSmokeImage).Run(); err != nil {
		t.Skipf("image %q not present locally — build it first (see file header)", dockerSmokeImage)
	}

	db, actingUserID := newTokenTestDB(t)
	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (1, 'WI', 'WI', true)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	repoDB := repository.NewAgentRunRepository(db)
	tm := auth.NewTokenManager(db, nil)
	tokens, _ := NewRunTokenService(tm)

	runner := &DockerRunner{Image: dockerSmokeImage}
	svc, err := NewRunService(repoDB, RunServiceOptions{
		Runner: runner,
		Tokens: tokens,
		Logger: silentLogger(t),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runID, err := svc.Start(ctx, RunRequest{
		WorkspaceID: 1,
		Token: &TokenSpec{
			ActingUserID: actingUserID,
			Name:         "agent-run:wi-86-e2e",
		},
		Env: map[string]string{
			"WS_API_URL":        "http://windshift.test/api",
			"WS_WORKSPACE_KEY":  "WI",
			"WS_REFRESH_DOCS":   "false", // don't call out to a fake server
			"WINDSHIFT_ITEM_ID": "WI-86",
		},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	svc.Wait()

	got, err := repoDB.Get(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != models.AgentRunStatusSucceeded {
		t.Fatalf("status: want succeeded, got %q (err=%q)", got.Status, got.Error)
	}

	events, err := repoDB.ListEvents(ctx, runID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	sawWSConfig := false
	for _, ev := range events {
		if ev.Type != "stdout" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(ev.PayloadJSON), &payload); err != nil {
			continue
		}
		if kind, _ := payload["type"].(string); kind == "ws_config" {
			sawWSConfig = true
			if got, _ := payload["api_url"].(string); got != "http://windshift.test/api" {
				t.Errorf("ws_config.api_url: want http://windshift.test/api, got %q", got)
			}
			if got, _ := payload["workspace_key"].(string); got != "WI" {
				t.Errorf("ws_config.workspace_key: want WI, got %q", got)
			}
			if tp, _ := payload["token_present"].(bool); !tp {
				t.Errorf("ws_config.token_present: want true, got %v (event=%s)", tp, ev.PayloadJSON)
			}
		}
	}
	if !sawWSConfig {
		t.Errorf("expected a ws_config event proving envsubst rendered the toml; events=%+v", events)
	}
}
