package repository

import (
	"path/filepath"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

func TestAssetReportVisibilityJSONFailsClosedWhenMalformed(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	var channelID, assetSetID int
	if err := tdb.DB.QueryRow(`INSERT INTO channels (name, type, direction, status, config) VALUES ('Portal', 'portal', 'inbound', 'enabled', '{}') RETURNING id`).Scan(&channelID); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := tdb.DB.QueryRow(`INSERT INTO asset_management_sets (name) VALUES ('Assets') RETURNING id`).Scan(&assetSetID); err != nil {
		t.Fatalf("create asset set: %v", err)
	}

	repo := NewAssetReportRepository(tdb.GetDatabase())
	id, err := repo.Create(&models.AssetReport{
		ChannelID:  channelID,
		AssetSetID: assetSetID,
		Name:       "Malformed visibility",
		CQLQuery:   "",
		RunMode:    "direct",
		IsActive:   true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Direct corruption is required because the API and repository encoder only write valid arrays.
	if _, err := tdb.DB.ExecWrite(`UPDATE asset_reports SET visibility_group_ids = ? WHERE id = ?`, `{"unexpected":true}`, id); err != nil {
		t.Fatalf("corrupt visibility JSON: %v", err)
	}
	if _, err := repo.GetByID(int(id)); err == nil {
		t.Fatal("malformed visibility JSON was decoded as unrestricted")
	}
}

func TestAssetReportUpdatePersistsBindingAndClearsStaleFields(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "asset-report.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	var channelID, assetSetID, firstItemTypeID, secondItemTypeID, firstWorkspaceID, secondWorkspaceID int
	fixtures := []struct {
		query  string
		target *int
	}{
		{`INSERT INTO channels (name, type, direction, status, config) VALUES ('Portal', 'portal', 'inbound', 'enabled', '{}') RETURNING id`, &channelID},
		{`INSERT INTO asset_management_sets (name) VALUES ('Assets') RETURNING id`, &assetSetID},
		{`INSERT INTO item_types (name) VALUES ('First Asset Request') RETURNING id`, &firstItemTypeID},
		{`INSERT INTO item_types (name) VALUES ('Second Asset Request') RETURNING id`, &secondItemTypeID},
		{`INSERT INTO workspaces (name, key) VALUES ('First', 'ARF1') RETURNING id`, &firstWorkspaceID},
		{`INSERT INTO workspaces (name, key) VALUES ('Second', 'ARF2') RETURNING id`, &secondWorkspaceID},
	}
	for _, fixture := range fixtures {
		if err := db.QueryRow(fixture.query).Scan(fixture.target); err != nil {
			t.Fatalf("fixture insert %q: %v", fixture.query, err)
		}
	}

	repo := NewAssetReportRepository(db)
	report := models.AssetReport{
		ChannelID:   channelID,
		AssetSetID:  assetSetID,
		Name:        "Hardware lookup",
		CQLQuery:    `asset_tag = "${tag}"`,
		RunMode:     "form",
		ItemTypeID:  &firstItemTypeID,
		WorkspaceID: &firstWorkspaceID,
		IsActive:    true,
	}
	id, err := repo.Create(&report)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.ReplaceFields(int(id), []models.AssetReportField{{FieldIdentifier: "title", FieldType: "default"}}); err != nil {
		t.Fatalf("ReplaceFields: %v", err)
	}

	report.ItemTypeID = &secondItemTypeID
	report.WorkspaceID = &secondWorkspaceID
	if err := repo.Update(int(id), channelID, &report); err != nil {
		t.Fatalf("Update binding: %v", err)
	}
	updated, err := repo.GetByID(int(id))
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.ItemTypeID == nil || *updated.ItemTypeID != secondItemTypeID || updated.WorkspaceID == nil || *updated.WorkspaceID != secondWorkspaceID {
		t.Fatalf("binding was not persisted: item_type=%v workspace=%v", updated.ItemTypeID, updated.WorkspaceID)
	}
	fields, err := repo.ListFields(int(id))
	if err != nil {
		t.Fatalf("ListFields: %v", err)
	}
	if len(fields) != 0 {
		t.Fatalf("binding change retained %d stale field(s)", len(fields))
	}

	if err := repo.ReplaceFields(int(id), []models.AssetReportField{{FieldIdentifier: "description", FieldType: "default"}}); err != nil {
		t.Fatalf("ReplaceFields after binding change: %v", err)
	}
	report.Name = "Renamed hardware lookup"
	if err := repo.Update(int(id), channelID, &report); err != nil {
		t.Fatalf("Update without binding change: %v", err)
	}
	fields, err = repo.ListFields(int(id))
	if err != nil {
		t.Fatalf("ListFields after rename: %v", err)
	}
	if len(fields) != 1 || fields[0].FieldIdentifier != "description" {
		t.Fatalf("non-binding update unexpectedly cleared fields: %#v", fields)
	}
}
