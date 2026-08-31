package handlers

import (
	"net/http/httptest"
	"testing"
)

// TestParseHistoryPagination_RespectsQueryParams covers the v1 GetHistory
// regression where `limit` and `offset` were silently ignored. The cookie-auth
// handler already honored these params; this test pins that the v1 surface
// reads them with matching semantics (limit default 50, hard cap 200, offset
// must be non-negative).
func TestParseHistoryPagination_RespectsQueryParams(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantLimit  int
		wantOffset int
	}{
		{"defaults when absent", "", 50, 0},
		{"both supplied", "?limit=10&offset=10", 10, 10},
		{"offset alone", "?offset=25", 50, 25},
		{"limit alone", "?limit=5", 5, 0},
		{"limit over cap is ignored", "?limit=500", 50, 0},
		{"limit at cap accepted", "?limit=200", 200, 0},
		{"limit zero is ignored", "?limit=0", 50, 0},
		{"negative offset is ignored", "?offset=-3", 50, 0},
		{"non-numeric is ignored", "?limit=abc&offset=xyz", 50, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/x"+tc.raw, nil)
			gotLimit, gotOffset := parseHistoryPagination(req)
			if gotLimit != tc.wantLimit || gotOffset != tc.wantOffset {
				t.Errorf("got limit=%d offset=%d, want limit=%d offset=%d", gotLimit, gotOffset, tc.wantLimit, tc.wantOffset)
			}
		})
	}
}
