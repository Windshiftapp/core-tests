//go:build test

package gherkin

import (
	"strings"
	"testing"
)

func TestParseDocStringFenceEscapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		source      string
		wantContent string
	}{
		{
			name:        "escaped backtick fence embeds a fence line",
			source:      "Feature: F\nScenario: S\nGiven a doc\n```\nline1\n\\`\\`\\`\nline3\n```\nWhen x\n",
			wantContent: "line1\n```\nline3",
		},
		{
			name:        "escaped quote fence embeds a delimiter line",
			source:      "Feature: F\nScenario: S\nGiven a doc\n\"\"\"\n- \\\"item\\\"\n\"\"\"\nWhen x\n",
			wantContent: "- \"item\"",
		},
		{
			name:        "doubled backslash embeds one backslash",
			source:      "Feature: F\nScenario: S\nGiven a doc\n```\nC:\\\\path\n```\nWhen x\n",
			wantContent: "C:\\path",
		},
		{
			name:        "unknown escape keeps the backslash literal",
			source:      "Feature: F\nScenario: S\nGiven a doc\n```\nnot an escape \\n here\n```\nWhen x\n",
			wantContent: "not an escape \\n here",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc, errs := Parse(tt.source)
			if !errs.Empty() {
				t.Fatalf("Parse returned errors: %v", errs.Error())
			}
			docString := doc.Scenarios[0].Steps[0].DocString
			if docString == nil {
				t.Fatal("step has no doc string")
			}
			if docString.Content != tt.wantContent {
				t.Fatalf("doc string content = %q, want %q", docString.Content, tt.wantContent)
			}

			// The rendered source must re-parse to the same content instead of
			// truncating the block at an embedded fence.
			rendered := RenderSpec(doc.ScenarioSpecFrom(&doc.Scenarios[0]))
			reparsed, errs := Parse(rendered)
			if !errs.Empty() {
				t.Fatalf("re-parsing rendered source returned errors: %v\nrendered:\n%s", errs.Error(), rendered)
			}
			if len(reparsed.Scenarios) != 1 || len(reparsed.Scenarios[0].Steps) != 2 {
				t.Fatalf("re-parsed document lost structure: %+v", reparsed)
			}
			got := reparsed.Scenarios[0].Steps[0].DocString.Content
			if got != tt.wantContent {
				t.Fatalf("round-tripped doc string content = %q, want %q\nrendered:\n%s", got, tt.wantContent, rendered)
			}
		})
	}
}

func TestRenderSpecEscapesStoredFenceContent(t *testing.T) {
	t.Parallel()

	// A stored spec whose doc string content contains a fence line (reachable
	// via the persisted spec JSON) must render to a source that re-parses to
	// the same content instead of terminating the block early.
	spec := ScenarioSpec{
		FeatureName:     "F",
		ScenarioKeyword: "Scenario",
		ScenarioName:    "S",
		Steps: []Step{
			{Keyword: "Given", Text: "a doc", DocString: &DocString{Content: "line1\n```\nline3"}},
			{Keyword: "When", Text: "x"},
		},
	}

	rendered := RenderSpec(spec)
	reparsed, errs := Parse(rendered)
	if !errs.Empty() {
		t.Fatalf("re-parsing rendered source returned errors: %v\nrendered:\n%s", errs.Error(), rendered)
	}
	steps := reparsed.Scenarios[0].Steps
	if len(steps) != 2 {
		t.Fatalf("re-parsed steps = %d, want 2\nrendered:\n%s", len(steps), rendered)
	}
	if steps[0].DocString == nil || steps[0].DocString.Content != "line1\n```\nline3" {
		t.Fatalf("round-tripped doc string = %+v, want original content\nrendered:\n%s", steps[0].DocString, rendered)
	}
}

func TestValidatePlaceholdersPerExamplesBlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		source    string
		wantError string
	}{
		{
			name:      "placeholder provided by every block passes",
			source:    "Feature: F\nScenario Outline: S\nGiven <a> and <b>\nExamples: e1\n | a | b |\n | 1 | 2 |\nExamples: e2\n | a | b |\n | 3 | 4 |\n",
			wantError: "",
		},
		{
			name:      "column only in one block is reported for the other block",
			source:    "Feature: F\nScenario Outline: S\nGiven <a> and <b>\nExamples: e1\n | a |\n | 1 |\nExamples: e2\n | b |\n | 2 |\n",
			wantError: "line 4, column 1: Examples \"e1\" does not provide <b>: add the column or remove the placeholder\nline 7, column 1: Examples \"e2\" does not provide <a>: add the column or remove the placeholder",
		},
		{
			name:      "missing column in an unnamed block",
			source:    "Feature: F\nScenario Outline: S\nGiven <a>\nExamples:\n | x |\n | 1 |\n",
			wantError: "line 4, column 1: Examples does not provide <a>: add the column or remove the placeholder",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, errs := Parse(tt.source)
			got := ""
			if errs != nil && !errs.Empty() {
				got = errs.Error()
			}
			if got != tt.wantError {
				t.Fatalf("Parse errors = %q, want %q", got, tt.wantError)
			}
		})
	}
}

func TestTagsBeforeRejectedFeatureAreNotAttachedToNextScenario(t *testing.T) {
	t.Parallel()

	// The second Feature line is an error; its @x tag must not silently
	// attach to the following scenario.
	_, errs := Parse("Feature: A\n@x\nFeature: B\nScenario: S\nGiven a\n")
	if errs == nil || errs.Empty() {
		t.Fatal("Parse accepted a document with two Features")
	}
	messages := errs.Error()
	if !strings.Contains(messages, "only one Feature") {
		t.Fatalf("errors = %q, want the duplicate-Feature error", messages)
	}
	if strings.Contains(messages, "@x") {
		t.Fatalf("errors = %q, want the tag consumed without a misattribution error", messages)
	}
}
