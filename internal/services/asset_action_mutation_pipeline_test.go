//go:build test

package services

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"windshift/internal/assetevents"
	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestAssetActionSetFieldUsesCanonicalMutationPipeline(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	db := tdb.GetDatabase()

	insertID := func(label, query string, args ...interface{}) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		return id
	}

	userID := insertID("user", `
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('asset-mutation@example.test', 'asset-mutation', 'Asset', 'Mutation') RETURNING id`)
	setID := insertID("asset set", `
		INSERT INTO asset_management_sets (name, created_by)
		VALUES ('Mutation assets', ?) RETURNING id`, userID)
	typeID := insertID("asset type", `
		INSERT INTO asset_types (set_id, name)
		VALUES (?, 'Server') RETURNING id`, setID)
	fieldID := insertID("number field", `
		INSERT INTO custom_field_definitions (name, field_type)
		VALUES ('Rack units', 'number') RETURNING id`)
	if _, err := db.ExecWrite(`
		INSERT INTO asset_type_fields (asset_type_id, custom_field_id, is_required)
		VALUES (?, ?, true)
	`, typeID, fieldID); err != nil {
		t.Fatalf("assign field: %v", err)
	}
	assetID := insertID("asset", `
		INSERT INTO assets (set_id, asset_type_id, title, custom_field_values, created_by)
		VALUES (?, ?, 'Original title', ?, ?) RETURNING id`,
		setID, typeID, fmt.Sprintf(`{"%d":1}`, fieldID), userID)

	service := &AssetActionService{
		db:         db,
		repo:       repository.NewAssetActionRepository(db),
		chainStore: NewExecutionChainStore(),
	}
	ctx := &models.AssetActionExecutionContext{
		Event: &models.AssetActionEvent{
			EventType:    models.AssetTriggerManual,
			SetID:        setID,
			AssetID:      assetID,
			ActorUserID:  userID,
			CascadeDepth: 1,
		},
		Variables: map[string]interface{}{},
		ChainID:   "asset-mutation-chain",
	}

	numberNode := &models.AssetActionNode{
		NodeType: models.AssetNodeSetField,
		NodeConfig: fmt.Sprintf(
			`{"field_name":%q,"value":"42"}`,
			fmt.Sprintf("%d", fieldID),
		),
	}
	result := &models.StepResult{}
	if err := service.executeSetField(numberNode, ctx, result); err != nil {
		t.Fatalf("executeSetField number: %v", err)
	}
	var rawFields string
	if err := db.QueryRow(`SELECT custom_field_values FROM assets WHERE id = ?`, assetID).Scan(&rawFields); err != nil {
		t.Fatalf("load custom fields: %v", err)
	}
	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(rawFields), &fields); err != nil {
		t.Fatalf("decode custom fields: %v", err)
	}
	if got := fields[fmt.Sprintf("%d", fieldID)]; got != float64(42) {
		t.Fatalf("stored number = %#v, want numeric 42", got)
	}

	emptyTitle := &models.AssetActionNode{
		NodeType:   models.AssetNodeSetField,
		NodeConfig: `{"field_name":"title","value":"   "}`,
	}
	if err := service.executeSetField(emptyTitle, ctx, &models.StepResult{}); err == nil || !strings.Contains(err.Error(), "title is required") {
		t.Fatalf("empty title error = %v, want canonical title validation", err)
	}
	var title string
	if err := db.QueryRow(`SELECT title FROM assets WHERE id = ?`, assetID).Scan(&title); err != nil {
		t.Fatalf("load title: %v", err)
	}
	if title != "Original title" {
		t.Fatalf("title = %q, want unchanged Original title", title)
	}

	unknownField := &models.AssetActionNode{
		NodeType:   models.AssetNodeSetField,
		NodeConfig: `{"field_name":"not_declared","value":"x"}`,
	}
	if err := service.executeSetField(unknownField, ctx, &models.StepResult{}); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("unknown field error = %v, want schema rejection", err)
	}

	var auditCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM audit_logs
		WHERE action_type = ? AND resource_type = ? AND resource_id = ? AND user_id = ?
	`, logger.ActionAssetUpdate, logger.ResourceAsset, assetID, userID).Scan(&auditCount); err != nil {
		t.Fatalf("count asset update audits: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("successful mutation audit count = %d, want 1", auditCount)
	}

	var actorKind, actorRef, payloadJSON string
	if err := db.QueryRow(`
		SELECT actor_kind, actor_ref, payload
		FROM domain_events
		WHERE aggregate_type = 'asset' AND aggregate_id = ? AND event_type = ?
		ORDER BY id DESC LIMIT 1
	`, assetID, assetevents.Updated).Scan(&actorKind, &actorRef, &payloadJSON); err != nil {
		t.Fatalf("load canonical asset update event: %v", err)
	}
	var payload assetevents.UpdatedV1
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("decode canonical asset update event: %v", err)
	}
	if actorKind != "user" || actorRef != fmt.Sprint(userID) || payload.Asset.ID != assetID {
		t.Fatalf("update event = actor:%s/%s payload:%#v", actorKind, actorRef, payload)
	}
	if payload.Automation == nil || !payload.Automation.TriggeredByAction || payload.Automation.ExecutionChainID != "asset-mutation-chain" || payload.Automation.CascadeDepth != 2 {
		t.Fatalf("update cascade context = %#v", payload.Automation)
	}
}
