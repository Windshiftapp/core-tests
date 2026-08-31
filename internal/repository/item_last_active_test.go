//go:build test

package repository

import (
	"testing"
	"time"

	"windshift/internal/testutils"
)

// sentinelTime is a fixed past timestamp we stamp onto an item so we can detect
// which of updated_at / last_active_at a write path bumps. (The SQLite driver
// re-serializes this on read, so tests compare against the captured baseline
// rather than this literal.)
const sentinelTime = "2000-01-01 00:00:00"

// readItemActivityTimes returns (last_active_at, updated_at) as raw strings.
func readItemActivityTimes(t *testing.T, tdb *testutils.TestDB, itemID int) (string, string) {
	t.Helper()
	var lastActive, updated string
	if err := tdb.DB.QueryRow(
		"SELECT last_active_at, updated_at FROM items WHERE id = ?", itemID,
	).Scan(&lastActive, &updated); err != nil {
		t.Fatalf("failed to read item activity times: %v", err)
	}
	return lastActive, updated
}

// stampSentinel resets both timestamps to the sentinel and returns the values as
// stored (last_active_at, updated_at), so callers compare against the actual
// baseline regardless of how the driver serializes datetimes.
func stampSentinel(t *testing.T, tdb *testutils.TestDB, itemID int) (string, string) {
	t.Helper()
	if _, err := tdb.DB.Exec(
		"UPDATE items SET last_active_at = ?, updated_at = ? WHERE id = ?",
		sentinelTime, sentinelTime, itemID,
	); err != nil {
		t.Fatalf("failed to stamp sentinel: %v", err)
	}
	return readItemActivityTimes(t, tdb, itemID)
}

func TestItemLastActiveAt(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	repo := NewItemRepository(tdb.GetDatabase())
	testData := setupRepositoryTestData(t, tdb)

	t.Run("TouchActivity bumps last_active_at but not updated_at", func(t *testing.T) {
		baseActive, baseUpdated := stampSentinel(t, tdb, testData.ItemID)

		if err := repo.TouchActivity(tdb.GetDatabase(), testData.ItemID, time.Now()); err != nil {
			t.Fatalf("TouchActivity failed: %v", err)
		}

		lastActive, updated := readItemActivityTimes(t, tdb, testData.ItemID)
		if lastActive == baseActive {
			t.Error("expected last_active_at to be bumped, but it was unchanged")
		}
		if updated != baseUpdated {
			t.Errorf("expected updated_at to be unchanged (%q), got %q", baseUpdated, updated)
		}
	})

	t.Run("UpdateFields bumps both updated_at and last_active_at", func(t *testing.T) {
		baseActive, baseUpdated := stampSentinel(t, tdb, testData.ItemID)

		tx, err := tdb.GetDatabase().Begin()
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		if err := repo.UpdateFields(tx, testData.ItemID, map[string]interface{}{"title": "Edited"}); err != nil {
			tx.Rollback()
			t.Fatalf("UpdateFields failed: %v", err)
		}
		tx.Commit()

		lastActive, updated := readItemActivityTimes(t, tdb, testData.ItemID)
		if updated == baseUpdated {
			t.Error("expected updated_at to be bumped on edit, but it was unchanged")
		}
		if lastActive == baseActive {
			t.Error("expected last_active_at to be bumped on edit, but it was unchanged")
		}
	})

	t.Run("MoveItemBetween (rank reorder) does NOT bump last_active_at", func(t *testing.T) {
		baseActive, baseUpdated := stampSentinel(t, tdb, testData.ItemID)

		if _, err := MoveItemBetween(tdb.GetDatabase(), testData.ItemID, nil, nil); err != nil {
			t.Fatalf("MoveItemBetween failed: %v", err)
		}

		lastActive, updated := readItemActivityTimes(t, tdb, testData.ItemID)
		if lastActive != baseActive {
			t.Errorf("expected last_active_at to be unchanged by reorder (%q), got %q", baseActive, lastActive)
		}
		if updated != baseUpdated {
			t.Errorf("expected updated_at to be unchanged by reorder (%q), got %q", baseUpdated, updated)
		}
	})
}
