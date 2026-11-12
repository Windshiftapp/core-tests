//go:build test

package services

import (
	"testing"

	"windshift/internal/testutils"
)

func TestCreateItemHonorsPinnedWorkspaceItemNumber(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	pinned := 30
	var outcome PinnedItemNumberOutcome
	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID:            data.WorkspaceID,
		Title:                  "Pinned item",
		PinnedItemNumber:       &pinned,
		PinnedItemNumberResult: &outcome,
	})
	if err != nil {
		t.Fatalf("create pinned item: %v", err)
	}
	if got := itemNumberByID(t, db, int(itemID)); got != 30 {
		t.Fatalf("pinned item number = %d, want 30", got)
	}
	if outcome != (PinnedItemNumberOutcome{Requested: 30, Assigned: 30}) {
		t.Fatalf("outcome = %+v, want requested/assigned 30 without fallback", outcome)
	}

	nextID := createKeyNumberItem(t, db, data.WorkspaceID, "After pin", nil)
	if got := itemNumberByID(t, db, nextID); got != 31 {
		t.Fatalf("next item number = %d, want 31 (MAX+1 above the pinned number)", got)
	}
}

func TestCreateItemFallsBackWhenPinnedWorkspaceItemNumberTaken(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	pinned := 5
	existingID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID:      data.WorkspaceID,
		Title:            "Existing five",
		PinnedItemNumber: &pinned,
	})
	if err != nil {
		t.Fatalf("create existing pinned item: %v", err)
	}

	var outcome PinnedItemNumberOutcome
	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID:            data.WorkspaceID,
		Title:                  "Collides with five",
		PinnedItemNumber:       &pinned,
		PinnedItemNumberResult: &outcome,
	})
	if err != nil {
		t.Fatalf("create colliding pinned item: %v", err)
	}
	if got := itemNumberByID(t, db, int(itemID)); got != 6 {
		t.Fatalf("colliding item number = %d, want 6", got)
	}
	if outcome != (PinnedItemNumberOutcome{Requested: 5, Assigned: 6, FellBack: true}) {
		t.Fatalf("outcome = %+v, want requested 5 assigned 6 with fallback", outcome)
	}
	if got := itemNumberByID(t, db, int(existingID)); got != 5 {
		t.Fatalf("existing item number = %d, want unchanged 5", got)
	}
}

func TestCreateItemFallsBackWhenPinnedWorkspaceItemNumberReserved(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	// A deleted item retires its number, so the pinned number is no longer free
	// even though no item holds it.
	retired := 9
	retiredID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID:      data.WorkspaceID,
		Title:            "Retired nine",
		PinnedItemNumber: &retired,
	})
	if err != nil {
		t.Fatalf("create retired item: %v", err)
	}
	if err := NewItemCRUDService(db).DeleteSingle(int(retiredID)); err != nil {
		t.Fatalf("delete retired item: %v", err)
	}
	if got := keyReservationCount(t, db, data.WorkspaceID, 9); got != 1 {
		t.Fatalf("reservation rows for 9 = %d, want 1", got)
	}

	var outcome PinnedItemNumberOutcome
	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID:            data.WorkspaceID,
		Title:                  "Reuses retired nine",
		PinnedItemNumber:       &retired,
		PinnedItemNumberResult: &outcome,
	})
	if err != nil {
		t.Fatalf("create item pinned to reserved number: %v", err)
	}
	if got := itemNumberByID(t, db, int(itemID)); got != 10 {
		t.Fatalf("item number = %d, want 10", got)
	}
	if outcome != (PinnedItemNumberOutcome{Requested: 9, Assigned: 10, FellBack: true}) {
		t.Fatalf("outcome = %+v, want requested 9 assigned 10 with fallback", outcome)
	}
}
