package handlers

import (
	"strings"
	"testing"

	"windshift/internal/jira"
)

func adfRichTextDoc() map[string]any {
	return map[string]any{
		"type":    "doc",
		"version": 1,
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{"type": "text", "text": "Deployment "},
					map[string]any{
						"type":  "text",
						"text":  "notes",
						"marks": []any{map[string]any{"type": "strong"}},
					},
				},
			},
			map[string]any{
				"type": "bulletList",
				"content": []any{
					map[string]any{
						"type": "listItem",
						"content": []any{
							map[string]any{
								"type":    "paragraph",
								"content": []any{map[string]any{"type": "text", "text": "step one"}},
							},
						},
					},
				},
			},
		},
	}
}

// TestExtractCustomFieldValueRendersADFTextareaAsMarkdown is the WI-1708
// regression: a native textarea mapping over an ADF document must render
// readable Markdown instead of JSON-marshaling the document. Plain strings,
// empty/malformed documents, and explicit PreserveRaw mappings keep their
// documented behavior.
func TestExtractCustomFieldValueRendersADFTextareaAsMarkdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		value     any
		preserve  bool
		wantOK    bool
		wantExact string
		wantHas   []string
		wantNot   []string
	}{
		{
			name:    "ADF document renders as Markdown",
			value:   adfRichTextDoc(),
			wantOK:  true,
			wantHas: []string{"Deployment", "**notes**", "step one"},
			wantNot: []string{`"type":"doc"`, `"content"`},
		},
		{
			name:      "plain string is unchanged",
			value:     "Plain text value",
			wantOK:    true,
			wantExact: "Plain text value",
		},
		{
			name:   "empty ADF document is omitted",
			value:  map[string]any{"type": "doc", "version": 1, "content": []any{}},
			wantOK: false,
		},
		{
			name:   "malformed ADF document is omitted",
			value:  map[string]any{"type": "doc", "version": 1, "content": "not a list"},
			wantOK: false,
		},
		{
			name:      "explicit PreserveRaw keeps JSON",
			value:     adfRichTextDoc(),
			preserve:  true,
			wantOK:    true,
			wantHas:   []string{`"type":"doc"`},
			wantNot:   []string{"**notes**"},
		},
		{
			name:      "non-ADF option object still uses its label",
			value:     map[string]any{"value": "Red"},
			wantOK:    true,
			wantExact: "Red",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fields := &jira.JiraIssueFields{CustomFields: map[string]any{"customfield_rich": tt.value}}
			got, ok := extractCustomFieldValueWithOptions(CustomFieldMapping{
				JiraID:        "customfield_rich",
				WindshiftType: "textarea",
				Action:        "create",
				PreserveRaw:   tt.preserve,
			}, fields, nil, nil, nil)
			if ok != tt.wantOK {
				t.Fatalf("resolved = %v, want %v (value %#v)", ok, tt.wantOK, got)
			}
			if !ok {
				return
			}
			s, isString := got.(string)
			if !isString {
				t.Fatalf("value = %#v, want a string", got)
			}
			if tt.wantExact != "" && s != tt.wantExact {
				t.Fatalf("value = %q, want %q", s, tt.wantExact)
			}
			for _, want := range tt.wantHas {
				if !strings.Contains(s, want) {
					t.Errorf("value = %q, want to contain %q", s, want)
				}
			}
			for _, notWant := range tt.wantNot {
				if strings.Contains(s, notWant) {
					t.Errorf("value = %q, must not contain %q", s, notWant)
				}
			}
		})
	}
}
