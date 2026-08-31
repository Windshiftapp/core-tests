//go:build test

package services

import (
	"testing"

	"windshift/internal/models"
)

// WI-891: boolean is the canonical asset type and checkbox remains a
// compatibility alias. Non-empty values must still be actual booleans.
func TestAssetCheckboxFieldValueContract(t *testing.T) {
	for _, fieldType := range []string{"checkbox", "boolean"} {
		t.Run(fieldType, func(t *testing.T) {
			field := models.AssetTypeField{FieldType: fieldType}

			if err := validateAssetFieldValue(field, true); err != nil {
				t.Fatalf("true should validate: %v", err)
			}
			if err := validateAssetFieldValue(field, false); err != nil {
				t.Fatalf("false should validate: %v", err)
			}
			if err := validateAssetFieldValue(field, nil); err != nil {
				t.Fatalf("nil should validate as unset: %v", err)
			}
			if err := validateAssetFieldValue(field, "true"); err == nil {
				t.Fatalf("string \"true\" must be rejected")
			}
			if err := validateAssetFieldValue(field, 1); err == nil {
				t.Fatalf("number must be rejected")
			}
		})
	}
}

func TestRequiredAssetBooleanAllowsEmptyTrueAndFalse(t *testing.T) {
	field := models.AssetTypeField{
		CustomFieldID: 7,
		FieldName:     "Approved",
		FieldType:     "boolean",
		IsRequired:    true,
	}
	for _, values := range []map[string]interface{}{
		{},
		{"7": nil},
		{"7": false},
		{"7": true},
	} {
		if err := validateCustomFieldsSchemaCore(
			[]models.AssetTypeField{field},
			values,
			CustomFieldsValidationOpts{EnforceRequired: true},
		); err != nil {
			t.Fatalf("boolean value %#v should satisfy requiredness: %v", values, err)
		}
	}
}

func TestAssetCheckboxFieldValueCoercion(t *testing.T) {
	field := models.AssetTypeField{FieldType: "checkbox"}

	if got := coerceAssetFieldValue(field, true); got != true {
		t.Fatalf("bool true should pass through, got %#v", got)
	}
	if got := coerceAssetFieldValue(field, "true"); got != true {
		t.Fatalf("string \"true\" should coerce to bool, got %#v", got)
	}
	if got := coerceAssetFieldValue(field, "FALSE"); got != false {
		t.Fatalf("string \"FALSE\" should coerce to false, got %#v", got)
	}
	if got := coerceAssetFieldValue(field, "maybe"); got != "maybe" {
		t.Fatalf("ambiguous string should pass through for later rejection, got %#v", got)
	}
}
