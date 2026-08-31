//go:build test

package services

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"windshift/internal/testutils"
)

func seedVirtualRequestType(t *testing.T) (*testutils.TestDB, int) {
	t.Helper()
	db := testutils.CreateTestDB(t, true)
	if !testutils.IsPostgres() {
		t.Cleanup(func() { _ = db.Close() })
	}

	var workspaceID, itemTypeID, channelID, requestTypeID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Virtual requests', 'VREQ') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO item_types (name) VALUES ('Virtual request') RETURNING id`).Scan(&itemTypeID); err != nil {
		t.Fatalf("insert item type: %v", err)
	}
	channelConfig := fmt.Sprintf(`{"form_slug":"virtual","form_workspace_ids":[%d]}`, workspaceID)
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config, public_slug)
		VALUES ('Virtual form', 'form', 'inbound', 'enabled', ?, 'virtual') RETURNING id
	`, channelConfig).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO request_types (channel_id, name, item_type_id, workspace_id, is_active)
		VALUES (?, 'Virtual request', ?, ?, true) RETURNING id
	`, channelID, itemTypeID, workspaceID).Scan(&requestTypeID); err != nil {
		t.Fatalf("insert request type: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO request_type_fields
			(request_type_id, field_identifier, field_type, is_required, display_order, virtual_field_type, virtual_field_options)
		VALUES
			(?, 'title', 'default', true, 1, NULL, NULL),
			(?, 'note', 'virtual', false, 2, 'text', NULL),
			(?, 'details', 'virtual', false, 3, 'textarea', NULL),
			(?, 'urgency', 'virtual', true, 4, 'select', '[{"value":"low","label":"Low"},{"value":"high","label":"High"}]'),
			(?, 'confirmed', 'virtual', true, 5, 'checkbox', NULL)
	`, requestTypeID, requestTypeID, requestTypeID, requestTypeID, requestTypeID); err != nil {
		t.Fatalf("insert request type fields: %v", err)
	}
	return db, requestTypeID
}

func TestValidateAndSeparateRequestFieldsNormalizesVirtualFields(t *testing.T) {
	db, requestTypeID := seedVirtualRequestType(t)

	result, err := ValidateAndSeparateRequestFields(context.Background(), db, &requestTypeID, "Printer", "", map[string]interface{}{
		"note":      "  <b>Desk</b>  ",
		"details":   "<script>alert(1)</script>More detail",
		"urgency":   "high",
		"confirmed": true,
		"unknown":   "must not persist",
	})
	if err != nil {
		t.Fatalf("ValidateAndSeparateRequestFields: %v", err)
	}
	if _, exists := result.VirtualFieldValues["unknown"]; exists {
		t.Fatal("unknown virtual field was persisted")
	}
	if got := result.VirtualFieldValues["urgency"]; got != "high" {
		t.Fatalf("urgency = %#v, want high", got)
	}
	if got := result.VirtualFieldValues["confirmed"]; got != true {
		t.Fatalf("confirmed = %#v, want true", got)
	}
	if got := result.VirtualFieldValues["note"]; got == "  <b>Desk</b>  " {
		t.Fatalf("note was not normalized: %#v", got)
	}
	if got := result.VirtualFieldValues["details"]; reflect.DeepEqual(got, "<script>alert(1)</script>More detail") {
		t.Fatalf("details was not sanitized: %#v", got)
	}
}

func TestValidateAndSeparateRequestFieldsRejectsInvalidVirtualValues(t *testing.T) {
	db, requestTypeID := seedVirtualRequestType(t)
	valid := map[string]interface{}{"urgency": "low", "confirmed": true}

	tests := []struct {
		name   string
		values map[string]interface{}
	}{
		{name: "text object", values: map[string]interface{}{"note": map[string]interface{}{"bad": true}, "urgency": "low", "confirmed": true}},
		{name: "textarea boolean", values: map[string]interface{}{"details": true, "urgency": "low", "confirmed": true}},
		{name: "checkbox string", values: map[string]interface{}{"urgency": "low", "confirmed": "true"}},
		{name: "invalid select option", values: map[string]interface{}{"urgency": "critical", "confirmed": true}},
		{name: "select object", values: map[string]interface{}{"urgency": map[string]interface{}{"value": "low"}, "confirmed": true}},
		{name: "missing required select", values: map[string]interface{}{"confirmed": true}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ValidateAndSeparateRequestFields(context.Background(), db, &requestTypeID, "Printer", "", test.values)
			if err == nil {
				t.Fatalf("invalid virtual values unexpectedly passed: %#v", test.values)
			}
		})
	}

	if _, err := ValidateAndSeparateRequestFields(context.Background(), db, &requestTypeID, "Printer", "", valid); err != nil {
		t.Fatalf("valid control failed: %v", err)
	}
	for name, values := range map[string]map[string]interface{}{
		"required checkbox false":   {"urgency": "low", "confirmed": false},
		"required checkbox missing": {"urgency": "low"},
		"required checkbox null":    {"urgency": "low", "confirmed": nil},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateAndSeparateRequestFields(context.Background(), db, &requestTypeID, "Printer", "", values); err != nil {
				t.Fatalf("empty/false checkbox should be valid: %v", err)
			}
		})
	}
}
