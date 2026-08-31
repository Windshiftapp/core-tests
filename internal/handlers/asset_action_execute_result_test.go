//go:build test

package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func TestManualAssetActionFailedResponseMatchesExecutionLog(t *testing.T) {
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
		VALUES ('asset-execute@example.test', 'asset-execute', 'Asset', 'Execute') RETURNING id`)
	if _, err := db.ExecWrite(`
		INSERT INTO user_global_permissions (user_id, permission_id)
		SELECT ?, id FROM permissions WHERE permission_key = 'system.admin'
	`, userID); err != nil {
		t.Fatalf("grant system admin: %v", err)
	}
	setID := insertID("asset set", `
		INSERT INTO asset_management_sets (name, created_by)
		VALUES ('Execute assets', ?) RETURNING id`, userID)
	typeID := insertID("asset type", `
		INSERT INTO asset_types (set_id, name)
		VALUES (?, 'Server') RETURNING id`, setID)
	assetID := insertID("asset", `
		INSERT INTO assets (set_id, asset_type_id, title, created_by)
		VALUES (?, ?, 'Manual target', ?) RETURNING id`, setID, typeID, userID)
	actionID := insertID("asset action", `
		INSERT INTO asset_actions (set_id, name, is_enabled, trigger_type, created_by)
		VALUES (?, 'Failing manual action', true, 'manual', ?) RETURNING id`, setID, userID)
	insertID("notify node", `
		INSERT INTO asset_action_nodes (action_id, node_type, node_config)
		VALUES (?, 'notify_user', '{"recipients":["creator"],"message":"hello"}') RETURNING id`, actionID)

	permissionService, err := services.NewPermissionService(db, services.PermissionCacheConfig{
		TTL: time.Minute, MaxCacheSize: 8, WarmupOnStartup: false, PreWarmActive: false, BatchSize: 10,
	})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = permissionService.Close() })
	actionService := services.NewAssetActionService(db, services.DefaultActionServiceConfig(), nil)
	t.Cleanup(actionService.Stop)
	handler := NewAssetActionHandler(
		repository.NewAssetActionRepository(db),
		NewAssetHandler(db, permissionService, ""),
		actionService,
		logger.NewAuditor(db),
	)

	request := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/asset-sets/%d/actions/%d/execute", setID, actionID),
		bytes.NewBufferString(fmt.Sprintf(`{"asset_id":%d}`, assetID)),
	)
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("setId", fmt.Sprintf("%d", setID))
	request.SetPathValue("id", fmt.Sprintf("%d", actionID))
	request = testutils.WithAuthContext(request, &models.User{
		ID: userID, Email: "asset-execute@example.test", Username: "asset-execute",
		FirstName: "Asset", LastName: "Execute", IsActive: true,
	})
	recorder := httptest.NewRecorder()

	handler.ExecuteAction(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response services.AssetActionExecutionResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != models.ActionStatusFailed {
		t.Fatalf("response status = %q, want failed", response.Status)
	}
	if response.LogID <= 0 {
		t.Fatalf("response log_id = %d, want positive", response.LogID)
	}
	if response.ErrorMessage != "notify_user: notification service not configured" {
		t.Fatalf("response error = %q", response.ErrorMessage)
	}

	var persistedStatus models.ActionExecutionStatus
	var persistedError string
	if err := db.QueryRow(`
		SELECT status, COALESCE(error_message, '')
		FROM asset_action_execution_logs WHERE id = ?
	`, response.LogID).Scan(&persistedStatus, &persistedError); err != nil {
		t.Fatalf("load execution log: %v", err)
	}
	if persistedStatus != response.Status || persistedError != response.ErrorMessage {
		t.Fatalf(
			"persisted result = (%q, %q), response = (%q, %q)",
			persistedStatus, persistedError, response.Status, response.ErrorMessage,
		)
	}
}
