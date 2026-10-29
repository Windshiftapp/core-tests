//go:build test

package handlers

import "testing"

func TestEscapeHandlerJQLStringEscapesQuotesAndBackslashes(t *testing.T) {
	if got, want := escapeHandlerJQLString(`OPS\" OR assignee IS NOT EMPTY`), `OPS\\\" OR assignee IS NOT EMPTY`; got != want {
		t.Fatalf("escaped JQL value = %q, want %q", got, want)
	}
}
