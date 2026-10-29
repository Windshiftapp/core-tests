//go:build test

package services

import (
	"testing"

	"windshift/internal/repository"
)

// Custom fields that reference workspace-scoped entities, or cross-workspace
// items, cannot survive a workspace clone. The strict copy policy drops them
// rather than writing an id that is invalid or belongs to the source workspace.
func TestAcceptTemplateCustomFieldValueDropsWorkspaceScopedTypes(t *testing.T) {
	for _, fieldType := range []string{
		"user", "multi_user", "milestone", "iteration", "asset",
		"portalcustomer", "customerorganisation", "linking",
	} {
		t.Run(fieldType, func(t *testing.T) {
			def := repository.TemplateCustomFieldDef{FieldType: fieldType}
			if _, keep := acceptTemplateCustomFieldValue(def, float64(42)); keep {
				t.Fatalf("field type %q scalar value kept, want dropped", fieldType)
			}
			if _, keep := acceptTemplateCustomFieldValue(def, []any{float64(42)}); keep {
				t.Fatalf("field type %q multi value kept, want dropped", fieldType)
			}
		})
	}
}

// Portable scalar and option fields survive the clone; select options must
// still exist in the definition's current option set.
func TestAcceptTemplateCustomFieldValueKeepsPortableTypes(t *testing.T) {
	if got, keep := acceptTemplateCustomFieldValue(repository.TemplateCustomFieldDef{FieldType: "text"}, "hello"); !keep || got != "hello" {
		t.Fatalf("text = %v/%t, want hello/true", got, keep)
	}
	if got, keep := acceptTemplateCustomFieldValue(repository.TemplateCustomFieldDef{FieldType: "number"}, float64(3)); !keep || got != float64(3) {
		t.Fatalf("number = %v/%t, want 3/true", got, keep)
	}

	selectDef := repository.TemplateCustomFieldDef{
		FieldType: "select",
		Options:   `{"next_id":8,"items":[{"id":7,"label":"Seven"}]}`,
	}
	if _, keep := acceptTemplateCustomFieldValue(selectDef, float64(7)); !keep {
		t.Fatal("known select option dropped, want kept")
	}
	if _, keep := acceptTemplateCustomFieldValue(selectDef, float64(8)); keep {
		t.Fatal("unknown select option kept, want dropped")
	}
}
