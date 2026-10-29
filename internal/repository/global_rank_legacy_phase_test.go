//go:build test

package repository

import (
	"strings"
	"testing"

	"windshift/internal/database"
)

// deleteGlobalRankStateRow simulates out-of-band corruption of the singleton
// coordination row. The API cannot create this state, so direct SQL is the
// only way to exercise the generators' missing-state refusal contract.
func deleteGlobalRankStateRow(t testing.TB, db database.Database) {
	t.Helper()
	if _, err := db.Exec("DELETE FROM global_rank_state WHERE id = 1"); err != nil {
		t.Fatalf("delete global rank state row: %v", err)
	}
}

func TestGenerateFracIndexForNewItemRefusesMissingGlobalRankState(t *testing.T) {
	db := createFracIndexTestDB(t)
	deleteGlobalRankStateRow(t, db)

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	key, err := GenerateFracIndexForNewItem(tx)
	if err == nil {
		t.Fatalf("GenerateFracIndexForNewItem() = %q, want missing-state refusal", key)
	}
	if !strings.Contains(err.Error(), "load global rank active bucket") {
		t.Fatalf("GenerateFracIndexForNewItem() error = %v, want active-bucket load failure", err)
	}
}

func TestGenerateFracIndexesForBatchRefusesMissingGlobalRankState(t *testing.T) {
	db := createFracIndexTestDB(t)
	deleteGlobalRankStateRow(t, db)

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	keys, err := GenerateFracIndexesForBatch(tx, 3)
	if err == nil {
		t.Fatalf("GenerateFracIndexesForBatch() = %v, want missing-state refusal", keys)
	}
	if !strings.Contains(err.Error(), "load global rank active bucket") {
		t.Fatalf("GenerateFracIndexesForBatch() error = %v, want active-bucket load failure", err)
	}
}

func TestMoveItemBetweenRefusesMissingGlobalRankState(t *testing.T) {
	db := createFracIndexTestDB(t)
	workspaceID := createFracIndexTestWorkspace(t, db)
	movingID := int(insertItemWithFracIndex(t, db, workspaceID, 1, "0|a1"))
	deleteGlobalRankStateRow(t, db)

	key, err := MoveItemBetween(db, movingID, nil, nil)
	if err == nil {
		t.Fatalf("MoveItemBetween() = %q, want missing-state refusal", key)
	}
	if !strings.Contains(err.Error(), "load global rank active bucket") {
		t.Fatalf("MoveItemBetween() error = %v, want active-bucket load failure", err)
	}
}

func TestGenerateFracIndexForNewItemReplacesCorruptMaxRankWithBucketedKey(t *testing.T) {
	db := createFracIndexTestDB(t)
	workspaceID := createFracIndexTestWorkspace(t, db)
	// Corruption fixture: an unbucketed MAX rank cannot be produced by the
	// API, and the generator must not propagate the malformed payload.
	insertItemWithFracIndex(t, db, workspaceID, 1, "a1")

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	key, err := GenerateFracIndexForNewItem(tx)
	if err != nil {
		t.Fatalf("GenerateFracIndexForNewItem() error = %v", err)
	}
	rank, err := ParseGlobalRank(key)
	if err != nil {
		t.Fatalf("generated rank %q is not bucketed: %v", key, err)
	}
	if rank.Bucket != GlobalRankBucket0 {
		t.Fatalf("generated rank %q uses bucket %d, want active bucket 0", key, rank.Bucket)
	}
}

func TestSaveGlobalRankStateRefusesRetiredLegacyPhase(t *testing.T) {
	db := createFracIndexTestDB(t)

	err := database.WithTx(db, func(tx database.Tx) error {
		return SaveGlobalRankState(tx, GlobalRankState{ActiveBucket: GlobalRankBucket0, Phase: GlobalRankPhase("legacy")})
	})
	if err == nil || !strings.Contains(err.Error(), "invalid global rank phase") {
		t.Fatalf("SaveGlobalRankState() error = %v, want invalid-phase refusal", err)
	}
}
