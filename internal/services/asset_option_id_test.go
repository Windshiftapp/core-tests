//go:build test

package services

import (
	"math"
	"testing"

	"windshift/internal/models"
)

// WI-1824: asset select/multiselect option ids are integers. A fractional
// number must not be truncated into a matching option id.
func TestAssetFieldOptionAllowedRejectsFractionalOptionIDs(t *testing.T) {
	field := models.AssetTypeField{
		CustomFieldID: 1,
		FieldName:     "Priority",
		FieldType:     "select",
		Options:       `{"next_id":3,"items":[{"id":1,"label":"Low"},{"id":2,"label":"High"}]}`,
	}

	allowed := []struct {
		name  string
		value any
	}{
		{name: "int option id", value: 1},
		{name: "int64 option id", value: int64(2)},
		{name: "integral float option id", value: float64(2)},
		{name: "numeric string option id", value: "1"},
		{name: "legacy option label", value: "High"},
	}
	for _, tt := range allowed {
		t.Run("allows "+tt.name, func(t *testing.T) {
			if !assetFieldOptionAllowed(field, tt.value) {
				t.Fatalf("value %#v (%T) should be allowed", tt.value, tt.value)
			}
		})
	}

	rejected := []struct {
		name  string
		value any
	}{
		{name: "fractional float near a lower option id", value: 1.5},
		{name: "fractional float near a higher option id", value: 2.9},
		{name: "NaN", value: math.NaN()},
		{name: "positive infinity", value: math.Inf(1)},
		{name: "fractional numeric string", value: "1.5"},
		{name: "unknown integral id", value: float64(99)},
		{name: "unknown label", value: "Urgent"},
	}
	for _, tt := range rejected {
		t.Run("rejects "+tt.name, func(t *testing.T) {
			if assetFieldOptionAllowed(field, tt.value) {
				t.Fatalf("value %#v (%T) should be rejected", tt.value, tt.value)
			}
		})
	}
}

// A field with no options, or malformed options JSON, stays fail-open.
func TestAssetFieldOptionAllowedFailsOpen(t *testing.T) {
	for _, options := range []string{"", "not-json"} {
		t.Run("options="+options, func(t *testing.T) {
			field := models.AssetTypeField{FieldType: "select", Options: options}
			if !assetFieldOptionAllowed(field, 1.5) {
				t.Fatalf("field with options %q should accept any value", options)
			}
		})
	}
}
