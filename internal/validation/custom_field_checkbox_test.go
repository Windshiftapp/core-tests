//go:build test

package validation

import (
	"strings"
	"testing"

	"windshift/internal/testutils"
)

// TestValidateCheckboxValue pins the asset-aligned contract used by portal
// virtual fields and global boolean custom fields: empty, true, and false are
// valid, while non-empty values must be actual Go/JSON booleans.
func TestValidateCheckboxValue(t *testing.T) {
	tests := []struct {
		name    string
		raw     interface{}
		want    bool
		wantErr string
	}{
		{name: "true accepted", raw: true, want: true},
		{name: "false accepted", raw: false, want: false},
		{name: "nil accepted as empty", raw: nil, want: false},
		{name: "string true rejected", raw: "true", wantErr: "must be a boolean value"},
		{name: "string false rejected", raw: "false", wantErr: "must be a boolean value"},
		{name: "number one rejected", raw: 1, wantErr: "must be a boolean value"},
		{name: "number zero rejected", raw: 0, wantErr: "must be a boolean value"},
		{name: "list rejected", raw: []interface{}{true}, wantErr: "must be a boolean value"},
		{name: "object rejected", raw: map[string]interface{}{"value": true}, wantErr: "must be a boolean value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateCheckboxValue("7", tt.raw)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
			if got != tt.want {
				t.Errorf("expected value %v, got %v", tt.want, got)
			}
		})
	}
}

func TestValidateAndNormalizeCustomFieldValuesBooleanAliases(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	for _, fieldType := range []string{"boolean", "checkbox"} {
		fieldType := fieldType
		t.Run(fieldType, func(t *testing.T) {
			key := insertCustomField(t, tdb, "Approved "+fieldType, fieldType, "")

			for _, raw := range []interface{}{true, false, nil} {
				cfv := map[string]interface{}{key: raw}
				if err := ValidateAndNormalizeCustomFieldValues(db, cfv); err != nil {
					t.Fatalf("expected %#v to pass: %v", raw, err)
				}
				if cfv[key] != raw {
					t.Errorf("expected preserved %#v, got %#v", raw, cfv[key])
				}
			}

			rejected := map[string]interface{}{
				"true":   "true",
				"false":  "false",
				"one":    1,
				"zero":   0,
				"list":   []interface{}{true},
				"object": map[string]interface{}{"value": true},
			}
			for name, raw := range rejected {
				t.Run("rejects "+name, func(t *testing.T) {
					cfv := map[string]interface{}{key: raw}
					err := ValidateAndNormalizeCustomFieldValues(db, cfv)
					if err == nil {
						t.Fatalf("expected validation error for %T %v", raw, raw)
					}
					vErr, ok := err.(*ValidationError)
					if !ok {
						t.Fatalf("expected *ValidationError, got %T: %v", err, err)
					}
					if vErr.Field != "custom_field_values."+key {
						t.Errorf("expected field path custom_field_values.%s, got %q", key, vErr.Field)
					}
					if !strings.Contains(strings.ToLower(vErr.Message), "boolean") {
						t.Errorf("expected boolean-category message, got %q", vErr.Message)
					}
				})
			}
		})
	}
}
