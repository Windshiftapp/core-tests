//go:build test

package services

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"windshift/internal/models"
)

// RerunForItem backs the manual "Re-run" button on the item agent log. It
// derives the agent from the item's last run, reuses that binding, and guards
// against stacking a second run while one is in flight.

func TestBindingService_Rerun_StartsRunFromLastBinding(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	bindingID := seedMentionBinding(t, st, st.AgentID)
	itemID := seedItem(t, st.DB, 1)

	// A finished prior run that carried the binding.
	if _, err := st.DB.Exec(`INSERT INTO agent_runs(workspace_id, item_id, binding_id, status) VALUES (1, ?, ?, ?)`,
		itemID, bindingID, models.AgentRunStatusSucceeded); err != nil {
		t.Fatalf("seed prior run: %v", err)
	}

	started, err := st.BS.RerunForItem(ctx, itemID, st.AdminID)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if !started {
		t.Fatal("want started=true")
	}
	st.BS.runs.Wait()
	if got := atomic.LoadInt32(st.RunCalls); got != 1 {
		t.Fatalf("expected 1 runner invocation, got %d", got)
	}

	// The new run is attributed to the clicking user (the SCM principal).
	var triggeredBy int
	if err := st.DB.QueryRow(`SELECT triggered_by_user_id FROM agent_runs WHERE status = 'queued' OR status = 'running' OR status = 'succeeded' ORDER BY id DESC LIMIT 1`).Scan(&triggeredBy); err != nil {
		t.Fatalf("read run: %v", err)
	}
	if triggeredBy != st.AdminID {
		t.Errorf("triggered_by_user_id: want %d, got %d", st.AdminID, triggeredBy)
	}
}

func TestBindingService_Rerun_DedupWhenActive(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	bindingID := seedMentionBinding(t, st, st.AgentID)
	itemID := seedItem(t, st.DB, 1)

	// A run already in flight: re-run must be a no-op, not a stacked job.
	if _, err := st.DB.Exec(`INSERT INTO agent_runs(workspace_id, item_id, binding_id, status) VALUES (1, ?, ?, ?)`,
		itemID, bindingID, models.AgentRunStatusRunning); err != nil {
		t.Fatalf("seed active run: %v", err)
	}

	started, err := st.BS.RerunForItem(ctx, itemID, st.AdminID)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if started {
		t.Fatal("want started=false while a run is active")
	}
	if got := atomic.LoadInt32(st.RunCalls); got != 0 {
		t.Fatalf("expected no runner invocation, got %d", got)
	}
}

func TestBindingService_Rerun_NoPriorRun(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	itemID := seedItem(t, st.DB, 1)

	if _, err := st.BS.RerunForItem(ctx, itemID, st.AdminID); !errors.Is(err, ErrRerunNoPriorRun) {
		t.Fatalf("want ErrRerunNoPriorRun, got %v", err)
	}
}

func TestBindingService_Rerun_LastRunHasNoBinding(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	itemID := seedItem(t, st.DB, 1)

	// A manual/test run with no binding can't be reconstructed.
	if _, err := st.DB.Exec(`INSERT INTO agent_runs(workspace_id, item_id, status) VALUES (1, ?, ?)`,
		itemID, models.AgentRunStatusSucceeded); err != nil {
		t.Fatalf("seed prior run: %v", err)
	}

	if _, err := st.BS.RerunForItem(ctx, itemID, st.AdminID); !errors.Is(err, ErrRerunNoBinding) {
		t.Fatalf("want ErrRerunNoBinding, got %v", err)
	}
}
