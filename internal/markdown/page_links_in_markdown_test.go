//go:build test

package markdown

import (
	"testing"
)

// Portal surfaces that render the raw markdown through the shared read-only
// editor need the page links rewritten at the markdown level: the readonly
// pipeline renders markdown structure and treats raw HTML as inert text.
func TestRewritePageLinksInMarkdown(t *testing.T) {
	resolveKnown := func(pageID int) (string, bool) {
		if pageID == 2 {
			return "/portal/support/kb/2?source=ticket", true
		}
		return "", false
	}

	t.Run("resolvable link gets its destination rewritten", func(t *testing.T) {
		got := RewritePageLinksInMarkdown("See [the guide](page:2) for details.", resolveKnown)
		want := "See [the guide](/portal/support/kb/2?source=ticket) for details."
		if got != want {
			t.Fatalf("RewritePageLinksInMarkdown = %q, want %q", got, want)
		}
	})

	t.Run("unresolvable link degrades to plain link text", func(t *testing.T) {
		got := RewritePageLinksInMarkdown("See [the secret page](page:7) for details.", resolveKnown)
		want := "See the secret page for details."
		if got != want {
			t.Fatalf("unresolvable rewrite = %q, want %q", got, want)
		}
	})

	t.Run("multiple links mixed resolvable and unresolvable", func(t *testing.T) {
		got := RewritePageLinksInMarkdown(
			"[published](page:2) and [unpublished](page:9) and [also published](page:2)",
			resolveKnown,
		)
		want := "[published](/portal/support/kb/2?source=ticket) and unpublished and [also published](/portal/support/kb/2?source=ticket)"
		if got != want {
			t.Fatalf("mixed rewrite = %q, want %q", got, want)
		}
	})

	t.Run("content without page links passes through", func(t *testing.T) {
		got := RewritePageLinksInMarkdown("Plain [https link](https://example.com) text.", resolveKnown)
		want := "Plain [https link](https://example.com) text."
		if got != want {
			t.Fatalf("passthrough = %q, want %q", got, want)
		}
	})

	t.Run("nil resolve leaves content unchanged", func(t *testing.T) {
		source := "See [the guide](page:2) for details."
		if got := RewritePageLinksInMarkdown(source, nil); got != source {
			t.Fatalf("nil resolve = %q, want %q", got, source)
		}
	})
}
