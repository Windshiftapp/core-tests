//go:build test

package services

import (
	"strings"
	"testing"
	"time"
)

// civilWorklogDate resolves a date in the named timezone for tests.
func civilWorklogDate(t *testing.T, date, timezone string) time.Time {
	t.Helper()
	_, location, err := ResolveTimezone(timezone)
	if err != nil {
		t.Fatalf("ResolveTimezone(%s): %v", timezone, err)
	}
	parsed, err := ParseCivilDate(date, location)
	if err != nil {
		t.Fatalf("ParseCivilDate(%s): %v", date, err)
	}
	return parsed
}

func TestParseWorklogTimesExplicitClocksTakePrecedenceOverDuration(t *testing.T) {
	// Reproduces WI-1279: the duration branch used to run first and replace
	// the intended start with local midnight, storing midnight-based
	// timestamps instead of the supplied 16:00-18:00 clocks.
	date := civilWorklogDate(t, "2026-08-05", "America/Denver")

	minutes, start, end, err := ParseWorklogTimes(date, WorklogTimeInput{
		StartTime: "16:00", EndTime: "18:00", Duration: "2h",
	})
	if err != nil {
		t.Fatalf("ParseWorklogTimes: %v", err)
	}
	if got := time.Unix(start, 0).UTC().Format(time.RFC3339); got != "2026-08-05T22:00:00Z" {
		t.Fatalf("start = %s, want 2026-08-05T22:00:00Z", got)
	}
	if got := time.Unix(end, 0).UTC().Format(time.RFC3339); got != "2026-08-06T00:00:00Z" {
		t.Fatalf("end = %s, want 2026-08-06T00:00:00Z", got)
	}
	if minutes != 120 {
		t.Fatalf("duration = %d, want 120", minutes)
	}
}

func TestParseWorklogTimesRejectsDurationInconsistentWithClocks(t *testing.T) {
	date := civilWorklogDate(t, "2026-08-05", "America/Denver")

	_, _, _, err := ParseWorklogTimes(date, WorklogTimeInput{
		StartTime: "16:00", EndTime: "18:00", Duration: "3h",
	})
	if err == nil || !strings.Contains(err.Error(), "duration does not match") {
		t.Fatalf("error = %v, want duration mismatch rejection", err)
	}
}

func TestParseWorklogTimesStartPlusDurationComputesEnd(t *testing.T) {
	date := civilWorklogDate(t, "2026-08-05", "America/Denver")

	minutes, start, end, err := ParseWorklogTimes(date, WorklogTimeInput{
		StartTime: "16:00", Duration: "2h",
	})
	if err != nil {
		t.Fatalf("ParseWorklogTimes: %v", err)
	}
	if got := time.Unix(start, 0).UTC().Format(time.RFC3339); got != "2026-08-05T22:00:00Z" {
		t.Fatalf("start = %s, want 2026-08-05T22:00:00Z", got)
	}
	if got := time.Unix(end, 0).UTC().Format(time.RFC3339); got != "2026-08-06T00:00:00Z" {
		t.Fatalf("end = %s, want 2026-08-06T00:00:00Z", got)
	}
	if minutes != 120 {
		t.Fatalf("duration = %d, want 120", minutes)
	}
}

func TestParseWorklogTimesOvernightEndCarriesToNextDay(t *testing.T) {
	// Reproduces WI-1282: 23:00-01:00 used to be rejected as a negative
	// duration; the explicit next-day end must store one continuous interval.
	date := civilWorklogDate(t, "2026-08-05", "America/Denver")

	minutes, start, end, err := ParseWorklogTimes(date, WorklogTimeInput{
		StartTime: "23:00", EndTime: "01:00", Duration: "2h",
	})
	if err != nil {
		t.Fatalf("ParseWorklogTimes: %v", err)
	}
	if got := time.Unix(start, 0).UTC().Format(time.RFC3339); got != "2026-08-06T05:00:00Z" {
		t.Fatalf("start = %s, want 2026-08-06T05:00:00Z", got)
	}
	if got := time.Unix(end, 0).UTC().Format(time.RFC3339); got != "2026-08-06T07:00:00Z" {
		t.Fatalf("end = %s, want 2026-08-06T07:00:00Z", got)
	}
	if minutes != 120 {
		t.Fatalf("duration = %d, want 120", minutes)
	}
}

func TestParseWorklogTimesEqualClocksSpanTheFullDayCap(t *testing.T) {
	date := civilWorklogDate(t, "2026-08-05", "UTC")

	minutes, _, _, err := ParseWorklogTimes(date, WorklogTimeInput{
		StartTime: "22:00", EndTime: "22:00",
	})
	if err != nil {
		t.Fatalf("ParseWorklogTimes: %v", err)
	}
	if minutes != MaxWorklogDurationMinutes {
		t.Fatalf("duration = %d, want %d", minutes, MaxWorklogDurationMinutes)
	}
}

func TestParseWorklogTimesOvernightHonorsDSTOffsetOnEachDate(t *testing.T) {
	// The start on 2026-03-07 is MST while 03:00 on 2026-03-08 is MDT, so four
	// wall-clock hours span three elapsed hours across the spring-forward
	// boundary.
	date := civilWorklogDate(t, "2026-03-07", "America/Denver")

	minutes, start, end, err := ParseWorklogTimes(date, WorklogTimeInput{
		StartTime: "23:00", EndTime: "03:00",
	})
	if err != nil {
		t.Fatalf("ParseWorklogTimes: %v", err)
	}
	if got := time.Unix(start, 0).UTC().Format(time.RFC3339); got != "2026-03-08T06:00:00Z" {
		t.Fatalf("start = %s, want 2026-03-08T06:00:00Z", got)
	}
	if got := time.Unix(end, 0).UTC().Format(time.RFC3339); got != "2026-03-08T09:00:00Z" {
		t.Fatalf("end = %s, want 2026-03-08T09:00:00Z", got)
	}
	if minutes != 180 {
		t.Fatalf("duration = %d, want 180", minutes)
	}
}

func TestParseWorklogTimesOvernightResolvesRepeatedAndMissingTimesExplicitly(t *testing.T) {
	tests := []struct {
		name string
		date string
		want string
	}{
		{name: "fold on carried day", date: "2026-10-24", want: "ambiguous"},
		{name: "gap on carried day", date: "2026-03-28", want: "does not exist"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			date := civilWorklogDate(t, test.date, "Europe/Zurich")
			_, _, _, err := ParseWorklogTimes(date, WorklogTimeInput{
				StartTime: "23:00", EndTime: "02:30",
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q rejection", err, test.want)
			}
		})
	}
}

func TestParseWorklogTimesRejectsHalfSuppliedClockPair(t *testing.T) {
	date := civilWorklogDate(t, "2026-08-05", "UTC")

	for _, input := range []WorklogTimeInput{
		{StartTime: "09:00"},
		{EndTime: "17:00"},
		{StartTime: "09:00", DurationMinutes: 0},
	} {
		_, _, _, err := ParseWorklogTimes(date, input)
		if err == nil || !strings.Contains(err.Error(), "provide both start_time and end_time") {
			t.Fatalf("ParseWorklogTimes(%+v) error = %v, want half-pair rejection", input, err)
		}
	}
}
