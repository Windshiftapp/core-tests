package handlers

import (
	"path/filepath"
	"strconv"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
)

func TestAssetReportBindingAvailableFailsClosedOnDrift(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "portal-asset-binding.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	var workspaceID, itemTypeID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Portal', 'PAB') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO item_types (name) VALUES ('Portal Asset Request') RETURNING id`).Scan(&itemTypeID); err != nil {
		t.Fatalf("insert item type: %v", err)
	}

	handler := &PortalHandler{db: db}
	config := models.ChannelConfig{PortalWorkspaceIDs: []int{workspaceID}}
	allowed, err := handler.assetReportBindingAvailable(&config, "form", &itemTypeID, &workspaceID)
	if err != nil || !allowed {
		t.Fatalf("valid binding = (%v, %v), want (true, nil)", allowed, err)
	}

	otherWorkspaceID := workspaceID + 100
	allowed, err = handler.assetReportBindingAvailable(&config, "form", &itemTypeID, &otherWorkspaceID)
	if err != nil || allowed {
		t.Fatalf("unserved workspace binding = (%v, %v), want (false, nil)", allowed, err)
	}
	missingItemTypeID := itemTypeID + 100
	allowed, err = handler.assetReportBindingAvailable(&config, "form", &missingItemTypeID, nil)
	if err != nil || allowed {
		t.Fatalf("missing item type binding = (%v, %v), want (false, nil)", allowed, err)
	}
	allowed, err = handler.assetReportBindingAvailable(&config, "direct", nil, &otherWorkspaceID)
	if err != nil || !allowed {
		t.Fatalf("direct report = (%v, %v), want (true, nil)", allowed, err)
	}
}

func TestLoadAssetReportFieldsOnlyReturnsFieldsBoundToCreateScreen(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "portal-asset-fields.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	insertID := func(label, query string, args ...interface{}) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		return id
	}
	workspaceID := insertID("workspace", `INSERT INTO workspaces (name, key) VALUES ('Portal asset fields', 'PAF') RETURNING id`)
	itemTypeID := insertID("item type", `INSERT INTO item_types (name) VALUES ('Portal Asset Field Type') RETURNING id`)
	channelID := insertID("channel", `INSERT INTO channels (name, type, direction, status, config) VALUES ('Portal Asset Field Channel', 'portal', 'inbound', 'enabled', '{}') RETURNING id`)
	assetSetID := insertID("asset set", `INSERT INTO asset_management_sets (name) VALUES ('Portal Asset Field Set') RETURNING id`)
	configSetID := insertID("configuration set", `INSERT INTO configuration_sets (name) VALUES ('Portal Asset Field Config') RETURNING id`)
	screenID := insertID("screen", `INSERT INTO screens (name) VALUES ('Portal Asset Field Screen') RETURNING id`)
	allowedFieldID := insertID("allowed field", `INSERT INTO custom_field_definitions (name, field_type) VALUES ('Portal Asset Allowed', 'text') RETURNING id`)
	hiddenFieldID := insertID("hidden field", `INSERT INTO custom_field_definitions (name, field_type) VALUES ('Portal Asset Hidden', 'text') RETURNING id`)

	statements := []struct {
		label string
		query string
		args  []interface{}
	}{
		{"workspace configuration", `INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, []interface{}{workspaceID, configSetID}},
		{"item-type screen", `INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id, create_screen_id) VALUES (?, ?, ?)`, []interface{}{configSetID, itemTypeID, screenID}},
		{"screen field", `INSERT INTO screen_fields (screen_id, field_type, field_identifier) VALUES (?, 'custom', ?)`, []interface{}{screenID, strconv.Itoa(allowedFieldID)}},
	}
	for _, statement := range statements {
		if _, err := db.ExecWrite(statement.query, statement.args...); err != nil {
			t.Fatalf("insert %s: %v", statement.label, err)
		}
	}

	reportID := insertID("asset report", `
		INSERT INTO asset_reports (channel_id, asset_set_id, name, run_mode, item_type_id, workspace_id)
		VALUES (?, ?, 'Portal Asset Field Report', 'form', ?, ?) RETURNING id
	`, channelID, assetSetID, itemTypeID, workspaceID)
	for _, fieldID := range []int{allowedFieldID, hiddenFieldID} {
		if _, err := db.ExecWrite(`
			INSERT INTO asset_report_fields (asset_report_id, field_identifier, field_type, display_order)
			VALUES (?, ?, 'custom', ?)
		`, reportID, strconv.Itoa(fieldID), fieldID); err != nil {
			t.Fatalf("insert asset report field %d: %v", fieldID, err)
		}
	}

	fields, err := (&PortalHandler{db: db}).loadAssetReportFields(reportID)
	if err != nil {
		t.Fatalf("loadAssetReportFields: %v", err)
	}
	if len(fields) != 1 || fields[0].FieldIdentifier != strconv.Itoa(allowedFieldID) {
		t.Fatalf("asset report fields = %+v, want only bound field %d", fields, allowedFieldID)
	}
}
