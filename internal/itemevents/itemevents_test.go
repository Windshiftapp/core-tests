//go:build test

package itemevents

import (
	"reflect"
	"testing"

	"windshift/internal/models"
)

func TestChangesReturnsStableTypedCanonicalFields(t *testing.T) {
	oldStatus, newStatus := 2, 3
	oldParent := 10
	before := &models.Item{
		ID: 7, WorkspaceID: 1, WorkspaceItemNumber: 9,
		Title: "Before", StatusID: &oldStatus, ParentID: &oldParent,
		CustomFieldValues: map[string]any{"20": "old", "10": float64(1)},
	}
	after := *before
	after.Title = "After"
	after.StatusID = &newStatus
	after.ParentID = nil
	after.CustomFieldValues = map[string]any{"10": float64(2), "30": true}

	got := Changes(before, &after)
	want := []FieldChange{
		{Field: "title", OldValue: "Before", NewValue: "After"},
		{Field: "status_id", OldValue: 2, NewValue: 3},
		{Field: "parent_id", OldValue: 10, NewValue: nil},
		{Field: "cf_10", OldValue: float64(1), NewValue: float64(2)},
		{Field: "cf_20", OldValue: "old", NewValue: nil},
		{Field: "cf_30", OldValue: nil, NewValue: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Changes() = %#v, want %#v", got, want)
	}
}
