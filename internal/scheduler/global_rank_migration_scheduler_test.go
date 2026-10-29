//go:build test

package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestGlobalRankMigrationSchedulerDoesNotStartStableMigration(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	scheduler := NewGlobalRankMigrationScheduler(tdb.DB, "scheduler-stable")
	result, active, err := scheduler.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if active {
		t.Fatal("stable state must not be treated as active migration")
	}
	if result.State.Phase != repository.GlobalRankPhaseStable || result.State.ActiveBucket != repository.GlobalRankBucket0 {
		t.Fatalf("RunOnce() state = %+v, want stable bucket 0", result.State)
	}
	state, err := repository.LoadGlobalRankState(tdb.DB)
	if err != nil {
		t.Fatalf("reload state: %v", err)
	}
	if state.Phase != repository.GlobalRankPhaseStable || state.TargetBucket != nil {
		t.Fatalf("scheduler changed stable state: %+v", state)
	}
}

func TestGlobalRankMigrationSchedulerResumesActiveMigration(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// The migration phase is an internal durable control state with no public
	// API representation; configure it directly to exercise the scheduler.
	if _, err := tdb.DB.Exec(`
		UPDATE global_rank_state
		SET target_bucket = 1,
		    phase = 'migrating',
		    direction = 'high_to_low',
		    frontier = NULL,
		    migrated_count = 0,
		    total_count = 0,
		    lease_owner = NULL,
		    lease_expires_at = NULL
		WHERE id = 1`); err != nil {
		t.Fatalf("set migration state: %v", err)
	}

	scheduler := NewGlobalRankMigrationScheduler(tdb.DB, "scheduler-active")
	result, active, err := scheduler.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !active {
		t.Fatal("migrating state must be treated as active")
	}
	if !result.LeaseAcquired || !result.Completed || result.Migrated != 0 {
		t.Fatalf("RunOnce() result = %+v, want acquired empty completion", result)
	}
	if result.State.Phase != repository.GlobalRankPhaseStable || result.State.ActiveBucket != repository.GlobalRankBucket1 {
		t.Fatalf("completed state = %+v, want stable bucket 1", result.State)
	}
}

func TestGlobalRankMigrationSchedulerHonorsExistingLease(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	leaseExpiry := time.Now().UTC().Add(time.Minute)
	if _, err := tdb.DB.Exec(`
		UPDATE global_rank_state
		SET target_bucket = 1,
		    phase = 'migrating',
		    direction = 'high_to_low',
		    lease_owner = ?,
		    lease_expires_at = ?
		WHERE id = 1`, "other-scheduler", leaseExpiry); err != nil {
		t.Fatalf("set leased migration state: %v", err)
	}

	scheduler := NewGlobalRankMigrationScheduler(tdb.DB, "scheduler-active")
	result, active, err := scheduler.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !active {
		t.Fatal("migrating state must be treated as active")
	}
	if result.LeaseAcquired || result.Migrated != 0 || result.Completed {
		t.Fatalf("RunOnce() result = %+v, want live lease observation", result)
	}
}

func TestGlobalRankMigrationSchedulerLeavesPausedMigrationPaused(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	if _, err := tdb.DB.Exec(`
		UPDATE global_rank_state
		SET target_bucket = 1,
		    phase = 'paused',
		    direction = 'high_to_low',
		    lease_owner = NULL,
		    lease_expires_at = NULL
		WHERE id = 1`); err != nil {
		t.Fatalf("set paused migration state: %v", err)
	}

	scheduler := NewGlobalRankMigrationScheduler(tdb.DB, "scheduler-paused")
	result, runnable, err := scheduler.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if runnable {
		t.Fatal("paused migration must not be runnable")
	}
	if result.State.Phase != repository.GlobalRankPhasePaused || result.State.ActiveBucket != repository.GlobalRankBucket0 {
		t.Fatalf("RunOnce() state = %+v, want paused bucket 0", result.State)
	}
	state, err := repository.LoadGlobalRankState(tdb.DB)
	if err != nil {
		t.Fatalf("reload state: %v", err)
	}
	if state.Phase != repository.GlobalRankPhasePaused {
		t.Fatalf("scheduler resumed paused migration: %+v", state)
	}
}

func TestGlobalRankMigrationSchedulerStartStopIsIdempotent(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	scheduler := NewGlobalRankMigrationScheduler(tdb.DB, "scheduler-lifecycle")
	scheduler.interval = time.Hour
	scheduler.Start()
	scheduler.Start()
	done := scheduler.done
	scheduler.Stop()
	scheduler.Stop()

	scheduler.mu.RLock()
	running := scheduler.running
	scheduler.mu.RUnlock()
	if running {
		t.Fatal("scheduler remains running after Stop")
	}
	select {
	case <-done:
	default:
		t.Fatal("Stop returned before the scheduler loop exited")
	}
}

func TestGlobalRankMigrationSchedulerDrainsActiveBatchesWithoutIdleInterval(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	var workspaceID int
	if err := tdb.DB.QueryRow(`
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Scheduler drain', 'SDRAIN', '', ?, ?) RETURNING id`, true, false).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	// Scheduler-layer fixture: direct rows let the test control the exact
	// active-bucket cardinality; public APIs are covered at the handler layer.
	for number, rank := range []string{"0|a1", "0|a2", "0|a3"} {
		if _, err := tdb.DB.Exec(`
			INSERT INTO items (workspace_id, workspace_item_number, title, frac_index)
			VALUES (?, ?, 'Scheduler item', ?)`, workspaceID, number+1, rank); err != nil {
			t.Fatalf("insert rank %s: %v", rank, err)
		}
	}
	if _, err := repository.ControlGlobalRankMigration(context.Background(), tdb.DB, repository.GlobalRankMigrationStart); err != nil {
		t.Fatalf("start migration: %v", err)
	}

	scheduler := NewGlobalRankMigrationScheduler(tdb.DB, "scheduler-drain")
	scheduler.interval = time.Hour
	scheduler.active = time.Millisecond
	scheduler.worker = repository.NewGlobalRankMigrationWorker(tdb.DB, "scheduler-drain", 1, time.Minute)
	completed := make(chan struct{})
	var once sync.Once
	scheduler.afterTick = func() {
		state, err := repository.LoadGlobalRankState(tdb.DB)
		if err == nil && state.Phase == repository.GlobalRankPhaseStable {
			once.Do(func() { close(completed) })
		}
	}
	scheduler.Start()
	defer scheduler.Stop()

	select {
	case <-completed:
	case <-time.After(2 * time.Second):
		t.Fatal("active migration waited for the idle scheduler interval")
	}
}
