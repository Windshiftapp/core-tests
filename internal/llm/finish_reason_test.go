//go:build test

package llm

import (
	"testing"

	"charm.land/fantasy"
)

// WI-1801: Fantasy emits the canonical finish reason "content-filter"; the
// adapter must normalize it to this package's "content_filter" spelling so the
// agent loop classifies a filtered response as StopReasonContentFilter instead
// of falling through to done/empty.

func TestContentFilterFinishReasonReachesTheLoop(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "with partial text", content: "partial answer before the filter"},
		{name: "without text", content: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := normalizedFantasyResponse(tc.content, nil, nil, Usage{}, fantasy.FinishReasonContentFilter)
			if got := resp.Choices[0].FinishReason; got != "content_filter" {
				t.Fatalf("adapter finish reason = %q, want %q", got, "content_filter")
			}
			if got := finalStopReason(resp.Choices[0].FinishReason, tc.content); got != StopReasonContentFilter {
				t.Fatalf("final stop reason = %q, want %q", got, StopReasonContentFilter)
			}
		})
	}
}

func TestToolCallFinishReasonStillNormalizes(t *testing.T) {
	resp := normalizedFantasyResponse("", nil, nil, Usage{}, fantasy.FinishReasonToolCalls)
	if got := resp.Choices[0].FinishReason; got != "tool_calls" {
		t.Fatalf("adapter finish reason = %q, want %q", got, "tool_calls")
	}
}

func TestUnfilteredFinishReasonStaysDone(t *testing.T) {
	resp := normalizedFantasyResponse("answer", nil, nil, Usage{}, fantasy.FinishReasonStop)
	if got := finalStopReason(resp.Choices[0].FinishReason, "answer"); got != StopReasonDone {
		t.Fatalf("final stop reason = %q, want %q", got, StopReasonDone)
	}
}
