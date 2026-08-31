//go:build test

package services

import (
	"encoding/json"
	"fmt"
	"testing"

	"windshift/internal/assetevents"
	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestUpdateAssetActionUsesSharedAssetMutationPipeline(t *testing.T) {
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
		"asset-update@example.test", "asset-update", "Asset", "Updater",
	)
	workspaceID := insertID("INSERT INTO workspaces (name, key) VALUES (?, ?)", "Asset action", "AAC")
	setID := insertID("INSERT INTO asset_management_sets (name, created_by) VALUES (?, ?)", "Action assets", actorID)
	typeID := insertID("INSERT INTO asset_types (set_id, name) VALUES (?, ?)", setID, "Server")
	fieldID := insertID("INSERT INTO custom_field_definitions (name, field_type) VALUES (?, ?)", "Rack", "text")
	if _, err := db.ExecWrite(
		"INSERT INTO asset_type_fields (asset_type_id, custom_field_id, is_required) VALUES (?, ?, false)",
		typeID, fieldID,
	); err != nil {
		t.Fatalf("link asset field: %v", err)
	}
	assetID := insertID(
		"INSERT INTO assets (set_id, asset_type_id, title, custom_field_values, created_by) VALUES (?, ?, ?, ?, ?)",
		setID, typeID, "Server", fmt.Sprintf(`{"%d":"rack-old"}`, fieldID), actorID,
	)
	itemID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID:           workspaceID,
		Title:                 "Server item",
		CustomFieldValuesJSON: fmt.Sprintf(`{"asset_ref":{"id":%d}}`, assetID),
	})
	if err != nil {
		t.Fatalf("insert fixture item: %v", err)
	}
	itemID := int(itemID64)

	assetService := NewAssetService(db, repository.NewAssetRepository(db))
	actionService := &ActionService{itemRepo: repository.NewItemRepository(db)}
	actionService.SetAssetNodeServices(
		assetService,
		selectiveAssetPermissionChecker{allowed: map[int]bool{actorID: true}},
	)

	config := fmt.Sprintf(`{
		"source_field_id":"asset_ref",
		"asset_set_id":%d,
		"asset_type_id":%d,
		"field_mappings":[{
			"source_type":"literal",
			"source_value":"rack-new",
			"target_field_id":"%d"
		}]
	}`, setID, typeID, fieldID)
	step := &models.StepResult{}
	err = actionService.executeNode(
		&models.ActionNode{NodeType: models.ActionNodeUpdateAsset, NodeConfig: config},
		&models.ExecutionContext{
			Event: &models.ActionEvent{
				WorkspaceID:  workspaceID,
				ItemID:       itemID,
				CascadeDepth: 4,
			},
			EffectiveActorID: actorID,
			Variables:        map[string]interface{}{},
			ChainID:          "asset-update-chain",
		},
		step,
	)
	if err != nil {
		t.Fatalf("execute update_asset: %v", err)
	}

	var rawFields string
	if err := db.QueryRow("SELECT custom_field_values FROM assets WHERE id = ?", assetID).Scan(&rawFields); err != nil {
		t.Fatalf("load updated asset: %v", err)
	}
	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(rawFields), &fields); err != nil {
		t.Fatalf("decode custom fields: %v", err)
	}
	fieldKey := fmt.Sprintf("%d", fieldID)
	if fields[fieldKey] != "rack-new" {
		t.Fatalf("custom field %s = %#v, want rack-new", fieldKey, fields[fieldKey])
	}

	var auditUserID int
	if err := db.QueryRow(`
		SELECT user_id FROM audit_logs
		WHERE action_type = ? AND resource_type = ?
		ORDER BY id DESC LIMIT 1
	`, logger.ActionAssetUpdate, logger.ResourceAsset).Scan(&auditUserID); err != nil {
		t.Fatalf("load asset update audit: %v", err)
	}
	if auditUserID != actorID {
		t.Fatalf("audit user_id = %d, want %d", auditUserID, actorID)
	}

	var actorRef, correlationID, sourceRef, payloadJSON string
	if err := db.QueryRow(`
		SELECT actor_ref, correlation_id, source_ref, payload
		FROM domain_events WHERE event_type = ? AND aggregate_id = ?
		ORDER BY id DESC LIMIT 1
	`, assetevents.Updated, assetID).Scan(&actorRef, &correlationID, &sourceRef, &payloadJSON); err != nil {
		t.Fatalf("load asset update fact: %v", err)
	}
	var payload assetevents.UpdatedV1
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("decode asset update fact: %v", err)
	}
	if payload.Asset.ID != assetID || actorRef != fmt.Sprint(actorID) {
		t.Fatalf("asset update fact = actor:%s payload:%#v", actorRef, payload)
	}
	if payload.Automation == nil || !payload.Automation.TriggeredByAction || correlationID != "asset-update-chain" || payload.Automation.CascadeDepth != 5 || sourceRef != "workspace" {
		t.Fatalf("asset event context = correlation:%q source:%q automation:%#v", correlationID, sourceRef, payload.Automation)
	}
	if step.Output["asset_id"] != assetID || step.Output["mapping_count"] != 1 {
		t.Fatalf("step output = %#v", step.Output)
	}
}
