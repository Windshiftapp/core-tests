package aitooladapter

import (
	"context"
	"slices"
	"testing"

	"windshift/internal/aitools"
)

func TestEntriesForStandardUsesMandatoryPresetAndFiltersUnsafeAccess(t *testing.T) {
	entries := EntriesForStandard(aitools.Default, []string{string(aitools.CapabilityIssueManagement)})
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
		if entry.Access == aitools.AccessDestructive || entry.Access == aitools.AccessAdmin {
			t.Fatalf("unsafe entry %q admitted with access %q", entry.Name, entry.Access)
		}
	}
	for _, mandatory := range []string{"get_item", "list_comments", "add_comment"} {
		if !slices.Contains(names, mandatory) {
			t.Errorf("mandatory tool %q was not admitted: %v", mandatory, names)
		}
	}
	if !slices.Contains(names, "update_item") {
		t.Fatalf("selected issue-management tool was not admitted: %v", names)
	}
	for _, excluded := range []string{"delete_item", "delete_comment", "grant_page_permission"} {
		if slices.Contains(names, excluded) {
			t.Errorf("unsafe tool %q was admitted", excluded)
		}
	}
}

func TestExecutorRejectsToolsOutsideItsAdmissionSnapshot(t *testing.T) {
	executor := NewExecutor(&aitools.Env{}, nil)
	got, err := executor.Execute(context.Background(), "get_item", `{"item_id":1}`)
	if err != nil {
		t.Fatalf("execute rejected tool: %v", err)
	}
	if got != `{"error":"tool is not available to this agent"}` {
		t.Fatalf("rejection = %s", got)
	}
}
