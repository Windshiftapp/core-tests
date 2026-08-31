//go:build test

package validation

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeTitle(t *testing.T) {
	tests := []struct {
		name    string
		title   string
		want    string
		message string
	}{
		{name: "preserves angle brackets", title: "Promise<Anything>", want: "Promise<Anything>"},
		{name: "trims surrounding whitespace", title: " \nPromise<Anything>\t", want: "Promise<Anything>"},
		{name: "allows maximum rune count", title: strings.Repeat("ø", TitleMaxRunes), want: strings.Repeat("ø", TitleMaxRunes)},
		{name: "rejects empty", title: "", message: "Title is required"},
		{name: "rejects whitespace only", title: " \n\t", message: "Title is required"},
		{name: "rejects newline", title: "Run\nbook", message: "Title must be a single line without control characters"},
		{name: "rejects too many runes", title: strings.Repeat("x", TitleMaxRunes+1), message: "Title must be at most 255 characters"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeTitle(tt.title)
			if tt.message == "" {
				if err != nil {
					t.Fatalf("NormalizeTitle(%q): %v", tt.title, err)
				}
				if got != tt.want {
					t.Fatalf("NormalizeTitle(%q) = %q, want %q", tt.title, got, tt.want)
				}
				return
			}
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("error = %T %v, want *ValidationError", err, err)
			}
			if validationErr.Field != "title" || validationErr.Message != tt.message {
				t.Fatalf("validation error = %+v, want field title message %q", validationErr, tt.message)
			}
		})
	}
}

func TestValidateMarkdownSource(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		maxBytes int
		required bool
		message  string
	}{
		{name: "accepts exact source", source: "`Promise<Anything>`\r\n&amp;", maxBytes: 256},
		{name: "accepts optional empty source", maxBytes: 256},
		{name: "rejects required whitespace", source: " \n", maxBytes: 256, required: true, message: "content is required"},
		{name: "counts bytes", source: "øø", maxBytes: 3, message: "content must be at most 3 bytes"},
		{name: "rejects invalid UTF-8", source: string([]byte{0xff}), maxBytes: 256, message: "content must be valid UTF-8"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMarkdownSource("content", tt.source, tt.maxBytes, tt.required)
			if tt.message == "" {
				if err != nil {
					t.Fatalf("ValidateMarkdownSource(%q): %v", tt.source, err)
				}
				return
			}
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("error = %T %v, want *ValidationError", err, err)
			}
			if validationErr.Field != "content" || validationErr.Message != tt.message {
				t.Fatalf("validation error = %+v, want field content message %q", validationErr, tt.message)
			}
		})
	}
}
