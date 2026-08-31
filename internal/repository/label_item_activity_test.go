//go:build test

package repository

import (
	"errors"
	"testing"

	"windshift/internal/testutils"
)

func TestLabelAssignmentMutationsTouchItemAndEmitChange(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, tdb *testutils.TestDB, itemID, labelID int)
		mutate  func(repo *LabelRepository, itemID, labelID int) error
	}{
		{
			name: "replace labels",
			mutate: func(repo *LabelRepository, itemID, labelID int) error {
				return repo.ReplaceItemLabels(itemID, []int{labelID})
			},
		},
		{
			name: "add label",
			mutate: func(repo *LabelRepository, itemID, labelID int) error {
				return repo.AddItemLabel(itemID, labelID)
			},
		},
		{
			name: "remove label",
			prepare: func(t *testing.T, tdb *testutils.TestDB, itemID, labelID int) {
				t.Helper()
				if _, err := tdb.DB.Exec(
					"INSERT INTO item_labels (item_id, label_id) VALUES (?, ?)",
					itemID, labelID,
				); err != nil {
					t.Fatalf("attach label fixture: %v", err)
				}
			},
			mutate: func(repo *LabelRepository, itemID, labelID int) error {
				return repo.RemoveItemLabel(itemID, labelID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tdb := testutils.CreateTestDB(t, true)
			defer tdb.Close()

			data := setupRepositoryTestData(t, tdb)
			repo := NewLabelRepository(tdb.GetDatabase())
			labelID, _, err := repo.Create("Activity label", "#2563EB")
			if err != nil {
				t.Fatalf("create label: %v", err)
			}
			if tt.prepare != nil {
				tt.prepare(t, tdb, data.ItemID, int(labelID))
			}

			baseActive, baseUpdated := stampSentinel(t, tdb, data.ItemID)
			var watermark int64
			if err := tdb.DB.QueryRow("SELECT COALESCE(MAX(id), 0) FROM item_change_log").Scan(&watermark); err != nil {
				t.Fatalf("read change watermark: %v", err)
			}

			if err := tt.mutate(repo, data.ItemID, int(labelID)); err != nil {
				t.Fatalf("mutate labels: %v", err)
			}

			lastActive, updated := readItemActivityTimes(t, tdb, data.ItemID)
			if updated == baseUpdated {
				t.Error("updated_at was not bumped")
			}
			if lastActive == baseActive {
				t.Error("last_active_at was not bumped")
			}

			var changes int
			if err := tdb.DB.QueryRow(
				"SELECT COUNT(*) FROM item_change_log WHERE item_id = ? AND id > ?",
				data.ItemID, watermark,
			).Scan(&changes); err != nil {
				t.Fatalf("count item changes: %v", err)
			}
			if changes != 1 {
				t.Fatalf("change rows = %d, want 1", changes)
			}
		})
	}
}

func TestNoOpLabelAssignmentDoesNotTouchItem(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, tdb *testutils.TestDB, itemID, labelID int)
		mutate  func(repo *LabelRepository, itemID, labelID int) error
		wantErr error
	}{
		{
			name: "remove missing assignment",
			mutate: func(repo *LabelRepository, itemID, labelID int) error {
				return repo.RemoveItemLabel(itemID, labelID)
			},
		},
		{
			name: "reject duplicate assignment",
			prepare: func(t *testing.T, tdb *testutils.TestDB, itemID, labelID int) {
				t.Helper()
				if _, err := tdb.DB.Exec(
					"INSERT INTO item_labels (item_id, label_id) VALUES (?, ?)",
					itemID, labelID,
				); err != nil {
					t.Fatalf("attach label fixture: %v", err)
				}
			},
			mutate: func(repo *LabelRepository, itemID, labelID int) error {
				return repo.AddItemLabel(itemID, labelID)
			},
			wantErr: ErrDuplicateEntry,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tdb := testutils.CreateTestDB(t, true)
			defer tdb.Close()

			data := setupRepositoryTestData(t, tdb)
			repo := NewLabelRepository(tdb.GetDatabase())
			labelID, _, err := repo.Create("No-op label", "#2563EB")
			if err != nil {
				t.Fatalf("create label: %v", err)
			}
			if tt.prepare != nil {
				tt.prepare(t, tdb, data.ItemID, int(labelID))
			}

			baseActive, baseUpdated := stampSentinel(t, tdb, data.ItemID)
			var watermark int64
			if err := tdb.DB.QueryRow("SELECT COALESCE(MAX(id), 0) FROM item_change_log").Scan(&watermark); err != nil {
				t.Fatalf("read change watermark: %v", err)
			}

			err = tt.mutate(repo, data.ItemID, int(labelID))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("mutation error = %v, want %v", err, tt.wantErr)
			}

			lastActive, updated := readItemActivityTimes(t, tdb, data.ItemID)
			if updated != baseUpdated || lastActive != baseActive {
				t.Fatalf(
					"activity = (%q, %q), want unchanged (%q, %q)",
					lastActive, updated, baseActive, baseUpdated,
				)
			}

			var changes int
			if err := tdb.DB.QueryRow(
				"SELECT COUNT(*) FROM item_change_log WHERE item_id = ? AND id > ?",
				data.ItemID, watermark,
			).Scan(&changes); err != nil {
				t.Fatalf("count item changes: %v", err)
			}
			if changes != 0 {
				t.Fatalf("change rows = %d, want 0", changes)
			}
		})
	}
}

func TestLabelCatalogMutationsInvalidateAssignedItemsWithoutActivity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(repo *LabelRepository, labelID int) error
	}{
		{
			name: "update label",
			mutate: func(repo *LabelRepository, labelID int) error {
				return repo.Update(labelID, "Renamed label", "#DC2626")
			},
		},
		{
			name: "delete label",
			mutate: func(repo *LabelRepository, labelID int) error {
				return repo.Delete(labelID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tdb := testutils.CreateTestDB(t, true)
			defer tdb.Close()

			data := setupRepositoryTestData(t, tdb)
			repo := NewLabelRepository(tdb.GetDatabase())
			labelID, _, err := repo.Create("Catalog label", "#2563EB")
			if err != nil {
				t.Fatalf("create label: %v", err)
			}
			if _, err := tdb.DB.Exec(
				"INSERT INTO item_labels (item_id, label_id) VALUES (?, ?)",
				data.ItemID, labelID,
			); err != nil {
				t.Fatalf("attach label fixture: %v", err)
			}

			baseActive, baseUpdated := stampSentinel(t, tdb, data.ItemID)
			var watermark int64
			if err := tdb.DB.QueryRow("SELECT COALESCE(MAX(id), 0) FROM item_change_log").Scan(&watermark); err != nil {
				t.Fatalf("read change watermark: %v", err)
			}

			if err := tt.mutate(repo, int(labelID)); err != nil {
				t.Fatalf("mutate label catalog: %v", err)
			}

			lastActive, updated := readItemActivityTimes(t, tdb, data.ItemID)
			if updated == baseUpdated {
				t.Error("updated_at was not bumped")
			}
			if lastActive != baseActive {
				t.Errorf("last_active_at = %q, want unchanged %q", lastActive, baseActive)
			}

			var changes int
			if err := tdb.DB.QueryRow(
				"SELECT COUNT(*) FROM item_change_log WHERE item_id = ? AND id > ?",
				data.ItemID, watermark,
			).Scan(&changes); err != nil {
				t.Fatalf("count item changes: %v", err)
			}
			if changes != 1 {
				t.Fatalf("change rows = %d, want 1", changes)
			}
		})
	}
}
