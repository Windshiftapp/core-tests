//go:build test

package validation

import (
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// Custom field updates are a per-field patch merged server-side: payload keys
// set fields, null values clear them, and omitted keys are preserved. A
// partial payload must never wipe fields it did not carry.
func TestValidateAndApplyUpdatesMergesCustomFieldValues(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := setupTestData(t, tdb)
	validator := NewItemFieldValidator(tdb.GetDatabase())

	tests := []struct {
		name    string
		current map[string]any
		patch   map[string]any
		want    map[string]any
	}{
		{
			name:    "partial patch preserves untouched fields",
			current: map[string]any{"11": "keep", "22": float64(42)},
			patch:   map[string]any{"33": "added"},
			want:    map[string]any{"11": "keep", "22": float64(42), "33": "added"},
		},
		{
			name:    "payload key overwrites only its own field",
			current: map[string]any{"11": "old", "22": float64(42)},
			patch:   map[string]any{"11": "new"},
			want:    map[string]any{"11": "new", "22": float64(42)},
		},
		{
			name:    "null value clears a single field",
			current: map[string]any{"11": "old", "22": float64(42)},
			patch:   map[string]any{"11": nil},
			want:    map[string]any{"22": float64(42)},
		},
		{
			name:    "full map still sets every key",
			current: map[string]any{"11": "old"},
			patch:   map[string]any{"11": "new", "22": float64(7)},
			want:    map[string]any{"11": "new", "22": float64(7)},
		},
		{
			name:    "merge onto empty current values",
			current: nil,
			patch:   map[string]any{"11": "first"},
			want:    map[string]any{"11": "first"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := &models.Item{WorkspaceID: data.WorkspaceID, CustomFieldValues: tt.current}
			err := validator.ValidateAndApplyUpdates(item, map[string]any{
				"custom_field_values": tt.patch,
			}, data.UserID)
			if err != nil {
				t.Fatalf("ValidateAndApplyUpdates: %v", err)
			}
			if len(item.CustomFieldValues) != len(tt.want) {
				t.Fatalf("merged values = %v, want %v", item.CustomFieldValues, tt.want)
			}
			for key, want := range tt.want {
				if got := item.CustomFieldValues[key]; got != want {
					t.Errorf("merged[%q] = %v, want %v", key, got, want)
				}
			}
		})
	}
}

// A null custom_field_values payload is ignored, not a whole-blob wipe.
func TestValidateAndApplyUpdatesIgnoresNullCustomFieldPayload(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := setupTestData(t, tdb)
	validator := NewItemFieldValidator(tdb.GetDatabase())

	item := &models.Item{
		WorkspaceID:       data.WorkspaceID,
		CustomFieldValues: map[string]any{"11": "kept"},
	}
	err := validator.ValidateAndApplyUpdates(item, map[string]any{
		"custom_field_values": nil,
	}, data.UserID)
	if err != nil {
		t.Fatalf("ValidateAndApplyUpdates: %v", err)
	}
	if len(item.CustomFieldValues) != 1 || item.CustomFieldValues["11"] != "kept" {
		t.Fatalf("values after null payload = %v, want existing values preserved", item.CustomFieldValues)
	}
}
