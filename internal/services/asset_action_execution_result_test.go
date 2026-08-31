//go:build test

package services

import (
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestManualAssetActionResultMatchesPersistedExecutionLog(t *testing.T) {
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
		VALUES ('asset-result@example.test', 'asset-result', 'Asset', 'Result') RETURNING id`)
	setID := insertID("asset set", `
		INSERT INTO asset_management_sets (name, created_by)
		VALUES ('Execution result assets', ?) RETURNING id`, userID)
	typeID := insertID("asset type", `
		INSERT INTO asset_types (set_id, name)
		VALUES (?, 'Server') RETURNING id`, setID)
	assetID := insertID("asset", `
		INSERT INTO assets (set_id, asset_type_id, title, created_by)
		VALUES (?, ?, 'Execution target', ?) RETURNING id`, setID, typeID, userID)
	actionID := insertID("asset action", `
		INSERT INTO asset_actions (set_id, name, is_enabled, trigger_type, created_by)
		VALUES (?, 'Manual result', true, 'manual', ?) RETURNING id`, setID, userID)

	service := &AssetActionService{
		db:         db,
		repo:       repository.NewAssetActionRepository(db),
		chainStore: NewExecutionChainStore(),
	}

	tests := []struct {
		name      string
		nodes     []models.AssetActionNode
		want      models.ActionExecutionStatus
		wantError string
	}{
		{
			name: "completed",
			nodes: []models.AssetActionNode{
				{ID: 1, NodeType: models.AssetNodeTrigger, NodeConfig: `{}`},
				{ID: 2, NodeType: models.AssetNodeCondition, NodeConfig: `{"field_name":"asset_title","operator":"eq","value":"Execution target"}`},
			},
			want: models.ActionStatusCompleted,
		},
		{
			name: "failed",
			nodes: []models.AssetActionNode{
				{ID: 1, NodeType: models.AssetNodeTrigger, NodeConfig: `{}`},
				{ID: 2, NodeType: models.AssetNodeNotifyUser, NodeConfig: `{"recipients":["creator"],"message":"hello"}`},
			},
			want:      models.ActionStatusFailed,
			wantError: "notify_user: notification service not configured",
		},
		{
			name: "skipped",
			nodes: []models.AssetActionNode{
				{ID: 1, NodeType: models.AssetNodeTrigger, NodeConfig: `{}`},
			},
			want: models.ActionStatusSkipped,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action := &models.AssetAction{
				ID:          actionID,
				SetID:       setID,
				Name:        tc.name,
				IsEnabled:   true,
				TriggerType: models.AssetTriggerManual,
				Nodes:       tc.nodes,
			}
			result, err := service.ExecuteActionManuallyWithResult(action, assetID, userID)
			if err != nil {
				t.Fatalf("ExecuteActionManuallyWithResult: %v", err)
			}
			if result == nil {
				t.Fatal("result is nil")
			}
			if result.LogID <= 0 {
				t.Fatalf("log_id = %d, want positive", result.LogID)
			}
			if result.Status != tc.want {
				t.Fatalf("status = %q, want %q", result.Status, tc.want)
			}
			if result.ErrorMessage != tc.wantError {
				t.Fatalf("error = %q, want %q", result.ErrorMessage, tc.wantError)
			}

			var persistedStatus models.ActionExecutionStatus
			var persistedError string
			if err := db.QueryRow(`
				SELECT status, COALESCE(error_message, '')
				FROM asset_action_execution_logs
				WHERE id = ?
			`, result.LogID).Scan(&persistedStatus, &persistedError); err != nil {
				t.Fatalf("load execution log: %v", err)
			}
			if persistedStatus != result.Status || persistedError != result.ErrorMessage {
				t.Fatalf(
					"persisted result = (%q, %q), returned = (%q, %q)",
					persistedStatus, persistedError, result.Status, result.ErrorMessage,
				)
			}
		})
	}
}
