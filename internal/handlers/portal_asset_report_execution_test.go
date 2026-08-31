//go:build test

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/middleware"
	"windshift/internal/testutils"
)

func TestExecuteAssetReportSubstitutesFormPlaceholderBeforeRunningCQL(t *testing.T) {
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

	workspaceID := insertID("workspace", `INSERT INTO workspaces (name, key) VALUES ('Asset report workspace', 'ARW') RETURNING id`)
	itemTypeID := insertID("item type", `INSERT INTO item_types (name) VALUES ('Asset report request') RETURNING id`)
	channelConfig := fmt.Sprintf(`{"portal_slug":"asset-report","portal_workspace_ids":[%d]}`, workspaceID)
	channelID := insertID("channel", `
		INSERT INTO channels (name, type, direction, status, config, public_slug)
		VALUES ('Asset report portal', 'portal', 'inbound', 'enabled', ?, 'asset-report') RETURNING id
	`, channelConfig)
	assetSetID := insertID("asset set", `INSERT INTO asset_management_sets (name) VALUES ('Asset report inventory') RETURNING id`)
	assetTypeID := insertID("asset type", `INSERT INTO asset_types (set_id, name) VALUES (?, 'Device') RETURNING id`, assetSetID)
	wantedAssetID := insertID("matching asset", `
		INSERT INTO assets (set_id, asset_type_id, title, asset_tag)
		VALUES (?, ?, 'Matching device', 'MATCH-1') RETURNING id
	`, assetSetID, assetTypeID)
	insertID("non-matching asset", `
		INSERT INTO assets (set_id, asset_type_id, title, asset_tag)
		VALUES (?, ?, 'Other device', 'OTHER-1') RETURNING id
	`, assetSetID, assetTypeID)
	reportID := insertID("asset report", `
		INSERT INTO asset_reports
			(channel_id, asset_set_id, name, cql_query, is_active, column_config, run_mode, item_type_id, workspace_id)
		VALUES (?, ?, 'Asset tag lookup', 'asset_tag = ${asset-tag}', true, '["title","asset_tag"]', 'form', ?, ?)
		RETURNING id
	`, channelID, assetSetID, itemTypeID, workspaceID)
	if _, err := db.ExecWrite(`
		INSERT INTO asset_report_fields
			(asset_report_id, field_identifier, field_type, is_required, display_name)
		VALUES (?, 'asset-tag', 'virtual', true, 'Asset tag')
	`, reportID); err != nil {
		t.Fatalf("insert report field: %v", err)
	}

	sessionManager := auth.NewSessionManager(db, false, false, nil, "asset-report-test-secret", "strict")
	handler := NewPortalHandler(db, sessionManager, nil, nil, "")
	request := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/portal/asset-report/asset-reports/%d/execute", reportID),
		bytes.NewBufferString(`{"params":{"asset-tag":"MATCH-1"}}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "asset-report")
	request.SetPathValue("id", fmt.Sprintf("%d", reportID))
	request = request.WithContext(context.WithValue(request.Context(), middleware.ContextKeySession, &auth.Session{UserID: 1}))
	recorder := httptest.NewRecorder()

	handler.ExecuteAssetReport(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Assets []struct {
			ID       int    `json:"id"`
			Title    string `json:"title"`
			AssetTag string `json:"asset_tag"`
		} `json:"assets"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Total != 1 || len(response.Assets) != 1 {
		t.Fatalf("result = %+v, want exactly one matching asset", response)
	}
	if response.Assets[0].ID != wantedAssetID || response.Assets[0].Title != "Matching device" || response.Assets[0].AssetTag != "MATCH-1" {
		t.Fatalf("asset = %+v, want matching asset %d", response.Assets[0], wantedAssetID)
	}
}
