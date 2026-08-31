package handlers

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/services"
)

// Regression tests for docs/bughunt-2026-05-19-pass-2.md findings #2 and #3.
//
// F2: portal/form submissions must only persist custom fields that are
// configured on the request type. Arbitrary submitted field IDs (whether or
// not the field exists as a definition) must be dropped silently so the
// endpoint cannot be used as an oracle for which fields are configured.
//
// F3: required-field validation must treat empty arrays, empty objects, and
// whitespace-only strings as blank. Scalar `false` and `0` are NOT blank.

func seedRequestTypeWithCustomFields(t *testing.T, db database.Database, rtID int, customFieldIdentifiers []string, requiredIdentifiers map[string]bool) {
	t.Helper()
	var itemTypeID int
	if err := db.QueryRow(`SELECT id FROM item_types LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("no seeded item_type: %v", err)
	}
	var workspaceID int
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, key, active)
		VALUES (?, ?, true) RETURNING id
	`, "Workspace RT "+strconv.Itoa(rtID), "RT"+strconv.Itoa(rtID)).Scan(&workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	// request_types.channel_id is NOT NULL; seed one channel per request type.
	res, err := db.Exec(`
		INSERT INTO channels (name, type, direction, status, config)
		VALUES (?, 'portal', 'inbound', 'enabled', ?)
	`, "ch-rt-"+strconv.Itoa(rtID), `{"portal_workspace_ids":[`+strconv.Itoa(workspaceID)+`]}`)
	if err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	chID64, _ := res.LastInsertId()
	var configSetID, screenID int
	if err := db.QueryRow(`INSERT INTO configuration_sets (name) VALUES (?) RETURNING id`, "Config RT "+strconv.Itoa(rtID)).Scan(&configSetID); err != nil {
		t.Fatalf("seed configuration set: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO screens (name) VALUES (?) RETURNING id`, "Create RT "+strconv.Itoa(rtID)).Scan(&screenID); err != nil {
		t.Fatalf("seed create screen: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, workspaceID, configSetID); err != nil {
		t.Fatalf("bind workspace configuration: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id, create_screen_id)
		VALUES (?, ?, ?)
	`, configSetID, itemTypeID, screenID); err != nil {
		t.Fatalf("bind create screen: %v", err)
	}
	for _, identifier := range customFieldIdentifiers {
		fieldID, err := strconv.Atoi(identifier)
		if err != nil {
			t.Fatalf("custom field identifier %q must be numeric: %v", identifier, err)
		}
		if _, err := db.Exec(`
			INSERT INTO custom_field_definitions (id, name, field_type)
			VALUES (?, ?, 'text')
		`, fieldID, "Field "+identifier); err != nil {
			t.Fatalf("seed custom field %s: %v", identifier, err)
		}
		if _, err := db.Exec(`
			INSERT INTO screen_fields (screen_id, field_type, field_identifier)
			VALUES (?, 'custom', ?)
		`, screenID, identifier); err != nil {
			t.Fatalf("seed screen field %s: %v", identifier, err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO request_types (id, channel_id, name, item_type_id, workspace_id, config, is_active)
		VALUES (?, ?, ?, ?, ?, '{}', true)
	`, rtID, int(chID64), "RT-"+strconv.Itoa(rtID), itemTypeID, workspaceID); err != nil {
		t.Fatalf("seed request_type: %v", err)
	}
	display := 0
	for _, id := range customFieldIdentifiers {
		if _, err := db.Exec(`
			INSERT INTO request_type_fields (request_type_id, field_identifier, field_type, is_required, display_order)
			VALUES (?, ?, 'custom', ?, ?)
		`, rtID, id, requiredIdentifiers[id], display); err != nil {
			t.Fatalf("seed request_type_field %s: %v", id, err)
		}
		display++
	}
}

// F2: submitted custom field IDs that aren't configured on the request type
// must be dropped from the validation result, even when a custom field
// definition exists in the system. The dropped key must not reach
// storeCustomFieldValues.
func TestValidateAndSeparateRequestFields_DropsUnconfiguredCustomFields(t *testing.T) {
	db := newNegativeTestDB(t)
	const rtID = 100
	seedRequestTypeWithCustomFields(t, db, rtID, []string{"42"}, map[string]bool{"42": false})
	for _, identifier := range []string{"99", "7"} {
		if _, err := db.Exec(`
			INSERT INTO custom_field_definitions (id, name, field_type)
			VALUES (?, ?, 'text')
		`, identifier, "Hidden field "+identifier); err != nil {
			t.Fatalf("seed hidden custom field %s: %v", identifier, err)
		}
	}

	rt := rtID
	customFields := map[string]interface{}{
		"42": "allowed-value",  // configured on the RT → kept
		"99": "tampered-value", // NOT configured → must be dropped
		"7":  "another-hidden", // NOT configured → must be dropped
	}

	res, err := services.ValidateAndSeparateRequestFields(context.Background(), db, &rt, "Title", "Desc", customFields)
	if err != nil {
		t.Fatalf("validateAndSeparateFields: %v", err)
	}
	if got, want := res.CustomFieldValues["42"], "allowed-value"; got != want {
		t.Errorf("configured field 42 = %v, want %q", got, want)
	}
	if _, present := res.CustomFieldValues["99"]; present {
		t.Errorf("unconfigured field 99 must not appear in customFieldValues; got %v", res.CustomFieldValues["99"])
	}
	if _, present := res.CustomFieldValues["7"]; present {
		t.Errorf("unconfigured field 7 must not appear in customFieldValues; got %v", res.CustomFieldValues["7"])
	}
}

// F2 negative path: submission must succeed (no 400) when extra fields are
// included — silent drop only. A 400 would act as an oracle telling probers
// which custom field IDs the request type accepts.
func TestValidateAndSeparateRequestFields_UnconfiguredFieldDoesNotError(t *testing.T) {
	db := newNegativeTestDB(t)
	const rtID = 101
	seedRequestTypeWithCustomFields(t, db, rtID, []string{"5"}, map[string]bool{"5": false})

	rt := rtID
	_, err := services.ValidateAndSeparateRequestFields(context.Background(), db, &rt, "Title", "Desc", map[string]interface{}{
		"5":      "ok",
		"hidden": "tampered",
	})
	if err != nil {
		t.Fatalf("expected silent drop, got error: %v", err)
	}
}

// F3: required-field validation must reject blank values for arrays, objects,
// and whitespace strings — what JSON `[]`, `{}`, and `"   "` unmarshal to.
func TestValidateAndSeparateRequestFields_RequiredRejectsBlankComposites(t *testing.T) {
	db := newNegativeTestDB(t)
	const rtID = 102
	const fieldID = "43"
	seedRequestTypeWithCustomFields(t, db, rtID, []string{fieldID}, map[string]bool{fieldID: true})
	if _, err := db.Exec(`UPDATE request_type_fields SET field_type = 'virtual', virtual_field_type = 'text' WHERE request_type_id = ?`, rtID); err != nil {
		t.Fatalf("make required field virtual: %v", err)
	}
	rt := rtID

	cases := []struct {
		name  string
		value interface{}
	}{
		{"nil", nil},
		{"empty-string", ""},
		{"whitespace", "   \t\n"},
		{"empty-array", []interface{}{}},
		{"empty-object", map[string]interface{}{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := services.ValidateAndSeparateRequestFields(context.Background(), db, &rt, "Title", "Desc", map[string]interface{}{fieldID: tc.value})
			if err == nil {
				t.Fatalf("expected required-field error for blank %s value, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), "field "+fieldID+" is required") {
				t.Errorf("expected required error mentioning field; got %v", err)
			}
		})
	}
}

func TestValidateAndSeparateRequestFields_RequiredBooleanAllowsEmptyTrueAndFalse(t *testing.T) {
	db := newNegativeTestDB(t)
	const rtID = 103
	const fieldID = "44"
	seedRequestTypeWithCustomFields(t, db, rtID, []string{fieldID}, map[string]bool{fieldID: true})
	if _, err := db.Exec(`UPDATE custom_field_definitions SET field_type = 'boolean' WHERE id = ?`, fieldID); err != nil {
		t.Fatalf("make required field boolean: %v", err)
	}
	rt := rtID

	for name, values := range map[string]map[string]interface{}{
		"missing": nil,
		"null":    {fieldID: nil},
		"false":   {fieldID: false},
		"true":    {fieldID: true},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := services.ValidateAndSeparateRequestFields(context.Background(), db, &rt, "Title", "Desc", values); err != nil {
				t.Fatalf("required boolean %s should be valid: %v", name, err)
			}
		})
	}
}

// F3 helper contract: false, 0, and populated composites are not generically
// blank. Type-specific virtual-field validation decides whether each shape is
// valid for the configured field.
func TestIsBlankSubmittedField_AcceptsFalsyAndNonEmpty(t *testing.T) {
	cases := []struct {
		name  string
		value interface{}
	}{
		{"false", false},
		{"zero-int", 0},
		{"zero-float", 0.0},
		{"populated-array", []interface{}{"a"}},
		{"populated-object", map[string]interface{}{"k": "v"}},
		{"non-empty-string", "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if services.IsBlankSubmittedField(tc.value) {
				t.Errorf("%s value was classified as blank", tc.name)
			}
		})
	}
}
