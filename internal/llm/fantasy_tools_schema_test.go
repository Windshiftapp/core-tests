//go:build test

package llm

import (
	"encoding/json"
	"testing"

	"charm.land/fantasy"
)

// Externally supplied tool schemas — the coding-agent runner decodes its own
// CompletionRequest — never pass through the aitools registry, so the provider
// boundary is the only place they can be canonicalized.
func TestToFantasyToolsAddsPropertiesToExternallySuppliedObjectSchema(t *testing.T) {
	tools := toFantasyTools([]ToolDefinition{{
		Type: "function",
		Function: FunctionDef{
			Name:        "read_file",
			Description: "read a file",
			Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
		},
	}}, false)
	if len(tools) != 1 {
		t.Fatalf("tool count = %d, want 1", len(tools))
	}
	ft, ok := tools[0].(fantasy.FunctionTool)
	if !ok {
		t.Fatalf("tool type = %T, want fantasy.FunctionTool", tools[0])
	}
	properties, ok := ft.InputSchema["properties"]
	if !ok {
		t.Fatalf("provider-facing schema has no properties: %#v", ft.InputSchema)
	}
	if _, ok := properties.(map[string]any); !ok {
		t.Fatalf("properties = %#v, want object", properties)
	}
}

func TestToFantasyToolsLeavesDeclaredPropertiesUntouched(t *testing.T) {
	raw := `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`
	tools := toFantasyTools([]ToolDefinition{{
		Type: "function",
		Function: FunctionDef{
			Name:       "read_file",
			Parameters: json.RawMessage(raw),
		},
	}}, false)
	ft, ok := tools[0].(fantasy.FunctionTool)
	if !ok {
		t.Fatalf("tool type = %T, want fantasy.FunctionTool", tools[0])
	}
	if _, ok := ft.InputSchema["properties"].(map[string]any)["path"]; !ok {
		t.Fatalf("declared properties were altered: %#v", ft.InputSchema)
	}
}
