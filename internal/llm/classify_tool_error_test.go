package llm

import (
	"reflect"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		tool   string
		output string
		want   ToolErrorClass
	}{
		// Coding-agent plain-text sentinels.
		{"agent unknown tool", "bogus", "(unknown tool: bogus)", ToolErrorUnknownTool},
		{"agent invalid args", "bash", "(tool arguments were not valid JSON: unexpected end)", ToolErrorInvalidArgs},
		{"agent plain success", "bash", "total 8\ndrwxr-xr-x", ToolErrorNone},
		{"agent empty", "bash", "", ToolErrorNone},

		// AI-chat JSON soft-error convention.
		{"chat unknown tool", "frobnicate", `{"error": "unknown tool"}`, ToolErrorUnknownTool},
		{"chat invalid args", "get_item", `{"error": "invalid arguments"}`, ToolErrorInvalidArgs},
		{"chat execution error", "get_item", `{"error": "item not found"}`, ToolErrorExecutionError},
		{"chat success json", "get_item", `{"id": 5, "title": "x"}`, ToolErrorNone},
		{"chat skipped dedupe", "create_item", `{"skipped":true,"reason":"duplicate terminal tool call suppressed"}`, ToolErrorSuppressed},

		// Transient network wording is excluded as noise (and must not be
		// mistaken for a generic execution error).
		{"transient i/o timeout", "get_item", `{"error": "dial tcp: i/o timeout"}`, ToolErrorTransient},
		{"transient conn refused", "get_item", `{"error": "connection refused"}`, ToolErrorTransient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.tool, tc.output); got != tc.want {
				t.Errorf("Classify(%q, %q) = %q, want %q", tc.tool, tc.output, got, tc.want)
			}
		})
	}
}

func TestEvaluateReview(t *testing.T) {
	cfg := DefaultReviewFlagConfig()

	t.Run("unrecovered unknown_tool flags", func(t *testing.T) {
		v := EvaluateReview([]ToolCallOutcome{
			{Tool: "bogus", Class: ToolErrorUnknownTool},
		}, cfg)
		if !v.Flagged {
			t.Fatal("want flagged")
		}
		if !reflect.DeepEqual(v.UnrecoveredClasses, []ToolErrorClass{ToolErrorUnknownTool}) {
			t.Errorf("UnrecoveredClasses = %v", v.UnrecoveredClasses)
		}
	})

	t.Run("recovered same-tool success does not flag", func(t *testing.T) {
		// A high-signal failure cleared by a later success of the SAME tool.
		v := EvaluateReview([]ToolCallOutcome{
			{Tool: "bash", Class: ToolErrorInvalidArgs},
			{Tool: "bash", Class: ToolErrorNone},
		}, cfg)
		if v.Flagged {
			t.Fatalf("want not flagged, got reasons %v", v.Reasons)
		}
	})

	t.Run("recovery only counts same tool", func(t *testing.T) {
		// A success of a DIFFERENT tool must not recover the failure.
		v := EvaluateReview([]ToolCallOutcome{
			{Tool: "bash", Class: ToolErrorInvalidArgs},
			{Tool: "read", Class: ToolErrorNone},
		}, cfg)
		if !v.Flagged {
			t.Fatal("want flagged: a different tool succeeding does not recover bash")
		}
	})

	t.Run("invalid_args thrash flags even when eventually recovered", func(t *testing.T) {
		v := EvaluateReview([]ToolCallOutcome{
			{Tool: "bash", Class: ToolErrorInvalidArgs},
			{Tool: "bash", Class: ToolErrorInvalidArgs},
			{Tool: "bash", Class: ToolErrorInvalidArgs},
			{Tool: "bash", Class: ToolErrorNone}, // recovers the last one
		}, cfg)
		if !v.Flagged {
			t.Fatal("want flagged: 3x invalid_args is thrash")
		}
	})

	t.Run("execution_error alone does not flag", func(t *testing.T) {
		v := EvaluateReview([]ToolCallOutcome{
			{Tool: "get_item", Class: ToolErrorExecutionError},
			{Tool: "get_item", Class: ToolErrorExecutionError},
		}, cfg)
		if v.Flagged {
			t.Fatalf("want not flagged, got reasons %v", v.Reasons)
		}
	})

	t.Run("suppressed-only does not flag", func(t *testing.T) {
		v := EvaluateReview([]ToolCallOutcome{
			{Tool: "create_item", Class: ToolErrorSuppressed},
		}, cfg)
		if v.Flagged {
			t.Fatalf("want not flagged, got reasons %v", v.Reasons)
		}
	})

	t.Run("empty run does not flag", func(t *testing.T) {
		if EvaluateReview(nil, cfg).Flagged {
			t.Fatal("want not flagged for an empty run")
		}
	})

	t.Run("both unrecovered classes reported deterministically", func(t *testing.T) {
		v := EvaluateReview([]ToolCallOutcome{
			{Tool: "ghost", Class: ToolErrorUnknownTool},
			{Tool: "bash", Class: ToolErrorInvalidArgs},
		}, cfg)
		if !v.Flagged {
			t.Fatal("want flagged")
		}
		want := []ToolErrorClass{ToolErrorUnknownTool, ToolErrorInvalidArgs}
		if !reflect.DeepEqual(v.UnrecoveredClasses, want) {
			t.Errorf("UnrecoveredClasses = %v, want %v", v.UnrecoveredClasses, want)
		}
	})
}
