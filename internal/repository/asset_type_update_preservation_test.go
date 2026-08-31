//go:build test

package repository

import (
	"testing"

	"windshift/internal/testutils"
)

func TestUpdateAssetType_PreservesOmittedDisplayOrder(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	db := tdb.GetDatabase()
	repo := NewAssetRepository(db)
	setID := assetLifecycleInsertID(t, db, "INSERT INTO asset_management_sets (name) VALUES (?)", "Ordered assets")
	typeID := assetLifecycleInsertID(t, db,
		"INSERT INTO asset_types (set_id, name, display_order) VALUES (?, ?, ?)",
		setID, "Server", 7)

	if err := repo.UpdateAssetType(typeID, AssetTypeUpdate{
		Name:        "Compute server",
		Description: "Updated description",
		Icon:        "server",
		Color:       "#123456",
	}); err != nil {
		t.Fatalf("UpdateAssetType: %v", err)
	}

	var displayOrder int
	if err := db.QueryRow("SELECT display_order FROM asset_types WHERE id = ?", typeID).Scan(&displayOrder); err != nil {
		t.Fatalf("read display order: %v", err)
	}
	if displayOrder != 7 {
		t.Errorf("display_order = %d, want 7", displayOrder)
	}

	zero := 0
	if err := repo.UpdateAssetType(typeID, AssetTypeUpdate{
		Name:         "Compute server",
		Description:  "Updated description",
		Icon:         "server",
		Color:        "#123456",
		DisplayOrder: &zero,
	}); err != nil {
		t.Fatalf("UpdateAssetType explicit zero: %v", err)
	}
	if err := db.QueryRow("SELECT display_order FROM asset_types WHERE id = ?", typeID).Scan(&displayOrder); err != nil {
		t.Fatalf("read explicit display order: %v", err)
	}
	if displayOrder != 0 {
		t.Errorf("explicit display_order = %d, want 0", displayOrder)
	}
}
