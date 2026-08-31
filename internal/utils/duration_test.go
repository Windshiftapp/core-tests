//go:build test

package utils

import (
	"testing"
	"time"
)

// TestParseDurationAcceptsDocumentedForms pins every shape ParseDuration
// advertises, so tightening the grammar can never quietly drop one.
func TestParseDurationAcceptsDocumentedForms(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  time.Duration
	}{
		{name: "Hours", input: "1h", want: time.Hour},
		{name: "FractionalHours", input: "0.5h", want: 30 * time.Minute},
		{name: "Minutes", input: "30m", want: 30 * time.Minute},
		{name: "FractionalMinutes", input: "1.5m", want: 90 * time.Second},
		{name: "HoursAndMinutes", input: "1h30m", want: 90 * time.Minute},
		{name: "HoursAndMinutesLong", input: "3h45m", want: 225 * time.Minute},
		{name: "Days", input: "1d", want: 8 * time.Hour},
		{name: "MultipleDays", input: "2d", want: 16 * time.Hour},
		{name: "FractionalDays", input: "0.5d", want: 4 * time.Hour},
		{name: "Uppercase", input: "1H30M", want: 90 * time.Minute},
		{name: "SurroundingWhitespace", input: "  2h  ", want: 2 * time.Hour},
		{name: "InternalWhitespace", input: "1h 30m", want: 90 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDuration(tt.input)
			if err != nil {
				t.Fatalf("ParseDuration(%q) returned error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestParseDurationRejectsPartialMatches is the regression guard for WI-944.
// The pattern used to be unanchored with both groups optional, so it matched a
// leading prefix and silently discarded the rest: "5h30" recorded 5h and the
// millisecond typo "90ms" recorded 90 minutes. A malformed duration must fail
// loudly — a silently wrong timesheet entry is not self-correcting, and the
// same parser backs the log_time MCP tool where an LLM supplies the string.
func TestParseDurationRejectsPartialMatches(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "BareTrailingNumber", input: "5h30"},
		{name: "MillisecondTypo", input: "90ms"},
		{name: "PluralisedUnit", input: "30mins"},
		{name: "UnsupportedSeconds", input: "2h15m20s"},
		{name: "SecondsOnly", input: "20s"},
		{name: "TrailingJunk", input: "5h junk"},
		{name: "RepeatedUnit", input: "5hh"},
		{name: "NoUnit", input: "1.5"},
		{name: "Negative", input: "-5h"},
		{name: "ExponentNotation", input: "1e5h"},
		{name: "UnitsOutOfOrder", input: "30m1h"},
		{name: "NonNumeric", input: "abc"},
		{name: "Empty", input: ""},
		{name: "ZeroHours", input: "0h"},
		{name: "MalformedDays", input: "1h30d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDuration(tt.input)
			if err == nil {
				t.Fatalf("ParseDuration(%q) = %v, want an error", tt.input, got)
			}
		})
	}
}
