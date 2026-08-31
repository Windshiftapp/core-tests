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

func TestAssetActionTogglePayloadMatrixAndAudit(t *testing.T) {
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
		VALUES ('asset-toggle@example.test', 'asset-toggle', 'Asset', 'Toggle') RETURNING id`)
	if _, err := db.ExecWrite(`
		INSERT INTO user_global_permissions (user_id, permission_id)
		SELECT ?, id FROM permissions WHERE permission_key = 'system.admin'
	`, userID); err != nil {
		t.Fatalf("grant system admin: %v", err)
	}
	setID := insertID("asset set", `
		INSERT INTO asset_management_sets (name, created_by)
		VALUES ('Toggle assets', ?) RETURNING id`, userID)
	actionID := insertID("asset action", `
		INSERT INTO asset_actions (set_id, name, is_enabled, trigger_type, created_by)
		VALUES (?, 'Toggle action', false, 'manual', ?) RETURNING id`, setID, userID)

	permissionService, err := services.NewPermissionService(db, services.PermissionCacheConfig{
		TTL: time.Minute, MaxCacheSize: 8, WarmupOnStartup: false, PreWarmActive: false, BatchSize: 10,
	})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = permissionService.Close() })
	handler := NewAssetActionHandler(
		repository.NewAssetActionRepository(db),
		NewAssetHandler(db, permissionService, ""),
		nil,
		logger.NewAuditor(db),
	)
	user := &models.User{
		ID: userID, Email: "asset-toggle@example.test", Username: "asset-toggle",
		FirstName: "Asset", LastName: "Toggle", IsActive: true,
	}

	tests := []struct {
		name           string
		body           string
		startEnabled   bool
		wantStatus     int
		wantEnabled    bool
		wantAuditDelta int
	}{
		{name: "empty body toggles", body: "", startEnabled: false, wantStatus: http.StatusOK, wantEnabled: true, wantAuditDelta: 1},
		{name: "explicit boolean is idempotent", body: `{"is_enabled":true}`, startEnabled: true, wantStatus: http.StatusOK, wantEnabled: true, wantAuditDelta: 1},
		{name: "truncated JSON is rejected", body: `{"is_enabled":`, startEnabled: true, wantStatus: http.StatusBadRequest, wantEnabled: true},
		{name: "wrong type is rejected", body: `{"is_enabled":"yes"}`, startEnabled: false, wantStatus: http.StatusBadRequest, wantEnabled: false},
	}

	auditCount := 0
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.ExecWrite(`UPDATE asset_actions SET is_enabled = ? WHERE id = ?`, tc.startEnabled, actionID); err != nil {
				t.Fatalf("reset action: %v", err)
			}
			request := httptest.NewRequest(
				http.MethodPost,
				fmt.Sprintf("/api/asset-sets/%d/actions/%d/toggle", setID, actionID),
				bytes.NewBufferString(tc.body),
			)
			request.Header.Set("Content-Type", "application/json")
			request.SetPathValue("setId", fmt.Sprintf("%d", setID))
			request.SetPathValue("id", fmt.Sprintf("%d", actionID))
			request = testutils.WithAuthContext(request, user)
			recorder := httptest.NewRecorder()

			handler.ToggleAction(recorder, request)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			var enabled bool
			if err := db.QueryRow(`SELECT is_enabled FROM asset_actions WHERE id = ?`, actionID).Scan(&enabled); err != nil {
				t.Fatalf("load action: %v", err)
			}
			if enabled != tc.wantEnabled {
				t.Fatalf("is_enabled = %v, want %v", enabled, tc.wantEnabled)
			}

			auditCount += tc.wantAuditDelta
			var gotAuditCount int
			if err := db.QueryRow(`
				SELECT COUNT(*)
				FROM audit_logs
				WHERE action_type = ? AND resource_type = ? AND resource_id = ?
			`, logger.ActionAutomationToggle, logger.ResourceAutomation, actionID).Scan(&gotAuditCount); err != nil {
				t.Fatalf("count audit logs: %v", err)
			}
			if gotAuditCount != auditCount {
				t.Fatalf("toggle audit count = %d, want %d", gotAuditCount, auditCount)
			}
		})
	}

	var detailsJSON string
	if err := db.QueryRow(`
		SELECT details FROM audit_logs
		WHERE action_type = ? AND resource_id = ?
		ORDER BY id DESC LIMIT 1
	`, logger.ActionAutomationToggle, actionID).Scan(&detailsJSON); err != nil {
		t.Fatalf("load audit details: %v", err)
	}
	var details map[string]interface{}
	if err := json.Unmarshal([]byte(detailsJSON), &details); err != nil {
		t.Fatalf("decode audit details: %v", err)
	}
	if got := details["is_enabled"]; got != true {
		t.Fatalf("audit is_enabled = %#v, want true", got)
	}
	if got := details["old_is_enabled"]; got != true {
		t.Fatalf("audit old_is_enabled = %#v, want true", got)
	}
}
