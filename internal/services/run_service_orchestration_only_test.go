package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"windshift/internal/auth"
	"windshift/internal/models"
	"windshift/internal/repository"
)

// TestRunService_OrchestrationOnly_RejectsLocalRun pins the orchestration-only
// wiring: a RunService built with no Runner starts no in-process worker pool,
// reports LocalExecutionEnabled()==false, and rejects a local (non-pool) run up
// front — without inserting a row that nothing would ever claim.
func TestRunService_OrchestrationOnly_RejectsLocalRun(t *testing.T) {
	ctx := context.Background()
	db := newRunServiceTestDB(t)
	repo := repository.NewAgentRunRepository(db)

	svc, err := NewRunService(repo, RunServiceOptions{Logger: silentLogger(t)})
	if err != nil {
		t.Fatalf("new orchestration-only svc: %v", err)
	}
	if svc.LocalExecutionEnabled() {
		t.Fatal("LocalExecutionEnabled: want false for a runner-less service")
	}

	runID, err := svc.Start(ctx, RunRequest{WorkspaceID: 1})
	if !errors.Is(err, ErrLocalRunnerDisabled) {
		t.Fatalf("Start(local run): want ErrLocalRunnerDisabled, got id=%d err=%v", runID, err)
	}
	if runID != 0 {
		t.Errorf("Start should not return a run id when rejecting: got %d", runID)
	}
	// No orphan row left behind.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_runs`).Scan(&count); err != nil {
		t.Fatalf("count agent_runs: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no agent_runs rows after a rejected local start, got %d", count)
	}
}

// TestRunService_OrchestrationOnly_RemotePoolStillWorks proves the whole point
// of the split: with no in-process runner, a pool-targeted run still queues and
// a remote claim is enriched with the per-run token + grants. This is exactly
// what a remote runner (windshift-runner) consumes.
func TestRunService_OrchestrationOnly_RemotePoolStillWorks(t *testing.T) {
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

	// Orchestration-only: Tokens + post-run hook, but no Runner.
	svc, err := NewRunService(repo, RunServiceOptions{Tokens: tokens, Logger: silentLogger(t)})
	if err != nil {
		t.Fatalf("new orchestration-only svc: %v", err)
	}
	if svc.LocalExecutionEnabled() {
		t.Fatal("LocalExecutionEnabled: want false")
	}
	svc.SetBindingInputsResolver(&fakeBindingInputs{
		spec: &TokenSpec{ActingUserID: actingUserID, TTL: 5 * time.Minute, Name: "agent-run:remote"},
		grants: &models.RunGrants{
			Git: &models.GitGrant{Repo: "owner/repo", ConnectionID: 7},
			LLM: &models.LLMGrant{ConnectionID: 9},
		},
		env: map[string]string{"WS_WORKSPACE_KEY": "WS"},
	})

	// A pool-targeted run goes through Start (the trigger path) and must end up
	// queued — never sent to the (non-existent) in-process worker pool.
	pool := 42
	binding := 3
	runID, err := svc.Start(ctx, RunRequest{
		WorkspaceID:  1,
		BindingID:    binding,
		TargetPoolID: &pool,
		JobKind:      models.JobKindCodingAgent,
		Grants:       &models.RunGrants{Skills: []models.SkillGrant{{ID: 6, Name: "queued", Body: "saved-body"}}},
	})
	if err != nil {
		t.Fatalf("Start(remote pool run): %v", err)
	}
	run, err := repo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != models.AgentRunStatusQueued {
		t.Fatalf("remote run status: want queued, got %q", run.Status)
	}
	if run.TargetPoolID == nil || *run.TargetPoolID != pool {
		t.Fatalf("remote run target pool not persisted: %v", run.TargetPoolID)
	}
	var queuedGrants models.RunGrants
	if err := json.Unmarshal([]byte(run.GrantsJSON), &queuedGrants); err != nil {
		t.Fatalf("decode queued grants: %v", err)
	}
	if len(queuedGrants.Skills) != 1 || queuedGrants.Skills[0].Body != "saved-body" {
		t.Fatalf("remote start did not persist skill snapshot: %+v", queuedGrants)
	}

	// The claim a remote runner would make is enriched the same way it always
	// was — token minted, grants bound — despite there being no local loop.
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
	if tokenID, _, grants, _, _ := repo.GetRunAuthz(ctx, runID); tokenID == 0 || grants == nil || grants.Git == nil {
		t.Errorf("expected token + grants bound after remote claim: tokenID=%d grants=%+v", tokenID, grants)
	}
}
