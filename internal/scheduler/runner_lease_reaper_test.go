package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/repository"
)

func newReaperTestDB(t *testing.T) database.Database {
	t.Helper()
	dsn := fmt.Sprintf("file:%s/reaper.db?mode=memory&cache=shared", t.TempDir())
	db, err := database.NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (1, 'ws', 'WS', TRUE)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	return db
}

// TestReaper_FlagsStalledQueuedRuns proves the sweep surfaces a remote-pool
// run nobody claimed: a one-time "warning" event lands on the run, and a
// second sweep does not duplicate it. A freshly queued run is not flagged.
func TestReaper_FlagsStalledQueuedRuns(t *testing.T) {
	ctx := context.Background()
	db := newReaperTestDB(t)
	runs := repository.NewAgentRunRepository(db)
	runners := repository.NewRunnerRepository(db)
	reaper := NewRunnerLeaseReaper(runs, runners)

	insertQueued := func(age time.Duration) int {
		res, err := db.Exec(
			`INSERT INTO agent_runs(workspace_id, status, target_pool_id, queued_at) VALUES (1, 'queued', 5, ?)`,
			time.Now().UTC().Add(-age),
		)
		if err != nil {
			t.Fatalf("seed queued run: %v", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("run id: %v", err)
		}
		return int(id)
	}
	stalled := insertQueued(10 * time.Minute)
	fresh := insertQueued(10 * time.Second)

	if _, _, err := reaper.Sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if has, err := runs.HasEvent(ctx, stalled, "warning"); err != nil || !has {
		t.Fatalf("stalled run should carry a warning event (has=%v err=%v)", has, err)
	}
	if has, err := runs.HasEvent(ctx, fresh, "warning"); err != nil || has {
		t.Fatalf("fresh run must not be flagged (has=%v err=%v)", has, err)
	}

	// Second sweep: the recurring log may fire again, but the run-level
	// warning event must not be duplicated.
	if _, _, err := reaper.Sweep(ctx); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	events, err := runs.ListEvents(ctx, stalled)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	warnings := 0
	for _, ev := range events {
		if ev.Type == "warning" {
			warnings++
		}
	}
	if warnings != 1 {
		t.Fatalf("want exactly 1 warning event after two sweeps, got %d", warnings)
	}
}
