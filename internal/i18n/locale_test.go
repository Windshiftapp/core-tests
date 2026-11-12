//go:build test

package i18n

import (
	"slices"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		tag  string
		want string
	}{
		{name: "empty defaults to english", tag: "", want: "en"},
		{name: "exact match", tag: "en", want: "en"},
		{name: "case insensitive", tag: "EN", want: "en"},
		{name: "region specific exact", tag: "pt-BR", want: "pt-BR"},
		{name: "underscore separator", tag: "pt_BR", want: "pt-BR"},
		{name: "region falls back to base language", tag: "de-AT", want: "de"},
		{name: "base language matches region locale", tag: "zh", want: "zh-CN"},
		{name: "script tag matches region locale", tag: "zh-Hans", want: "zh-CN"},
		{name: "korean region", tag: "ko-KR", want: "ko"},
		{name: "french region", tag: "fr-CA", want: "fr"},
		{name: "italian exact", tag: "it", want: "it"},
		{name: "polish exact", tag: "pl", want: "pl"},
		{name: "polish region", tag: "pl-PL", want: "pl"},
		{name: "unknown locale defaults to english", tag: "xx", want: "en"},
		{name: "whitespace trimmed", tag: "  de  ", want: "de"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Normalize(tc.tag); got != tc.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tc.tag, got, tc.want)
			}
		})
	}
}

func TestSupported(t *testing.T) {
	locales := Supported()
	// Pinned to the shipped set; update alongside a new locale. The backend
	// derives this from the generated date-format table, which mirrors the
	// frontend's SUPPORTED_LOCALES.
	want := []string{"ar", "de", "en", "es", "fa", "fr", "it", "ko", "pl", "pt-BR", "ru", "zh-CN"}
	if !slices.Equal(locales, want) {
		t.Fatalf("Supported() = %v, want %v", locales, want)
	}
	seen := make(map[string]bool, len(locales))
	for _, locale := range locales {
		if locale == "" {
			t.Fatal("Supported() returned an empty locale")
		}
		if seen[locale] {
			t.Fatalf("Supported() returned duplicate locale %q", locale)
		}
		seen[locale] = true
	}
	if !seen[Default] {
		t.Fatalf("Supported() does not include the default locale %q", Default)
	}

	// Mutating the returned slice must not corrupt package state.
	locales[0] = "mutated"
	if Supported()[0] == "mutated" {
		t.Fatal("Supported() returns the backing slice; callers can mutate package state")
	}
}

func TestIsRTL(t *testing.T) {
	tests := []struct {
		locale string
		want   bool
	}{
		{locale: "ar", want: true},
		{locale: "ar-EG", want: true},
		{locale: "en", want: false},
		{locale: "de", want: false},
		{locale: "he", want: false}, // unsupported, normalizes to en
	}
	for _, tc := range tests {
		if got := IsRTL(tc.locale); got != tc.want {
			t.Errorf("IsRTL(%q) = %v, want %v", tc.locale, got, tc.want)
		}
	}
}
