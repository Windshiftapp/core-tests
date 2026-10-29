//go:build test

package services

import (
	"encoding/json"
	"fmt"
	"testing"

	"windshift/internal/testutils"
)

func setupRequiredAssetImportTest(t *testing.T) (*AssetApplicationService, *testutils.TestDB, int, int, int, int, int) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	db := tdb.GetDatabase()
	insertID := func(label, query string, args ...any) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		return id
	}
	userID := insertID("user", `INSERT INTO users (email, username, first_name, last_name) VALUES ('asset-import@example.test', 'asset-import', 'Asset', 'Importer') RETURNING id`)
	setID := insertID("asset set", `INSERT INTO asset_management_sets (name, created_by) VALUES ('Required import fields', ?) RETURNING id`, userID)
	typeID := insertID("asset type", `INSERT INTO asset_types (set_id, name) VALUES (?, 'Server') RETURNING id`, setID)
	ownerFieldID := insertID("owner field", `INSERT INTO custom_field_definitions (name, field_type) VALUES ('Import owner', 'text') RETURNING id`)
	regionFieldID := insertID("region field", `INSERT INTO custom_field_definitions (name, field_type) VALUES ('Import region', 'text') RETURNING id`)
	if _, err := db.ExecWrite(`INSERT INTO asset_type_fields (asset_type_id, custom_field_id, is_required, display_order) VALUES (?, ?, true, 0), (?, ?, true, 1)`, typeID, ownerFieldID, typeID, regionFieldID); err != nil {
		t.Fatalf("attach required fields: %v", err)
	}
	return NewAssetApplicationService(db, nil, nil, nil), tdb, setID, typeID, ownerFieldID, regionFieldID, userID
}

func TestImportAssetCSVRowAlwaysEnforcesRequiredCustomFields(t *testing.T) {
	app, tdb, setID, typeID, ownerFieldID, regionFieldID, userID := setupRequiredAssetImportTest(t)
	db := tdb.GetDatabase()
	seedRunningAssetImportJob(t, db, setID, userID, "required-fields-test")
	base := AssetImportMappings{Title: 0, Description: -1, AssetTag: -1, CategoryID: -1, StatusID: -1}
	tests := []struct {
		name         string
		record       []string
		customFields map[string]int
	}{
		{name: "no custom mapping", record: []string{"No mapping"}},
		{name: "all mapped cells blank", record: []string{"Blank mapping", "", ""}, customFields: map[string]int{fmt.Sprint(ownerFieldID): 1, fmt.Sprint(regionFieldID): 2}},
		{name: "partial required values", record: []string{"Partial mapping", "alice", ""}, customFields: map[string]int{fmt.Sprint(ownerFieldID): 1, fmt.Sprint(regionFieldID): 2}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mappings := base
			mappings.CustomFields = test.customFields
			if err := app.importAssetCSVRow(test.record, setID, StartAssetImport{AssetTypeID: typeID, Mappings: mappings}, userID, nil, "required-fields-test"); err == nil {
				t.Fatal("importAssetCSVRow succeeded, want required-field error")
			}
		})
	}
	var invalid int
	if err := db.QueryRow("SELECT COUNT(*) FROM assets").Scan(&invalid); err != nil || invalid != 0 {
		t.Fatalf("invalid assets = %d, err = %v", invalid, err)
	}
	valid := base
	valid.CustomFields = map[string]int{fmt.Sprint(ownerFieldID): 1, fmt.Sprint(regionFieldID): 2}
	if err := app.importAssetCSVRow([]string{"Valid mapping", "alice", "eu-central"}, setID, StartAssetImport{AssetTypeID: typeID, Mappings: valid}, userID, nil, "required-fields-test"); err != nil {
		t.Fatalf("valid importAssetCSVRow: %v", err)
	}
	var raw string
	if err := db.QueryRow("SELECT custom_field_values FROM assets WHERE title = 'Valid mapping'").Scan(&raw); err != nil {
		t.Fatalf("load values: %v", err)
	}
	values := make(map[string]any)
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		t.Fatalf("decode values: %v", err)
	}
	if values[fmt.Sprint(ownerFieldID)] != "alice" || values[fmt.Sprint(regionFieldID)] != "eu-central" {
		t.Fatalf("custom fields = %#v", values)
	}
}
