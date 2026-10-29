//go:build test

package markdown

import (
	"strings"
	"testing"
)

func TestRewritePageLinks(t *testing.T) {
	render := func(source string) string {
		t.Helper()
		html, err := Render(source)
		if err != nil {
			t.Fatalf("render %q: %v", source, err)
		}
		return html
	}

	t.Run("resolvable link gets its href rewritten", func(t *testing.T) {
		html := render("See [the guide](page:42) for details.")
		got := RewritePageLinks(html, func(pageID int) (string, bool) {
			if pageID != 42 {
				t.Fatalf("resolve pageID = %d, want 42", pageID)
			}
			return "/portal/support/kb/42", true
		})
		want := "<p>See <a href=\"/portal/support/kb/42\">the guide</a> for details.</p>\n"
		if got != want {
			t.Fatalf("RewritePageLinks = %q, want %q", got, want)
		}
	})

	t.Run("unresolvable link degrades to its link text", func(t *testing.T) {
		html := render("See [secret draft](page:7) for details.")
		got := RewritePageLinks(html, func(int) (string, bool) { return "", false })
		want := "<p>See secret draft for details.</p>\n"
		if got != want {
			t.Fatalf("RewritePageLinks = %q, want %q", got, want)
		}
		if strings.Contains(got, "<a") || strings.Contains(got, "href") {
			t.Fatalf("unresolvable link kept an anchor: %q", got)
		}
	})

	t.Run("external links are untouched", func(t *testing.T) {
		html := render("[site](https://example.com) and [rel](/other) and [broken](page:abc)")
		got := RewritePageLinks(html, func(int) (string, bool) {
			t.Fatal("resolve must not be called without page: anchors")
			return "", false
		})
		if got != html {
			t.Fatalf("RewritePageLinks = %q, want unchanged %q", got, html)
		}
	})

	t.Run("multiple links resolve independently", func(t *testing.T) {
		html := render("[one](page:1) and [two](page:2) and [gone](page:3)")
		got := RewritePageLinks(html, func(pageID int) (string, bool) {
			if pageID == 2 {
				return "", false
			}
			return "/portal/p/kb/" + strings.Repeat("x", 0) + itoa(pageID), true
		})
		want := "<p><a href=\"/portal/p/kb/1\">one</a> and two and <a href=\"/portal/p/kb/3\">gone</a></p>\n"
		if got != want {
			t.Fatalf("RewritePageLinks = %q, want %q", got, want)
		}
	})

	t.Run("empty html and nil resolver pass through", func(t *testing.T) {
		if got := RewritePageLinks("", func(int) (string, bool) { return "", false }); got != "" {
			t.Fatalf("empty html = %q, want %q", got, "")
		}
		html := render("[a](page:1)")
		if got := RewritePageLinks(html, nil); got != html {
			t.Fatalf("nil resolver = %q, want unchanged %q", got, html)
		}
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
