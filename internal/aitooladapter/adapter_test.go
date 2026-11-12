package aitooladapter

import (
	"context"
	"encoding/json"
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

func TestBuildToolsEmitsObjectPropertiesForEveryTool(t *testing.T) {
	// llama.cpp (and therefore LM Studio) rejects a function whose parameters
	// schema has no "properties" member, which is how a no-arg Args struct
	// marshals unless the registry normalizes it.
	for _, tool := range BuildTools(aitools.Default.All()) {
		t.Run(tool.Function.Name, func(t *testing.T) {
			var schema map[string]json.RawMessage
			if err := json.Unmarshal(tool.Function.Parameters, &schema); err != nil {
				t.Fatalf("parameters is not an object: %v", err)
			}
			properties, ok := schema["properties"]
			if !ok {
				t.Fatalf("parameters has no properties member: %s", tool.Function.Parameters)
			}
			var props map[string]json.RawMessage
			if err := json.Unmarshal(properties, &props); err != nil {
				t.Fatalf("properties is not an object: %v", err)
			}
		})
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
