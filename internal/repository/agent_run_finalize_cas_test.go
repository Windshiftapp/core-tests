package repository

import (
	"context"
	"testing"
	"time"

	"windshift/internal/models"
)

// TestAgentRunRepository_FinalizeRunning_CAS pins the compare-and-swap
// finalize used by the untrusted remote runner path (WI-168): a terminal
// stamp lands only while the run is running, and a second attempt on an
// already-terminal run is a no-op that reports transitioned=false (so the
// caller skips re-emitting events / re-firing the PR hook).
func TestAgentRunRepository_FinalizeRunning_CAS(t *testing.T) {
	ctx := context.Background()
	db := openAgentRunTestDB(t)
	repo := NewAgentRunRepository(db)

	id, err := repo.Insert(ctx, &models.AgentRun{WorkspaceID: 1})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)

	// Queued (not running) → CAS must not transition it.
	transitioned, err := repo.FinalizeRunning(ctx, id, models.AgentRunStatusFailed, "boom", now)
	if err != nil {
		t.Fatalf("finalize queued: %v", err)
	}
	if transitioned {
		t.Fatal("queued run should not finalize via FinalizeRunning")
	}
	got, _ := repo.Get(ctx, id)
	if got.Status != models.AgentRunStatusQueued {
		t.Fatalf("status after no-op: want queued, got %q", got.Status)
	}

	// Running → first finalize transitions.
	if err := repo.MarkRunning(ctx, id, "", now); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	transitioned, err = repo.FinalizeRunning(ctx, id, models.AgentRunStatusSucceeded, "", now)
	if err != nil {
		t.Fatalf("finalize running: %v", err)
	}
	if !transitioned {
		t.Fatal("running run should finalize")
	}
	got, _ = repo.Get(ctx, id)
	if got.Status != models.AgentRunStatusSucceeded {
		t.Fatalf("status after finalize: want succeeded, got %q", got.Status)
	}

	// Second finalize (replay / late report) must be a no-op and must not
	// rewrite the terminal status or error.
	transitioned, err = repo.FinalizeRunning(ctx, id, models.AgentRunStatusFailed, "rewrite attempt", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("finalize replay: %v", err)
	}
	if transitioned {
		t.Fatal("already-terminal run must not finalize again")
	}
	got, _ = repo.Get(ctx, id)
	if got.Status != models.AgentRunStatusSucceeded {
		t.Fatalf("status must be unchanged after replay: got %q", got.Status)
	}
	if got.Error != "" {
		t.Fatalf("error must not be rewritten: got %q", got.Error)
	}
}

// TestAgentRunRepository_FinalizeRunning_RejectsNonTerminal guards the input
// contract: a non-terminal target status is an error, never a write.
func TestAgentRunRepository_FinalizeRunning_RejectsNonTerminal(t *testing.T) {
	ctx := context.Background()
	db := openAgentRunTestDB(t)
	repo := NewAgentRunRepository(db)

	id, err := repo.Insert(ctx, &models.AgentRun{WorkspaceID: 1})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := repo.FinalizeRunning(ctx, id, models.AgentRunStatusRunning, "", time.Now().UTC()); err == nil {
		t.Fatal("expected error finalizing to a non-terminal status")
	}
}
