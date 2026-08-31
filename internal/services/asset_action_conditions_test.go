//go:build test

package services

import (
	"fmt"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func setupAssetActionConditionFixture(t *testing.T) (*AssetActionService, int, int, int) {
	t.Helper()
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
		VALUES ('asset-condition@example.test', 'asset-condition', 'Asset', 'Condition') RETURNING id`)
	setID := insertID("asset set", `
		INSERT INTO asset_management_sets (name, created_by)
		VALUES ('Condition assets', ?) RETURNING id`, userID)
	typeID := insertID("asset type", `
		INSERT INTO asset_types (set_id, name)
		VALUES (?, 'Server') RETURNING id`, setID)
	statusID := insertID("asset status", `
		INSERT INTO asset_statuses (set_id, name)
		VALUES (?, 'Online') RETURNING id`, setID)
	assetID := insertID("asset", `
		INSERT INTO assets (set_id, asset_type_id, status_id, title, asset_tag, description, created_by)
		VALUES (?, ?, ?, 'Edge server', 'SRV-42', 'At the edge', ?) RETURNING id`,
		setID, typeID, statusID, userID)

	return &AssetActionService{
		db:         db,
		repo:       repository.NewAssetActionRepository(db),
		chainStore: NewExecutionChainStore(),
	}, setID, assetID, userID
}

func TestAssetActionConditionFieldsResolveForEveryTrigger(t *testing.T) {
	service, setID, assetID, userID := setupAssetActionConditionFixture(t)
	fields := map[string]string{
		"asset_title":       "Edge server",
		"asset_tag":         "SRV-42",
		"asset_type_name":   "Server",
		"asset_status_name": "Online",
	}
	triggers := []models.AssetActionTriggerType{
		models.AssetTriggerAssetCreated,
		models.AssetTriggerAssetUpdated,
		models.AssetTriggerAssetStatusChanged,
		models.AssetTriggerManual,
	}

	for _, trigger := range triggers {
		for field, expected := range fields {
			t.Run(fmt.Sprintf("%s/%s", trigger, field), func(t *testing.T) {
				ctx := &models.AssetActionExecutionContext{
					Event: &models.AssetActionEvent{
						EventType:   trigger,
						SetID:       setID,
						AssetID:     assetID,
						ActorUserID: userID,
						OldValues:   map[string]interface{}{},
						NewValues:   map[string]interface{}{},
					},
					Variables: map[string]interface{}{},
				}
				service.loadAssetVariables(ctx)
				node := &models.AssetActionNode{
					NodeType:   models.AssetNodeCondition,
					NodeConfig: fmt.Sprintf(`{"field_name":%q,"operator":"eq","value":%q}`, field, expected),
				}
				result := &models.StepResult{}

				if err := service.executeCondition(node, ctx, result); err != nil {
					t.Fatalf("executeCondition: %v", err)
				}
				if got := result.Output["condition_result"]; got != true {
					t.Fatalf("condition_result = %#v, want true; output=%#v", got, result.Output)
				}
				if got := result.Output["field_name"]; got != field {
					t.Fatalf("field_name = %#v, want %q", got, field)
				}
				if got := result.Output["field_value"]; got != expected {
					t.Fatalf("field_value = %#v, want %q", got, expected)
				}
			})
		}
	}
}

func TestAssetActionConditionAliasesRemainCompatibleAndUnknownFieldsAreRejected(t *testing.T) {
	service, setID, assetID, userID := setupAssetActionConditionFixture(t)
	ctx := &models.AssetActionExecutionContext{
		Event: &models.AssetActionEvent{
			EventType:   models.AssetTriggerManual,
			SetID:       setID,
			AssetID:     assetID,
			ActorUserID: userID,
		},
		Variables: map[string]interface{}{},
	}
	service.loadAssetVariables(ctx)

	for alias, expectedCanonical := range map[string]string{
		"title":       "asset_title",
		"type_name":   "asset_type_name",
		"status_name": "asset_status_name",
	} {
		t.Run("legacy_"+alias, func(t *testing.T) {
			node := &models.AssetActionNode{
				NodeType:   models.AssetNodeCondition,
				NodeConfig: fmt.Sprintf(`{"field_name":%q,"operator":"is_not_empty","value":""}`, alias),
			}
			result := &models.StepResult{}
			if err := service.executeCondition(node, ctx, result); err != nil {
				t.Fatalf("executeCondition: %v", err)
			}
			if got := result.Output["condition_result"]; got != true {
				t.Fatalf("condition_result = %#v, want true", got)
			}
			if got := result.Output["field_name"]; got != expectedCanonical {
				t.Fatalf("field_name = %#v, want %q", got, expectedCanonical)
			}
		})
	}

	invalid := []models.AssetActionNode{{
		NodeType:   models.AssetNodeCondition,
		NodeConfig: `{"field_name":"unknown_field","operator":"eq","value":"x"}`,
	}}
	if err := service.ValidateTaxonomyReferences(setID, "", invalid); err == nil {
		t.Fatal("ValidateTaxonomyReferences accepted an unknown condition field")
	}
}
