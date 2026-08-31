package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
)

func TestClaimQueuedForRunnerEnforcesCurrentPoolScopeAndState(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "agent-run-pool-security.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	insertWorkspace := func(name, key string) int {
		t.Helper()
		var id int
		if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES (?, ?) RETURNING id`, name, key).Scan(&id); err != nil {
			t.Fatalf("insert workspace: %v", err)
		}
		return id
	}
	workspaceA := insertWorkspace("Pool A", "PAX")
	workspaceB := insertWorkspace("Pool B", "PBX")

	capability := &models.ActionCapability{
		Name:                   "Scoped runner pool",
		CapabilityType:         models.CapabilityRunnerPool,
		Config:                 `{}`,
		IsEnabled:              true,
		AppliesToAllWorkspaces: false,
	}
	actionRepo := NewActionRepository(db)
	poolID, err := actionRepo.CreateCapabilityWithWorkspaces(capability, []int{workspaceA})
	if err != nil {
		t.Fatalf("CreateCapabilityWithWorkspaces: %v", err)
	}
	runRepo := NewAgentRunRepository(db)
	insertRun := func(workspaceID int) int {
		t.Helper()
		id, err := runRepo.Insert(context.Background(), &models.AgentRun{
			WorkspaceID:  workspaceID,
			TargetPoolID: &poolID,
			JobKind:      models.JobKindCodingAgent,
		})
		if err != nil {
			t.Fatalf("insert run: %v", err)
		}
		return id
	}

	// Put the unauthorized workspace-B run at the head of the FIFO. The claim
	// must skip it and return the oldest currently authorized run instead.
	runB := insertRun(workspaceB)
	runA := insertRun(workspaceA)
	claimed, err := runRepo.ClaimQueuedForRunner(context.Background(), poolID, 501, time.Now())
	if err != nil {
		t.Fatalf("ClaimQueuedForRunner scoped A: %v", err)
	}
	if claimed == nil || claimed.ID != runA {
		t.Fatalf("claimed run = %+v, want authorized workspace-A run %d (workspace-B run %d must be skipped)", claimed, runA, runB)
	}

	// Narrowing the capability to B takes effect for work that was queued
	// before the scope change.
	if err := actionRepo.UpdateCapabilityWithWorkspaces(capability, []int{workspaceB}); err != nil {
		t.Fatalf("scope pool to B: %v", err)
	}
	claimed, err = runRepo.ClaimQueuedForRunner(context.Background(), poolID, 502, time.Now())
	if err != nil {
		t.Fatalf("ClaimQueuedForRunner scoped B: %v", err)
	}
	if claimed == nil || claimed.ID != runB {
		t.Fatalf("claimed run = %+v, want newly authorized workspace-B run %d", claimed, runB)
	}

	queuedWhileEnabled := insertRun(workspaceB)
	capability.IsEnabled = false
	if err := actionRepo.UpdateCapability(capability); err != nil {
		t.Fatalf("disable pool: %v", err)
	}
	claimed, err = runRepo.ClaimQueuedForRunner(context.Background(), poolID, 503, time.Now())
	if err != nil {
		t.Fatalf("ClaimQueuedForRunner disabled: %v", err)
	}
	if claimed != nil {
		t.Fatalf("disabled pool claimed run %+v", claimed)
	}
	assertAgentRunQueued(t, runRepo, queuedWhileEnabled)

	capability.IsEnabled = true
	if err := actionRepo.UpdateCapability(capability); err != nil {
		t.Fatalf("re-enable pool: %v", err)
	}
	if err := actionRepo.DeleteCapability(poolID); err != nil {
		t.Fatalf("delete pool: %v", err)
	}
	claimed, err = runRepo.ClaimQueuedForRunner(context.Background(), poolID, 504, time.Now())
	if err != nil {
		t.Fatalf("ClaimQueuedForRunner deleted: %v", err)
	}
	if claimed != nil {
		t.Fatalf("deleted pool claimed run %+v", claimed)
	}
	assertAgentRunQueued(t, runRepo, queuedWhileEnabled)
}

func assertAgentRunQueued(t *testing.T, repo *AgentRunRepository, runID int) {
	t.Helper()
	run, err := repo.Get(context.Background(), runID)
	if err != nil {
		t.Fatalf("load run %d: %v", runID, err)
	}
	if run.Status != models.AgentRunStatusQueued || run.RunnerID != nil {
		t.Fatalf("run %d status=%q runner=%v, want queued and unclaimed", runID, run.Status, run.RunnerID)
	}
}
