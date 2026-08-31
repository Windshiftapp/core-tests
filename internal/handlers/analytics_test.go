package handlers

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseAnalyticsDateRangeDefaultsToTwelveWeeks(t *testing.T) {
	start, end, err := parseAnalyticsDateRange(url.Values{}, time.Date(2026, 7, 17, 23, 30, 0, 0, time.FixedZone("CEST", 2*60*60)))
	if err != nil {
		t.Fatalf("parseAnalyticsDateRange: %v", err)
	}
	if got := start.Format("2006-01-02"); got != "2026-04-25" {
		t.Fatalf("default start = %s, want 2026-04-25", got)
	}
	if got := end.Format("2006-01-02"); got != "2026-07-17" {
		t.Fatalf("default end = %s, want 2026-07-17", got)
	}
	if days := int(end.Sub(start).Hours()/24) + 1; days != analyticsDefaultRangeDays {
		t.Fatalf("default inclusive days = %d, want %d", days, analyticsDefaultRangeDays)
	}
}

func TestParseAnalyticsDateRangeRejectsInvalidAndUnboundedRanges(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		values url.Values
		want   string
	}{
		{
			name:   "invalid start",
			values: url.Values{"start_date": {"17-07-2026"}},
			want:   "start_date must use YYYY-MM-DD",
		},
		{
			name: "reversed",
			values: url.Values{
				"start_date": {"2026-07-18"},
				"end_date":   {"2026-07-17"},
			},
			want: "start_date must be on or before end_date",
		},
		{
			name: "too long",
			values: url.Values{
				"start_date": {"2025-07-16"},
				"end_date":   {"2026-07-17"},
			},
			want: "date range cannot exceed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := parseAnalyticsDateRange(test.values, now)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}
