//go:build test

package i18n

import (
	"testing"
	"time"
)

func TestFormatDate(t *testing.T) {
	// A single fixed instant rendered in UTC so the expected strings exercise
	// each locale's numeric order and separators.
	instant := time.Date(2026, 9, 5, 14, 30, 0, 0, time.UTC)
	tests := []struct {
		locale string
		want   string
	}{
		{locale: "en", want: "9/5/2026"},
		{locale: "de", want: "05.09.2026"},
		{locale: "es", want: "5/9/2026"},
		{locale: "ar", want: "5/9/2026"},
		{locale: "pt-BR", want: "5/9/2026"},
		{locale: "ru", want: "05.09.2026"},
		{locale: "zh-CN", want: "2026/9/5"},
		{locale: "fr", want: "05/09/2026"},
		{locale: "ko", want: "2026. 9. 5."},
		{locale: "it", want: "05/09/2026"},
		{locale: "pl", want: "05.09.2026"},
		{locale: "unknown", want: "9/5/2026"},
		{locale: "", want: "9/5/2026"},
	}
	for _, tc := range tests {
		t.Run(tc.locale, func(t *testing.T) {
			if got := FormatDate(tc.locale, instant, time.UTC); got != tc.want {
				t.Fatalf("FormatDate(%q) = %q, want %q", tc.locale, got, tc.want)
			}
		})
	}
}

func TestFormatTime(t *testing.T) {
	instant := time.Date(2026, 9, 5, 14, 30, 0, 0, time.UTC)
	tests := []struct {
		locale string
		want   string
	}{
		{locale: "en", want: "2:30 PM"},
		{locale: "de", want: "14:30"},
		{locale: "zh-CN", want: "14:30"},
		{locale: "pl", want: "14:30"},
	}
	for _, tc := range tests {
		t.Run(tc.locale, func(t *testing.T) {
			if got := FormatTime(tc.locale, instant, time.UTC); got != tc.want {
				t.Fatalf("FormatTime(%q) = %q, want %q", tc.locale, got, tc.want)
			}
		})
	}
}

func TestFormatDateTimeConvertsTimezone(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation(America/New_York): %v", err)
	}
	// 2026-09-05 23:30 UTC is 19:30 in New York (EDT, UTC-4).
	instant := time.Date(2026, 9, 5, 23, 30, 0, 0, time.UTC)

	if got, want := FormatDateTime("de", instant, newYork), "05.09.2026, 19:30"; got != want {
		t.Fatalf("FormatDateTime(de) = %q, want %q", got, want)
	}
	if got, want := FormatDateTime("en", instant, newYork), "9/5/2026, 7:30 PM"; got != want {
		t.Fatalf("FormatDateTime(en) = %q, want %q", got, want)
	}
}

func TestFormatDateTimeZLabelsZone(t *testing.T) {
	instant := time.Date(2026, 9, 5, 14, 30, 0, 0, time.UTC)
	if got, want := FormatDateTimeZ("en", instant, time.UTC), "9/5/2026, 2:30 PM UTC"; got != want {
		t.Fatalf("FormatDateTimeZ(en, UTC) = %q, want %q", got, want)
	}
}

func TestFormatNilLocationUsesUTC(t *testing.T) {
	instant := time.Date(2026, 9, 5, 14, 30, 0, 0, time.UTC)
	if got, want := FormatDateTime("en", instant, nil), "9/5/2026, 2:30 PM"; got != want {
		t.Fatalf("FormatDateTime(en, nil) = %q, want %q", got, want)
	}
}

func TestLocation(t *testing.T) {
	if got := Location(""); got != time.UTC {
		t.Fatalf("Location(\"\") = %v, want UTC", got)
	}
	if got := Location("Not/AZone"); got != time.UTC {
		t.Fatalf("Location(Not/AZone) = %v, want UTC", got)
	}
	if got := Location("UTC"); got != time.UTC {
		t.Fatalf("Location(UTC) = %v, want UTC", got)
	}
	if got := Location("Europe/Berlin"); got.String() != "Europe/Berlin" {
		t.Fatalf("Location(Europe/Berlin) = %v, want Europe/Berlin", got)
	}
}

func TestZoneName(t *testing.T) {
	if got := ZoneName(nil); got != "UTC" {
		t.Fatalf("ZoneName(nil) = %q, want UTC", got)
	}
	if got := ZoneName(time.UTC); got != "UTC" {
		t.Fatalf("ZoneName(UTC) = %q, want UTC", got)
	}
}

// TestGeneratedDateFormatsAreComplete guards the generated table: every shipped
// locale must have all three layouts. A stale generated file fails here.
func TestGeneratedDateFormatsAreComplete(t *testing.T) {
	for _, locale := range Supported() {
		format, ok := dateFormats[locale]
		if !ok {
			t.Fatalf("locale %q is missing from the generated date format table", locale)
		}
		if format.Date == "" || format.DateTime == "" || format.Time == "" {
			t.Errorf("locale %q has an incomplete date format: %+v", locale, format)
		}
	}
}
