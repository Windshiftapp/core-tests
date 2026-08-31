package repository

import (
	"context"
	"testing"
	"time"

	"windshift/internal/models"
)

// TestForceCancelRunning verifies the admin phantom-run escape hatch: a running
// run is transitioned straight to canceled, but the CAS no-ops on a run that is
// not running (already terminal), so it can't clobber a finished run (WI-512).
func TestForceCancelRunning(t *testing.T) {
	ctx := context.Background()
	db := openAgentRunTestDB(t)
	repo := NewAgentRunRepository(db)
	now := time.Now().UTC().Truncate(time.Second)

	// A running run → force cancels.
	id, err := repo.Insert(ctx, &models.AgentRun{WorkspaceID: 1})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := repo.MarkRunning(ctx, id, "container-x", now); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	forced, err := repo.ForceCancelRunning(ctx, id, now)
	if err != nil {
		t.Fatalf("force cancel: %v", err)
	}
	if !forced {
		t.Fatal("want forced=true for a running run")
	}
	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != models.AgentRunStatusCanceled {
		t.Errorf("status = %q, want canceled", got.Status)
	}
	if got.EndedAt == nil {
		t.Error("ended_at should be set after force cancel")
	}

	// A second force cancel no-ops (already terminal).
	again, err := repo.ForceCancelRunning(ctx, id, now)
	if err != nil {
		t.Fatalf("force cancel again: %v", err)
	}
	if again {
		t.Error("force cancel must no-op on an already-terminal run")
	}

	// A queued run is not 'running' → no-op (cancel-queued is the right path).
	qid, err := repo.Insert(ctx, &models.AgentRun{WorkspaceID: 1})
	if err != nil {
		t.Fatalf("insert queued: %v", err)
	}
	if forced, _ := repo.ForceCancelRunning(ctx, qid, now); forced {
		t.Error("force cancel must no-op on a queued run")
	}
}
