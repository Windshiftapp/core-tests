//go:build test

package search

import (
	"reflect"
	"testing"
)

func TestTokens(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
		want []string
	}{
		{name: "lowercases and splits on punctuation", raw: "Outlook-Version", want: []string{"outlook", "version"}},
		{name: "keeps digits", raw: "Outlook 2026", want: []string{"outlook", "2026"}},
		{name: "drops single character terms", raw: "a outlook", want: []string{"outlook"}},
		{name: "two character term is kept", raw: "ok", want: []string{"ok"}},
		{name: "underscore separates", raw: "foo_bar", want: []string{"foo", "bar"}},
		{name: "cyrillic is tokenized", raw: "Версию", want: []string{"версию"}},
		{name: "empty query", raw: "", want: nil},
		{name: "only punctuation", raw: "!!!", want: nil},
		{name: "only short terms", raw: "a b c", want: nil},
		{name: "cjk query falls back", raw: "版本", want: nil},
		{name: "mixed cjk and latin falls back", raw: "outlook 版本", want: nil},
		{name: "thai query falls back", raw: "เวอร์ชัน", want: nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Tokens(tt.raw); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Tokens(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestTokensCapsTerms(t *testing.T) {
	got := Tokens("one two three four five six seven eight nine ten")
	if len(got) != maxTokens {
		t.Fatalf("Tokens returned %d terms, want %d: %v", len(got), maxTokens, got)
	}
}

func TestSQLiteMatch(t *testing.T) {
	if got := SQLiteMatch([]string{"outlo", "versio"}); got != "outlo* versio*" {
		t.Fatalf("SQLiteMatch = %q, want %q", got, "outlo* versio*")
	}
}

func TestPostgresTSQuery(t *testing.T) {
	if got := PostgresTSQuery([]string{"outlo", "versio"}); got != "outlo:* & versio:*" {
		t.Fatalf("PostgresTSQuery = %q, want %q", got, "outlo:* & versio:*")
	}
}
