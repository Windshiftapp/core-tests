//go:build test

package handlers

import (
	"testing"

	"windshift/internal/llm"
)

// TestReviewVerdictForToolCalls exercises the chat-side mapping from
// ToolCallRecord results to a review verdict, including the JSON soft-error
// shapes the aitools registry emits.
func TestReviewVerdictForToolCalls(t *testing.T) {
	t.Run("unrecovered unknown tool flags", func(t *testing.T) {
		v := reviewVerdictForToolCalls([]llm.ToolCallRecord{
			{Name: "frobnicate", Arguments: "{}", Result: `{"error": "unknown tool"}`},
		})
		if !v.Flagged {
			t.Fatal("want flagged")
		}
		if len(v.Reasons) == 0 {
			t.Error("want at least one reason")
		}
	})

	t.Run("recovered invalid args does not flag", func(t *testing.T) {
		v := reviewVerdictForToolCalls([]llm.ToolCallRecord{
			{Name: "get_item", Arguments: "{bad", Result: `{"error": "invalid arguments"}`},
			{Name: "get_item", Arguments: `{"id":5}`, Result: `{"id":5,"title":"x"}`},
		})
		if v.Flagged {
			t.Fatalf("want not flagged, got reasons %v", v.Reasons)
		}
	})

	t.Run("clean run does not flag", func(t *testing.T) {
		v := reviewVerdictForToolCalls([]llm.ToolCallRecord{
			{Name: "get_item", Arguments: `{"id":5}`, Result: `{"id":5}`},
		})
		if v.Flagged {
			t.Fatalf("want not flagged, got reasons %v", v.Reasons)
		}
	})
}
