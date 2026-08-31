//go:build test

package services

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func assetTaxonomyInsertID(t *testing.T, db database.Database, query string, args ...interface{}) int {
	t.Helper()
	var id int
	if err := db.QueryRow(query+" RETURNING id", args...).Scan(&id); err != nil {
		t.Fatalf("insert fixture: %v", err)
	}
	return id
}

type assetTaxonomyFixture struct {
	setID, otherSetID           int
	typeID, otherTypeID         int
	categoryID, otherCategoryID int
	statusID, otherStatusID     int
}

func seedAssetTaxonomyFixture(t *testing.T, db database.Database) assetTaxonomyFixture {
	t.Helper()
	f := assetTaxonomyFixture{}
	f.setID = assetTaxonomyInsertID(t, db, "INSERT INTO asset_management_sets (name) VALUES (?)", "Taxonomy set")
	f.otherSetID = assetTaxonomyInsertID(t, db, "INSERT INTO asset_management_sets (name) VALUES (?)", "Other taxonomy set")
	f.typeID = assetTaxonomyInsertID(t, db, "INSERT INTO asset_types (set_id, name) VALUES (?, ?)", f.setID, "Server")
	f.otherTypeID = assetTaxonomyInsertID(t, db, "INSERT INTO asset_types (set_id, name) VALUES (?, ?)", f.otherSetID, "Other server")
	f.categoryID = assetTaxonomyInsertID(t, db, "INSERT INTO asset_categories (set_id, name) VALUES (?, ?)", f.setID, "Production")
	f.otherCategoryID = assetTaxonomyInsertID(t, db, "INSERT INTO asset_categories (set_id, name) VALUES (?, ?)", f.otherSetID, "Other production")
	f.statusID = assetTaxonomyInsertID(t, db, "INSERT INTO asset_statuses (set_id, name) VALUES (?, ?)", f.setID, "Active")
	f.otherStatusID = assetTaxonomyInsertID(t, db, "INSERT INTO asset_statuses (set_id, name) VALUES (?, ?)", f.otherSetID, "Other active")
	return f
}

func TestActionSaveValidationRejectsCrossSetAssetTaxonomy(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()
	f := seedAssetTaxonomyFixture(t, db)
	service := NewAssetService(db, repository.NewAssetRepository(db))

	node := func(config models.CreateAssetNodeConfig) models.ActionNode {
		raw, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("marshal config: %v", err)
		}
		return models.ActionNode{NodeType: models.ActionNodeCreateAsset, NodeConfig: string(raw)}
	}
	valid := models.CreateAssetNodeConfig{
		AssetSetID:  f.setID,
		AssetTypeID: f.typeID,
		CategoryID:  &f.categoryID,
		StatusID:    &f.statusID,
		Title:       "Server",
	}
	if err := service.ValidateActionTaxonomyReferences([]models.ActionNode{node(valid)}); err != nil {
		t.Fatalf("valid taxonomy rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*models.CreateAssetNodeConfig)
		field  string
	}{
		{"type", func(c *models.CreateAssetNodeConfig) { c.AssetTypeID = f.otherTypeID }, "asset_type_id"},
		{"category", func(c *models.CreateAssetNodeConfig) { c.CategoryID = &f.otherCategoryID }, "category_id"},
		{"status", func(c *models.CreateAssetNodeConfig) { c.StatusID = &f.otherStatusID }, "status_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := valid
			tt.mutate(&config)
			err := service.ValidateActionTaxonomyReferences([]models.ActionNode{node(config)})
			if err == nil || !strings.Contains(err.Error(), tt.field) || !strings.Contains(err.Error(), "does not belong") {
				t.Fatalf("validation error = %v, want %s ownership rejection", err, tt.field)
			}
		})
	}
}

func TestAssetActionSaveAndExecutionRejectCrossSetStatus(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()
	f := seedAssetTaxonomyFixture(t, db)
	service := NewAssetActionService(db, DefaultActionServiceConfig(), nil)
	defer service.Stop()

	trigger := fmt.Sprintf(`{"asset_type_id":%d,"to_status_id":%d}`, f.typeID, f.otherStatusID)
	if err := service.ValidateTaxonomyReferences(f.setID, trigger, nil); err == nil || !strings.Contains(err.Error(), "to_status_id") {
		t.Fatalf("cross-set trigger validation error = %v", err)
	}
	node := models.AssetActionNode{
		NodeType:   models.AssetNodeSetStatus,
		NodeConfig: fmt.Sprintf(`{"status_id":%d}`, f.otherStatusID),
	}
	if err := service.ValidateTaxonomyReferences(f.setID, "", []models.AssetActionNode{node}); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("cross-set node validation error = %v", err)
	}

	assetID := assetTaxonomyInsertID(t, db,
		"INSERT INTO assets (set_id, asset_type_id, status_id, title) VALUES (?, ?, ?, ?)",
		f.setID, f.typeID, f.statusID, "Server")
	ctx := &models.AssetActionExecutionContext{
		Event: &models.AssetActionEvent{SetID: f.setID, AssetID: assetID},
	}
	err := service.executeSetStatus(&node, ctx, &models.StepResult{})
	if err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("execution error = %v, want ownership rejection", err)
	}
	var statusID int
	if err := db.QueryRow("SELECT status_id FROM assets WHERE id = ?", assetID).Scan(&statusID); err != nil {
		t.Fatalf("read status after rejection: %v", err)
	}
	if statusID != f.statusID {
		t.Fatalf("status after rejected execution = %d, want %d", statusID, f.statusID)
	}
}

func TestAssetTypeChangePrunesIncompatibleValuesAndRequiresNewFields(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()
	repo := repository.NewAssetRepository(db)
	service := NewAssetService(db, repo)

	setID := assetTaxonomyInsertID(t, db, "INSERT INTO asset_management_sets (name) VALUES (?)", "Type change set")
	oldTypeID := assetTaxonomyInsertID(t, db, "INSERT INTO asset_types (set_id, name) VALUES (?, ?)", setID, "Old type")
	newTypeID := assetTaxonomyInsertID(t, db, "INSERT INTO asset_types (set_id, name) VALUES (?, ?)", setID, "New type")
	oldOnlyID := assetTaxonomyInsertID(t, db,
		"INSERT INTO custom_field_definitions (name, field_type) VALUES (?, ?)", "Old only", "text")
	sharedID := assetTaxonomyInsertID(t, db,
		"INSERT INTO custom_field_definitions (name, field_type) VALUES (?, ?)", "Shared", "text")
	requiredID := assetTaxonomyInsertID(t, db,
		"INSERT INTO custom_field_definitions (name, field_type) VALUES (?, ?)", "Required new", "text")
	if err := repo.ReplaceAssetTypeFields(oldTypeID, []repository.AssetTypeFieldAssignment{
		{CustomFieldID: oldOnlyID},
		{CustomFieldID: sharedID},
	}); err != nil {
		t.Fatalf("assign old type fields: %v", err)
	}
	if err := repo.ReplaceAssetTypeFields(newTypeID, []repository.AssetTypeFieldAssignment{
		{CustomFieldID: sharedID},
		{CustomFieldID: requiredID, IsRequired: true},
	}); err != nil {
		t.Fatalf("assign new type fields: %v", err)
	}
	stored := fmt.Sprintf(`{"%d":"legacy","%d":"keep"}`, oldOnlyID, sharedID)
	assetID := assetTaxonomyInsertID(t, db,
		"INSERT INTO assets (set_id, asset_type_id, title, custom_field_values) VALUES (?, ?, ?, ?)",
		setID, oldTypeID, "Server", stored)
	snap, err := repo.GetAssetUpdateSnapshot(assetID)
	if err != nil {
		t.Fatalf("GetAssetUpdateSnapshot: %v", err)
	}

	_, err = service.UpdateAsset(AuditActor{}, assetID, *snap, repository.UpdateAssetInput{
		AssetTypeID: newTypeID,
		Title:       "Server",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "Required new") {
		t.Fatalf("missing required field error = %v", err)
	}
	var unchangedType int
	if err := db.QueryRow("SELECT asset_type_id FROM assets WHERE id = ?", assetID).Scan(&unchangedType); err != nil {
		t.Fatalf("read unchanged type: %v", err)
	}
	if unchangedType != oldTypeID {
		t.Fatalf("type changed after rejected update = %d, want %d", unchangedType, oldTypeID)
	}

	values := map[string]interface{}{
		fmt.Sprintf("%d", oldOnlyID):  "drop",
		fmt.Sprintf("%d", sharedID):   "keep",
		fmt.Sprintf("%d", requiredID): "provided",
	}
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal values: %v", err)
	}
	encoded := string(raw)
	updated, err := service.UpdateAsset(AuditActor{}, assetID, *snap, repository.UpdateAssetInput{
		AssetTypeID:           newTypeID,
		Title:                 "Server",
		CustomFieldValuesJSON: &encoded,
	}, values)
	if err != nil {
		t.Fatalf("UpdateAsset: %v", err)
	}
	if _, ok := updated.CustomFieldValues[fmt.Sprintf("%d", oldOnlyID)]; ok {
		t.Fatalf("incompatible field survived type change: %v", updated.CustomFieldValues)
	}
	if updated.CustomFieldValues[fmt.Sprintf("%d", sharedID)] != "keep" ||
		updated.CustomFieldValues[fmt.Sprintf("%d", requiredID)] != "provided" {
		t.Fatalf("compatible/new fields = %v", updated.CustomFieldValues)
	}
}
