//go:build test

package v2

import (
	"errors"
	"net/http"
	"testing"
)

func TestParseItemReferencePrefersNumericID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want itemLookupReference
	}{
		{name: "integer ID", raw: "42", want: itemLookupReference{ID: 42}},
		{name: "zero-padded integer ID", raw: "00042", want: itemLookupReference{ID: 42}},
		{name: "item key", raw: "WI-1238", want: itemLookupReference{WorkspaceKey: "WI", ItemNumber: 1238}},
		{name: "case-insensitive item key", raw: "wi-1238", want: itemLookupReference{WorkspaceKey: "wi", ItemNumber: 1238}},
		// Legacy personal-workspace keys contain dashes; the number is the
		// segment after the last one (WI-1416).
		{name: "legacy dashed key", raw: "JOHN-DOE-5", want: itemLookupReference{WorkspaceKey: "JOHN-DOE", ItemNumber: 5}},
		{name: "legacy dashed key with counter", raw: "JOHN-DOE-2-42", want: itemLookupReference{WorkspaceKey: "JOHN-DOE-2", ItemNumber: 42}},
		{name: "multi-dash key single digit", raw: "WI-1-2", want: itemLookupReference{WorkspaceKey: "WI-1", ItemNumber: 2}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseItemReference(test.raw)
			if err != nil {
				t.Fatalf("parseItemReference(%q): %v", test.raw, err)
			}
			if got != test.want {
				t.Fatalf("parseItemReference(%q) = %#v, want %#v", test.raw, got, test.want)
			}
		})
	}
}

func TestParseItemReferenceRejectsInvalidValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "zero ID", raw: "0"},
		{name: "negative ID", raw: "-1"},
		{name: "overflowing ID", raw: "999999999999999999999999"},
		{name: "surrounding whitespace", raw: " WI-1 "},
		{name: "short workspace key", raw: "W-1"},
		{name: "zero item number", raw: "WI-0"},
		{name: "non-numeric item number", raw: "WI-nope"},
		{name: "workspace key punctuation", raw: "WI_2-1"},
		{name: "leading dash", raw: "-WI-5"},
		{name: "double dash in key", raw: "JOHN--DOE-5"},
		{name: "trailing dash", raw: "WI-"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseItemReference(test.raw)
			var apiErr *Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("parseItemReference(%q) error = %v, want API error", test.raw, err)
			}
			if apiErr.Status != http.StatusBadRequest || apiErr.Code != "invalid_request" {
				t.Fatalf("parseItemReference(%q) error = %#v, want 400 invalid_request", test.raw, apiErr)
			}
		})
	}
}
