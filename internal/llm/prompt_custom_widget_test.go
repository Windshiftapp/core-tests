//go:build test

package llm

import (
	"strings"
	"testing"

	"windshift/internal/customwidgets"
)

// TestCustomWidgetPromptCoversEveryComponent pins the prompt against the
// server allow-list. A component the server accepts but the prompt never
// mentions will simply never be generated, and one the prompt invents will be
// rejected at save time.
func TestCustomWidgetPromptCoversEveryComponent(t *testing.T) {
	prompt := NewPromptStore("").Get(PromptCustomWidget)
	if strings.TrimSpace(prompt) == "" {
		t.Fatal("embedded custom_widget prompt is empty")
	}

	for _, name := range customwidgets.ComponentNames() {
		if !strings.Contains(prompt, name) {
			t.Errorf("prompt does not mention the %s component", name)
		}
	}
}

// TestCustomWidgetPromptStatesTheLoadBearingRules covers the rules that are not
// recoverable from the component signatures: positional arguments, the query
// scope boundary, and the sort requirement for browser-side aggregation.
func TestCustomWidgetPromptStatesTheLoadBearingRules(t *testing.T) {
	prompt := NewWidgetPromptForTest(t)

	required := []struct {
		name   string
		needle string
	}{
		{name: "positional arguments", needle: "positional"},
		{name: "the scope boundary", needle: "Never put a workspace, user, or asset-set ID"},
		{name: "no invented ordering", needle: "Do not invent an order"},
		{name: "client-side aggregation", needle: "Aggregation is client-side"},
		{name: "the queries block", needle: "queries block"},
		{name: "CQL is the query language", needle: "CQL"},
	}
	for _, test := range required {
		if !strings.Contains(prompt, test.needle) {
			t.Errorf("prompt does not state %s (missing %q)", test.name, test.needle)
		}
	}
}

// NewWidgetPromptForTest returns the embedded prompt, failing the test when it
// is missing so a rename is caught here rather than at runtime.
func NewWidgetPromptForTest(t *testing.T) string {
	t.Helper()
	prompt := NewPromptStore("").Get(PromptCustomWidget)
	if strings.TrimSpace(prompt) == "" {
		t.Fatal("embedded custom_widget prompt is empty")
	}
	return prompt
}
