//go:build test

package v2

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpaqueCursorRoundTripIsKindBound(t *testing.T) {
	t.Parallel()
	want := agentRunCursorValue{ID: 42}
	raw := encodeOpaqueCursor("agent-runs", want)
	var got agentRunCursorValue
	if err := decodeOpaqueCursor("agent-runs", raw, &got); err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if got != want {
		t.Fatalf("decoded cursor = %+v, want %+v", got, want)
	}
	if err := decodeOpaqueCursor("agent-run-events", raw, &got); err == nil {
		t.Fatal("cursor from another operation must be rejected")
	}
}

func TestCommentCursorReplacesTimestampAndIDBoundaries(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 9, 4, 10, 11, 12, 0, time.UTC)
	raw := encodeOpaqueCursor("comments", commentCursor{Direction: "older", CreatedAt: createdAt, ID: 19})
	request := httptest.NewRequest("GET", "/items/1/comments?cursor="+raw+"&page_size=25", nil)
	options, err := parseCommentOptions(request)
	if err != nil {
		t.Fatalf("parse opaque cursor: %v", err)
	}
	if options.Limit != 25 || options.Before == nil || options.Before.ID != 19 || !options.Before.CreatedAt.Equal(createdAt) {
		t.Fatalf("comment options = %+v", options)
	}

	for _, query := range []string{"before=2026-09-04", "before_id=19", "since=2026-09-04", "since_id=19"} {
		request := httptest.NewRequest("GET", "/items/1/comments?"+query, nil)
		if _, err := parseCommentOptions(request); err == nil {
			t.Fatalf("legacy boundary %q must be rejected", query)
		}
	}
}

func TestAgentRunCursorRejectsRawIDBoundary(t *testing.T) {
	t.Parallel()
	raw := encodeOpaqueCursor("agent-runs", agentRunCursorValue{ID: 27})
	request := httptest.NewRequest("GET", "/workspaces/1/agent-runs?cursor="+raw+"&page_size=10", nil)
	limit, beforeID, err := agentRunCursor(request)
	if err != nil {
		t.Fatalf("parse opaque cursor: %v", err)
	}
	if limit != 10 || beforeID != 27 {
		t.Fatalf("limit, beforeID = %d, %d", limit, beforeID)
	}

	request = httptest.NewRequest("GET", "/workspaces/1/agent-runs?before_id=27", nil)
	if _, _, err := agentRunCursor(request); err == nil {
		t.Fatal("before_id must be rejected")
	}
}
