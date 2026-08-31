package repository

import (
	"strings"
	"testing"
)

// TestCenteredSnippet_CentersOnFirstMatch pins the SQLite chunk-search snippet
// improvement: when the query lives past the first 240 bytes of content, the
// snippet must still contain it (previously the snippet was always the first
// 240 bytes, so any match further in was invisible to callers).
func TestCenteredSnippet_CentersOnFirstMatch(t *testing.T) {
	prefix := strings.Repeat("a ", 500) // ~1000 chars of filler
	query := "needle"
	content := prefix + query + " trailing content here for context."

	got := centeredSnippet(content, query, 240)
	if !strings.Contains(strings.ToLower(got), query) {
		t.Fatalf("snippet should contain %q, got %q", query, got)
	}
	if runes := []rune(got); len(runes) > 240 {
		t.Errorf("snippet rune length: want <=240, got %d", len(runes))
	}
}

// TestCenteredSnippet_NoMatchFallsBackToHead covers the no-match path: when
// the query isn't found in content (e.g. it matched in heading_path only),
// we still return a short head snippet rather than nothing.
func TestCenteredSnippet_NoMatchFallsBackToHead(t *testing.T) {
	content := strings.Repeat("alpha ", 200) // 1200 chars
	got := centeredSnippet(content, "needle", 240)
	if runes := []rune(got); len(runes) != 240 {
		t.Errorf("no-match snippet length: want 240 runes, got %d", len(runes))
	}
	if !strings.HasPrefix(content, got) {
		t.Errorf("no-match snippet should be the head of content")
	}
}

// TestCenteredSnippet_ShortContent returns the whole content untouched.
func TestCenteredSnippet_ShortContent(t *testing.T) {
	got := centeredSnippet("short body with needle in it", "needle", 240)
	if got != "short body with needle in it" {
		t.Errorf("short content: want full passthrough, got %q", got)
	}
}

// TestCenteredSnippet_PreservesMultibyteRunes makes sure the rune-based slice
// never cuts a multi-byte character (a hard-to-debug class of bug if byte
// slicing slipped back in).
func TestCenteredSnippet_PreservesMultibyteRunes(t *testing.T) {
	content := strings.Repeat("日本", 500) + "needle" + strings.Repeat("語", 500)
	got := centeredSnippet(content, "needle", 240)
	if !strings.Contains(got, "needle") {
		t.Errorf("expected snippet to contain match; got %q", got)
	}
	// strings.ToValidUTF8 returns the input unchanged when it's valid.
	if strings.ToValidUTF8(got, "?") != got {
		t.Errorf("snippet sliced through a multi-byte rune: %q", got)
	}
}
