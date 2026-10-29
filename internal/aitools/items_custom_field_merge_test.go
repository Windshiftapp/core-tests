//go:build test

package aitools

import (
	"testing"
)

// The update pipeline merges custom_field_values per field server-side, so
// update_item forwards exactly the caller's requested fields — no pre-read of
// the stored blob, no client-side merge.
func TestBuildUpdateDataForwardsRequestedCustomFieldValues(t *testing.T) {
	args := updateItemArgs{
		CustomFieldValues: map[string]any{"33": "High"},
	}

	updateData, changed, err := buildUpdateData(&Env{}, args, 7)
	if err != nil {
		t.Fatalf("buildUpdateData: %v", err)
	}

	got, ok := updateData["custom_field_values"].(map[string]any)
	if !ok {
		t.Fatalf("custom_field_values missing from update data: %v", updateData)
	}
	if len(got) != 1 || got["33"] != "High" {
		t.Fatalf("custom_field_values = %v, want map[33:High]", got)
	}

	// The update must still be reported as a custom-field change so callers
	// see why the item was touched.
	hasCustomFieldChange := false
	for _, name := range changed {
		if name == "custom_fields" {
			hasCustomFieldChange = true
		}
	}
	if !hasCustomFieldChange {
		t.Fatalf("changed = %v, want it to include custom_fields", changed)
	}
}

func TestBuildUpdateDataCustomFieldEdges(t *testing.T) {
	t.Run("no custom-field args produce no custom_field_values key", func(t *testing.T) {
		updateData, _, err := buildUpdateData(&Env{}, updateItemArgs{Title: strPtr("new title")}, 7)
		if err != nil {
			t.Fatalf("buildUpdateData: %v", err)
		}
		if _, present := updateData["custom_field_values"]; present {
			t.Fatalf("update data contains custom_field_values without a request for it: %v", updateData)
		}
	})
}

func strPtr(value string) *string {
	return &value
}
