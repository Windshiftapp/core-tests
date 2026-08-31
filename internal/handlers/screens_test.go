package handlers

import (
	"testing"

	"windshift/internal/models"
)

func TestEnsureAlwaysVisibleScreenFieldsAddsDescriptionBetweenTitleAndStatus(t *testing.T) {
	fields := []models.ScreenField{
		{FieldType: "system", FieldIdentifier: "title", DisplayOrder: 7},
		{FieldType: "system", FieldIdentifier: "status", DisplayOrder: 8},
		{FieldType: "custom", FieldIdentifier: "42", DisplayOrder: 9},
	}

	got := ensureAlwaysVisibleScreenFields(12, fields)
	gotIdentifiers := fieldIdentifiers(got)
	wantIdentifiers := []string{"title", "description", "status", "42"}
	if !equalStrings(gotIdentifiers, wantIdentifiers) {
		t.Fatalf("identifiers = %#v, want %#v", gotIdentifiers, wantIdentifiers)
	}

	for i, field := range got {
		if field.ScreenID != 12 {
			t.Fatalf("field %d screen id = %d, want 12", i, field.ScreenID)
		}
		if field.DisplayOrder != i {
			t.Fatalf("field %d display order = %d, want %d", i, field.DisplayOrder, i)
		}
	}
}

func TestEnsureAlwaysVisibleScreenFieldsAddsAllLockedFieldsToCustomOnlyScreen(t *testing.T) {
	fields := []models.ScreenField{
		{FieldType: "custom", FieldIdentifier: "42"},
	}

	got := ensureAlwaysVisibleScreenFields(12, fields)
	gotIdentifiers := fieldIdentifiers(got)
	wantIdentifiers := []string{"title", "description", "status", "42"}
	if !equalStrings(gotIdentifiers, wantIdentifiers) {
		t.Fatalf("identifiers = %#v, want %#v", gotIdentifiers, wantIdentifiers)
	}
}

func TestEnsureAlwaysVisibleScreenFieldsNormalizesStatusRequiredFalse(t *testing.T) {
	fields := []models.ScreenField{
		{FieldType: "system", FieldIdentifier: "title", IsRequired: false, FieldWidth: "half"},
		{FieldType: "system", FieldIdentifier: "description", IsRequired: true, FieldWidth: "half"},
		{FieldType: "system", FieldIdentifier: "status", IsRequired: true, FieldWidth: "full"},
	}

	got := ensureAlwaysVisibleScreenFields(12, fields)
	if !got[0].IsRequired || got[0].FieldWidth != "full" {
		t.Fatalf("title = required %v width %q, want required true width full", got[0].IsRequired, got[0].FieldWidth)
	}
	if got[1].IsRequired || got[1].FieldWidth != "full" {
		t.Fatalf("description = required %v width %q, want required false width full", got[1].IsRequired, got[1].FieldWidth)
	}
	if got[2].IsRequired || got[2].FieldWidth != "half" {
		t.Fatalf("status = required %v width %q, want required false width half", got[2].IsRequired, got[2].FieldWidth)
	}
}

func fieldIdentifiers(fields []models.ScreenField) []string {
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		out = append(out, field.FieldIdentifier)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
