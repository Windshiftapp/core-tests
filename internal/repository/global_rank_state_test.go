//go:build test

package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

func TestGlobalRankStateSchemaStartsStable(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	state, err := LoadGlobalRankState(tdb.DB)
	if err != nil {
		t.Fatalf("LoadGlobalRankState() error = %v", err)
	}
	if state.ActiveBucket != GlobalRankBucket0 {
		t.Fatalf("active bucket = %d, want 0", state.ActiveBucket)
	}
	if state.Phase != GlobalRankPhaseStable {
		t.Fatalf("phase = %q, want %q", state.Phase, GlobalRankPhaseStable)
	}
	if state.TargetBucket != nil || state.Direction != nil {
		t.Fatalf("stable state has migration fields: %+v", state)
	}
}

func TestGlobalRankStateRoundTripPersistsMigrationProgress(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	target := GlobalRankBucket1
	direction := GlobalRankDirectionHighToLow
	frontier := "0|a1"
	owner := "balancer-1"
	lastError := ""
	expires := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	want := GlobalRankState{
		ActiveBucket:   GlobalRankBucket0,
		TargetBucket:   &target,
		Phase:          GlobalRankPhaseMigrating,
		Direction:      &direction,
		Frontier:       &frontier,
		LeaseOwner:     &owner,
		LeaseExpiresAt: &expires,
		MigratedCount:  25,
		TotalCount:     100,
		LastError:      &lastError,
	}

	if err := database.WithTx(tdb.DB, func(tx database.Tx) error {
		return SaveGlobalRankState(tx, want)
	}); err != nil {
		t.Fatalf("SaveGlobalRankState() error = %v", err)
	}

	got, err := LoadGlobalRankState(tdb.DB)
	if err != nil {
		t.Fatalf("LoadGlobalRankState() error = %v", err)
	}
	if got.ActiveBucket != want.ActiveBucket || got.Phase != want.Phase || got.MigratedCount != want.MigratedCount || got.TotalCount != want.TotalCount {
		t.Fatalf("state scalar fields = %+v, want %+v", got, want)
	}
	if got.TargetBucket == nil || *got.TargetBucket != *want.TargetBucket {
		t.Fatalf("target bucket = %v, want %d", got.TargetBucket, *want.TargetBucket)
	}
	if got.Direction == nil || *got.Direction != *want.Direction {
		t.Fatalf("direction = %v, want %q", got.Direction, *want.Direction)
	}
	if got.Frontier == nil || *got.Frontier != frontier {
		t.Fatalf("frontier = %v, want %q", got.Frontier, frontier)
	}
	if got.LeaseOwner == nil || *got.LeaseOwner != owner {
		t.Fatalf("lease owner = %v, want %q", got.LeaseOwner, owner)
	}
	if got.LeaseExpiresAt == nil || got.LeaseExpiresAt.IsZero() {
		t.Fatal("lease expiry was not persisted")
	}
	if got.LastError == nil || *got.LastError != lastError {
		t.Fatalf("last error = %v, want empty string pointer", got.LastError)
	}
}

func TestGlobalRankMigrationControlsStartPauseResume(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	workspaceID := createFracIndexTestWorkspace(t, tdb.DB)
	insertItemWithFracIndex(t, tdb.DB, workspaceID, 1, "0|a1")

	started, err := ControlGlobalRankMigration(context.Background(), tdb.DB, GlobalRankMigrationStart)
	if err != nil {
		t.Fatalf("start migration: %v", err)
	}
	if started.Phase != GlobalRankPhaseMigrating || started.TargetBucket == nil || *started.TargetBucket != GlobalRankBucket1 {
		t.Fatalf("started state = %+v, want migrating to bucket 1", started)
	}
	if started.Direction == nil || *started.Direction != GlobalRankDirectionHighToLow || started.TotalCount != 1 {
		t.Fatalf("started state = %+v, want high-to-low total 1", started)
	}

	if _, err := ControlGlobalRankMigration(context.Background(), tdb.DB, GlobalRankMigrationStart); !errors.Is(err, ErrGlobalRankMigrationConflict) {
		t.Fatalf("second start error = %v, want state conflict", err)
	}
	paused, err := ControlGlobalRankMigration(context.Background(), tdb.DB, GlobalRankMigrationPause)
	if err != nil {
		t.Fatalf("pause migration: %v", err)
	}
	if paused.Phase != GlobalRankPhasePaused || paused.LeaseOwner != nil || paused.LeaseExpiresAt != nil {
		t.Fatalf("paused state = %+v, want paused without lease", paused)
	}

	resumed, err := ControlGlobalRankMigration(context.Background(), tdb.DB, GlobalRankMigrationResume)
	if err != nil {
		t.Fatalf("resume migration: %v", err)
	}
	if resumed.Phase != GlobalRankPhaseMigrating || resumed.Frontier != nil || resumed.TargetBucket == nil || *resumed.TargetBucket != GlobalRankBucket1 {
		t.Fatalf("resumed state = %+v, want original migration progress", resumed)
	}
	if _, err := ControlGlobalRankMigration(context.Background(), tdb.DB, GlobalRankMigrationResume); !errors.Is(err, ErrGlobalRankMigrationConflict) {
		t.Fatalf("second resume error = %v, want state conflict", err)
	}
}

func TestGlobalRankMigrationControlResetsFailedState(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	workspaceID := createFracIndexTestWorkspace(t, tdb.DB)
	insertItemWithFracIndex(t, tdb.DB, workspaceID, 1, "0|a1")

	frontier := "0|a5"
	owner := "failed-worker"
	expires := time.Now().UTC().Add(time.Minute)
	lastError := "item 1 has invalid active-bucket rank"
	if _, err := tdb.DB.Exec(`
		UPDATE global_rank_state
		SET target_bucket = 1,
		    phase = 'failed',
		    direction = 'high_to_low',
		    frontier = ?,
		    lease_owner = ?,
		    lease_expires_at = ?,
		    migrated_count = 25,
		    total_count = 100,
		    last_error = ?
		WHERE id = 1`, frontier, owner, expires, lastError); err != nil {
		t.Fatalf("set failed migration state: %v", err)
	}

	reset, err := ControlGlobalRankMigration(context.Background(), tdb.DB, GlobalRankMigrationReset)
	if err != nil {
		t.Fatalf("reset failed migration: %v", err)
	}
	if reset.Phase != GlobalRankPhaseStable || reset.ActiveBucket != GlobalRankBucket0 {
		t.Fatalf("reset state = %+v, want stable bucket 0", reset)
	}
	if reset.TargetBucket != nil || reset.Direction != nil || reset.Frontier != nil ||
		reset.LeaseOwner != nil || reset.LeaseExpiresAt != nil || reset.LastError != nil {
		t.Fatalf("reset state retains migration markers: %+v", reset)
	}
	if reset.MigratedCount != 0 || reset.TotalCount != 0 {
		t.Fatalf("reset progress = %d/%d, want 0/0", reset.MigratedCount, reset.TotalCount)
	}
	if _, err := ControlGlobalRankMigration(context.Background(), tdb.DB, GlobalRankMigrationReset); !errors.Is(err, ErrGlobalRankMigrationConflict) {
		t.Fatalf("reset from stable error = %v, want state conflict", err)
	}
}

func TestGlobalRankMigrationControlRefusesSplitResetAndRecoversByResume(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	workspaceID := createFracIndexTestWorkspace(t, tdb.DB)
	movingID := int(insertItemWithFracIndex(t, tdb.DB, workspaceID, 1, "0|a0"))
	badID := insertItemWithFracIndex(t, tdb.DB, workspaceID, 2, "0|a20")
	nextID := int(insertItemWithFracIndex(t, tdb.DB, workspaceID, 3, "1|a1"))
	insertItemWithFracIndex(t, tdb.DB, workspaceID, 4, "1|a2")

	if _, err := tdb.DB.Exec(`
		UPDATE global_rank_state
		SET target_bucket = 1,
		    phase = 'failed',
		    direction = 'high_to_low',
		    frontier = '0|a3',
		    migrated_count = 2,
		    total_count = 4,
		    last_error = 'item 2 has invalid active-bucket rank'
		WHERE id = 1`); err != nil {
		t.Fatalf("set half-migrated failure state: %v", err)
	}

	_, resetErr := ControlGlobalRankMigration(context.Background(), tdb.DB, GlobalRankMigrationReset)
	if resetErr == nil {
		if _, err := tdb.DB.Exec("UPDATE items SET frac_index = ? WHERE id = ?", "0|a2", badID); err != nil {
			t.Fatalf("repair malformed rank after unsafe reset: %v", err)
		}
		prevID := int(badID)
		if _, moveErr := MoveItemBetween(tdb.DB, movingID, &prevID, &nextID); moveErr != nil {
			t.Fatalf("unsafe reset left cross-bucket reorder broken: %v", moveErr)
		}
		t.Fatal("reset succeeded for a split item population")
	}
	if !errors.Is(resetErr, ErrGlobalRankMigrationConflict) {
		t.Fatalf("reset error = %v, want migration conflict", resetErr)
	}
	if !strings.Contains(resetErr.Error(), "resume") {
		t.Fatalf("reset error = %q, want actionable resume remedy", resetErr)
	}

	if _, err := ControlGlobalRankMigration(context.Background(), tdb.DB, GlobalRankMigrationResume); !errors.Is(err, ErrGlobalRankMigrationConflict) {
		t.Fatalf("resume with malformed rank error = %v, want migration conflict", err)
	}
	if _, err := tdb.DB.Exec("UPDATE items SET frac_index = ? WHERE id = ?", "0|a2", badID); err != nil {
		t.Fatalf("repair malformed rank: %v", err)
	}
	resumed, err := ControlGlobalRankMigration(context.Background(), tdb.DB, GlobalRankMigrationResume)
	if err != nil {
		t.Fatalf("resume repaired migration: %v", err)
	}
	if resumed.Phase != GlobalRankPhaseMigrating || resumed.LastError != nil {
		t.Fatalf("resumed state = %+v, want migrating without last error", resumed)
	}

	prevID := int(badID)
	newRank, err := MoveItemBetween(tdb.DB, movingID, &prevID, &nextID)
	if err != nil {
		t.Fatalf("cross-bucket reorder after recovery: %v", err)
	}
	parsed, err := ParseGlobalRank(newRank)
	if err != nil {
		t.Fatalf("parse recovered move rank %q: %v", newRank, err)
	}
	if parsed.Bucket != GlobalRankBucket0 || parsed.Fraction <= "a2" || parsed.Fraction >= "a3" {
		t.Fatalf("recovered move rank = %q, want active-bucket rank between a2 and a3", newRank)
	}
}

func TestGlobalRankIntegrityReportsFrontierAndLeaseProblems(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	workspaceID := createFracIndexTestWorkspace(t, tdb.DB)
	for number, rank := range []string{"0|a2", "0|a4", "1|a1", "1|a3", "2|a5"} {
		insertItemWithFracIndex(t, tdb.DB, workspaceID, number+1, rank)
	}
	frontier := "0|a3"
	target := GlobalRankBucket1
	direction := GlobalRankDirectionHighToLow
	owner := "expired-owner"
	expired := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	state := GlobalRankState{
		ActiveBucket:   GlobalRankBucket0,
		TargetBucket:   &target,
		Phase:          GlobalRankPhaseMigrating,
		Direction:      &direction,
		Frontier:       &frontier,
		LeaseOwner:     &owner,
		LeaseExpiresAt: &expired,
		MigratedCount:  2,
		TotalCount:     5,
	}

	integrity, err := NewFracIndexRepository(tdb.DB).GetGlobalRankIntegrity(state, expired.Add(time.Second))
	if err != nil {
		t.Fatalf("global rank integrity: %v", err)
	}
	if integrity.Healthy {
		t.Fatalf("integrity = %+v, want unhealthy", integrity)
	}
	if integrity.FrontierViolationCount != 1 {
		t.Fatalf("frontier violations = %d, want 1 active-bucket violation", integrity.FrontierViolationCount)
	}
	if integrity.UnexpectedBucketCount != 1 || !integrity.LeaseStalled {
		t.Fatalf("integrity = %+v, want unexpected bucket and stalled lease", integrity)
	}
	if integrity.BucketCounts["0"] != 2 || integrity.BucketCounts["1"] != 2 || integrity.BucketCounts["2"] != 1 {
		t.Fatalf("bucket counts = %+v", integrity.BucketCounts)
	}
}

func TestGlobalRankStateValidateRejectsInconsistentMigration(t *testing.T) {
	target := GlobalRankBucket2
	direction := GlobalRankDirectionHighToLow
	tests := []GlobalRankState{
		{ActiveBucket: GlobalRankBucket0, Phase: GlobalRankPhaseMigrating},
		{ActiveBucket: GlobalRankBucket0, TargetBucket: &target, Direction: &direction, Phase: GlobalRankPhaseMigrating},
		{ActiveBucket: GlobalRankBucket0, Phase: GlobalRankPhaseStable, TargetBucket: &target},
		{ActiveBucket: GlobalRankBucket0, Phase: GlobalRankPhaseStable, MigratedCount: 2, TotalCount: 1},
	}
	for i, state := range tests {
		if err := state.Validate(); err == nil {
			t.Errorf("case %d: Validate() succeeded, want error", i)
		}
	}
}

func TestBucketedRankGenerationAndMoveUseActiveBucket(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	workspaceID := createFracIndexTestWorkspace(t, tdb.DB)

	key := generateAndInsertAtEnd(t, tdb.DB, workspaceID, 1)
	if _, err := ParseGlobalRank(key); err != nil {
		t.Fatalf("fresh append rank %q is not bucketed: %v", key, err)
	}

	prevID := int(insertItemWithFracIndex(t, tdb.DB, workspaceID, 2, "0|a1"))
	movingID := int(insertItemWithFracIndex(t, tdb.DB, workspaceID, 3, "0|z1"))
	nextID := int(insertItemWithFracIndex(t, tdb.DB, workspaceID, 4, "0|a3"))
	newKey, err := MoveItemBetween(tdb.DB, movingID, &prevID, &nextID)
	if err != nil {
		t.Fatalf("MoveItemBetween() error = %v", err)
	}
	if newKey != "0|a2" {
		t.Fatalf("MoveItemBetween() = %q, want %q", newKey, "0|a2")
	}
}
