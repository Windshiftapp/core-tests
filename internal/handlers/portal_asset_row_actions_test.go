//go:build test

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/models"
)

func TestFilterVisibleRowActionsDropsInvisibleAndMalformed(t *testing.T) {
	visible := map[int]bool{10: true}
	actions := []models.AssetReportRowAction{
		{ID: "ok", Label: "  Report an issue  ", RequestTypeID: 10, TargetField: " device ", Source: "asset_id"},
		{ID: "hidden", Label: "Hidden", RequestTypeID: 11, TargetField: "device", Source: "asset_id"},
		{ID: "bad-source", Label: "Bad", RequestTypeID: 10, TargetField: "device", Source: "whole_asset"},
		{ID: "no-label", Label: "   ", RequestTypeID: 10, TargetField: "device", Source: "title"},
		{ID: "no-target", Label: "No target", RequestTypeID: 10, TargetField: "", Source: "title"},
	}

	got := filterVisibleRowActions(actions, visible)
	if len(got) != 1 {
		t.Fatalf("filtered actions = %+v, want only the valid visible action", got)
	}
	if got[0].ID != "ok" || got[0].Label != "Report an issue" || got[0].TargetField != "device" {
		t.Fatalf("action was not scrubbed: %+v", got[0])
	}
}

// TestPortalBootstrapRowActionsExposeOnlyVisibleRequestTypes guards the
// server-side visibility filter: a row action whose target request type is not
// visible to the visitor must never reach the public config.
func TestPortalBootstrapRowActionsExposeOnlyVisibleRequestTypes(t *testing.T) {
	db, channelID, workspaceID := setupPortalBootstrapTest(t)

	var visibleRT, hiddenRT int
	if err := db.QueryRow(`SELECT id FROM request_types WHERE channel_id = ?`, channelID).Scan(&visibleRT); err != nil {
		t.Fatalf("load visible request type: %v", err)
	}
	var itemTypeID int
	if err := db.QueryRow(`SELECT id FROM item_types LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("load item type: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO request_types (channel_id, name, item_type_id, workspace_id, is_active, visibility_group_ids)
		VALUES (?, 'Hidden form', ?, ?, true, '[42]') RETURNING id
	`, channelID, itemTypeID, workspaceID).Scan(&hiddenRT); err != nil {
		t.Fatalf("insert hidden request type: %v", err)
	}

	config := fmt.Sprintf(`{"row_actions":[
		{"id":"ra1","label":"Report an issue","request_type_id":%d,"target_field":"device","source":"asset_id"},
		{"id":"ra2","label":"Hidden action","request_type_id":%d,"target_field":"asset_tag","source":"asset_tag"}
	]}`, visibleRT, hiddenRT)
	var assetSetID int
	if err := db.QueryRow(`INSERT INTO asset_management_sets (name) VALUES ('Row action set') RETURNING id`).Scan(&assetSetID); err != nil {
		t.Fatalf("insert asset set: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO asset_reports (channel_id, asset_set_id, name, cql_query, run_mode, config)
		VALUES (?, ?, 'Devices', 'status = "Active"', 'direct', ?)
	`, channelID, assetSetID, config); err != nil {
		t.Fatalf("insert asset report: %v", err)
	}

	handler := NewPortalHandler(db, nil, nil, nil, blobstore.Disabled())
	recorder := httptest.NewRecorder()
	handler.GetBootstrap(recorder, authenticatedPortalBootstrapRequest())

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response PortalBootstrapResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.AssetReports) != 1 {
		t.Fatalf("asset reports = %d, want 1", len(response.AssetReports))
	}
	cfg := response.AssetReports[0].Config
	if cfg == nil || len(cfg.RowActions) != 1 {
		t.Fatalf("row actions = %+v, want only the visible target", cfg)
	}
	if cfg.RowActions[0].ID != "ra1" || cfg.RowActions[0].RequestTypeID != visibleRT {
		t.Fatalf("row action = %+v, want ra1 targeting %d", cfg.RowActions[0], visibleRT)
	}
}
