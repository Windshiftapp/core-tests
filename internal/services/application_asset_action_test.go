//go:build test

package services

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// TestCreateAssetActionMapsApplicationCustomFields pins that a create_asset
// automation node can populate a typed "application" object from literal
// mappings: a required text field, a user field, a single-select field, a
// boolean field, and the set's built-in status. Literals arrive as strings,
// so this also pins that the create pipeline coerces them to the field's
// declared type the same way update_asset already does.
func TestCreateAssetActionMapsApplicationCustomFields(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	insertID := func(query string, args ...interface{}) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query+" RETURNING id", args...).Scan(&id); err != nil {
			t.Fatalf("insert fixture: %v", err)
		}
		return id
	}

	actorID := insertID(
		"INSERT INTO users (email, username, first_name, last_name) VALUES (?, ?, ?, ?)",
		"app-asset@example.test", "app-asset", "App", "Owner",
	)
	setID := insertID("INSERT INTO asset_management_sets (name, created_by) VALUES (?, ?)", "Applications", actorID)
	typeID := insertID("INSERT INTO asset_types (set_id, name) VALUES (?, ?)", setID, "Application")
	statusID := insertID("INSERT INTO asset_statuses (set_id, name, is_default) VALUES (?, ?, true)", setID, "In use")

	nameFieldID := insertID("INSERT INTO custom_field_definitions (name, field_type) VALUES (?, ?)", "Name", "text")
	ownerFieldID := insertID("INSERT INTO custom_field_definitions (name, field_type) VALUES (?, ?)", "Owner", "user")
	typeFieldID := insertID(
		"INSERT INTO custom_field_definitions (name, field_type, options) VALUES (?, ?, ?)",
		"Type", "select",
		`{"next_id":4,"items":[{"id":1,"label":"Desktop"},{"id":2,"label":"Web"},{"id":3,"label":"Mobile"}]}`,
	)
	personalDataFieldID := insertID("INSERT INTO custom_field_definitions (name, field_type) VALUES (?, ?)", "Stores personal data", "boolean")

	if _, err := db.ExecWrite(`
		INSERT INTO asset_type_fields (asset_type_id, custom_field_id, is_required, display_order)
		VALUES (?, ?, true, 0), (?, ?, false, 1), (?, ?, false, 2), (?, ?, false, 3)
	`, typeID, nameFieldID, typeID, ownerFieldID, typeID, typeFieldID, typeID, personalDataFieldID); err != nil {
		t.Fatalf("link asset type fields: %v", err)
	}

	assetService := NewAssetService(db, repository.NewAssetRepository(db))
	actionService := &ActionService{itemRepo: repository.NewItemRepository(db)}
	actionService.SetAssetNodeServices(assetService, selectiveAssetPermissionChecker{allowed: map[int]bool{actorID: true}})

	config := fmt.Sprintf(`{
		"asset_set_id":%d,
		"asset_type_id":%d,
		"status_id":%d,
		"title":"Inventory App",
		"field_mappings":[
			{"source_type":"literal","source_value":"Inventory App","target_field_id":"%d"},
			{"source_type":"literal","source_value":"%s","target_field_id":"%d"},
			{"source_type":"literal","source_value":"2","target_field_id":"%d"},
			{"source_type":"literal","source_value":"true","target_field_id":"%d"}
		]
	}`,
		setID, typeID, statusID,
		nameFieldID,
		strconv.Itoa(actorID), ownerFieldID,
		typeFieldID,
		personalDataFieldID,
	)

	step := &models.StepResult{}
	err := actionService.executeNode(
		&models.ActionNode{NodeType: models.ActionNodeCreateAsset, NodeConfig: config},
		&models.ExecutionContext{
			Event:            &models.ActionEvent{},
			EffectiveActorID: actorID,
			Variables:        map[string]interface{}{},
			ChainID:          "application-asset-chain",
		},
		step,
	)
	if err != nil {
		t.Fatalf("execute create_asset: %v", err)
	}
	if step.Output["mapping_count"] != 4 {
		t.Fatalf("mapping_count = %#v, want 4", step.Output["mapping_count"])
	}

	var gotTitle string
	var gotTypeID, gotStatusID, gotCreatorID int
	var rawFields string
	if err := db.QueryRow(
		"SELECT title, asset_type_id, status_id, created_by, custom_field_values FROM assets",
	).Scan(&gotTitle, &gotTypeID, &gotStatusID, &gotCreatorID, &rawFields); err != nil {
		t.Fatalf("load created asset: %v", err)
	}
	if gotTitle != "Inventory App" || gotTypeID != typeID || gotStatusID != statusID || gotCreatorID != actorID {
		t.Fatalf("asset = title:%q type:%d status:%d creator:%d", gotTitle, gotTypeID, gotStatusID, gotCreatorID)
	}

	var fields map[string]any
	if err := json.Unmarshal([]byte(rawFields), &fields); err != nil {
		t.Fatalf("decode custom_field_values %q: %v", rawFields, err)
	}
	if got := fields[fmt.Sprintf("%d", nameFieldID)]; got != "Inventory App" {
		t.Fatalf("name field = %#v, want %q", got, "Inventory App")
	}
	if got := fields[fmt.Sprintf("%d", ownerFieldID)]; got != float64(actorID) {
		t.Fatalf("owner field = %#v, want user id %d", got, actorID)
	}
	if got := fields[fmt.Sprintf("%d", typeFieldID)]; got != "2" {
		// Select values keep their literal form; labels and numeric option
		// ids are both accepted, matching update_asset and CSV import.
		t.Fatalf("type field = %#v, want literal option \"2\"", got)
	}
	if got := fields[fmt.Sprintf("%d", personalDataFieldID)]; got != true {
		t.Fatalf("stores personal data field = %#v, want true", got)
	}
}
