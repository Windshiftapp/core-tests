//go:build test

package handlers

import (
	"testing"

	"windshift/internal/jira"
)

// TestJiraNextAssetObjectOffsetAdvancesByConsumedRows is the WI-2070
// regression: a server-capped short non-final page must advance the walk by the
// rows it actually returned instead of by the requested page size.
func TestJiraNextAssetObjectOffsetAdvancesByConsumedRows(t *testing.T) {
	t.Parallel()

	entries := func(n int) []jira.AssetObject {
		out := make([]jira.AssetObject, n)
		return out
	}

	tests := []struct {
		name        string
		startAt     int
		result      *jira.ObjectSearchResult
		wantNext    int
		wantAdvance bool
	}{
		{
			name:    "short first page advances by returned rows",
			startAt: 0,
			result: &jira.ObjectSearchResult{
				ObjectEntries: entries(25), StartIndex: 0, ToIndex: 25,
			},
			wantNext:    25,
			wantAdvance: true,
		},
		{
			name:    "short later page advances by returned rows",
			startAt: 25,
			result: &jira.ObjectSearchResult{
				ObjectEntries: entries(25), StartIndex: 25, ToIndex: 50,
			},
			wantNext:    50,
			wantAdvance: true,
		},
		{
			name:    "missing response start falls back to requested offset",
			startAt: 25,
			result: &jira.ObjectSearchResult{
				ObjectEntries: entries(25), StartIndex: 0, ToIndex: 0,
			},
			wantNext:    50,
			wantAdvance: true,
		},
		{
			name:    "non-advancing page is rejected",
			startAt: 25,
			result: &jira.ObjectSearchResult{
				ObjectEntries: entries(0), StartIndex: 25, ToIndex: 25,
			},
			wantAdvance: false,
		},
		{
			name:        "nil result is rejected",
			startAt:     0,
			result:      nil,
			wantAdvance: false,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			next, advanced := jiraNextAssetObjectOffset(test.startAt, test.result)
			if advanced != test.wantAdvance {
				t.Fatalf("advanced = %t, want %t", advanced, test.wantAdvance)
			}
			if advanced && next != test.wantNext {
				t.Fatalf("next = %d, want %d", next, test.wantNext)
			}
		})
	}
}
