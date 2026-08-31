// sanitize_test exercises each named policy in the sanitize package
// against a small but representative set of XSS / injection vectors.
// These are the contract tests for the canonical input policy library;
// every service in the app routes through these policies, so a
// regression here is a regression in the security posture of every
// surface that accepts user-supplied text.
package sanitize_test

import (
	"strings"
	"testing"

	"windshift/internal/sanitize"
)

// TestPlainTextField — short single-line user-facing label policy
// (titles, names). Must strip every HTML tag, decode entities, trim
// whitespace, and length-cap at 200 runes.
func TestPlainTextField(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"empty", "", ""},
		{"plain", "Hello World", "Hello World"},
		{"script_tag_dropped", "<script>alert(1)</script>Hello", "Hello"},
		{"img_onerror_dropped", "<img src=x onerror=alert(1)>Foo", "Foo"},
		{"entity_decoded", "Jamie&#39;s book", "Jamie's book"},
		{"trim_surrounding_ws", "  \t Hello \n ", "Hello"},
		{"nested_tags_dropped", "<b><i>Title</i></b>", "Title"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitize.PlainTextField.Sanitize(tc.input)
			if got != tc.want {
				t.Fatalf("PlainTextField(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}

	t.Run("length_cap_256_runes", func(t *testing.T) {
		input := strings.Repeat("a", 300)
		got := sanitize.PlainTextField.Sanitize(input)
		// Use rune count — bluemonday + cap operate on runes.
		if n := len([]rune(got)); n != sanitize.PlainTextFieldMaxRunes {
			t.Fatalf("PlainTextField length = %d, want %d", n, sanitize.PlainTextFieldMaxRunes)
		}
	})
}

// TestShortIdentifier — identifier-like field policy (asset tag, slug,
// label name). Same shape as PlainTextField with a tighter 100-rune cap.
func TestShortIdentifier(t *testing.T) {
	t.Run("script_stripped_short_cap", func(t *testing.T) {
		got := sanitize.ShortIdentifier.Sanitize("<script>x</script>LAP-001")
		if got != "LAP-001" {
			t.Fatalf("ShortIdentifier strip failed: %q", got)
		}
	})
	t.Run("length_cap_100_runes", func(t *testing.T) {
		input := strings.Repeat("x", 150)
		got := sanitize.ShortIdentifier.Sanitize(input)
		if n := len([]rune(got)); n != sanitize.ShortIdentifierMaxRunes {
			t.Fatalf("ShortIdentifier length = %d, want %d", n, sanitize.ShortIdentifierMaxRunes)
		}
	})
}

// TestRichText — multi-line body content policy (descriptions, notes).
// Strips HTML except <br />, decodes entities, neutralizes dangerous
// Markdown URL schemes, length-caps at 10 KiB.
func TestRichText(t *testing.T) {
	t.Run("preserves_commonmark_autolinks", func(t *testing.T) {
		for _, input := range []string{
			"<https://example.com/docs?q=windshift&view=full>",
			"<mailto:hello@example.com>",
			"<hello@example.com>",
		} {
			t.Run(input, func(t *testing.T) {
				if got := sanitize.RichText.Sanitize(input); got != input {
					t.Fatalf("RichText(%q) = %q, want the autolink preserved", input, got)
				}
			})
		}
	})
	t.Run("dangerous_autolink_schemes_are_not_restored", func(t *testing.T) {
		for _, input := range []string{
			"<javascript:alert(1)>",
			"<vbscript:msgbox(1)>",
			"<data:text/html,alert(1)>",
		} {
			t.Run(input, func(t *testing.T) {
				got := sanitize.RichText.Sanitize(input)
				if strings.Contains(strings.ToLower(got), strings.Split(input[1:], ":")[0]+":") {
					t.Fatalf("RichText restored unsafe autolink %q as %q", input, got)
				}
			})
		}
	})
	t.Run("br_preserved", func(t *testing.T) {
		got := sanitize.RichText.Sanitize("line one<br />line two")
		if !strings.Contains(got, "<br />") {
			t.Fatalf("RichText dropped <br />: %q", got)
		}
	})
	t.Run("script_stripped", func(t *testing.T) {
		got := sanitize.RichText.Sanitize("safe<script>evil()</script>after")
		if strings.Contains(got, "<script") {
			t.Fatalf("RichText kept <script>: %q", got)
		}
	})
	t.Run("javascript_url_neutralized", func(t *testing.T) {
		got := sanitize.RichText.Sanitize("Click [here](javascript:alert(1))")
		if strings.Contains(got, "javascript:") {
			t.Fatalf("RichText kept javascript: URL: %q", got)
		}
		if !strings.Contains(got, "#unsafe-link-removed") {
			t.Fatalf("RichText didn't mark unsafe link: %q", got)
		}
	})
	t.Run("data_url_neutralized", func(t *testing.T) {
		got := sanitize.RichText.Sanitize("![img](data:text/html,<script>x</script>)")
		if strings.Contains(got, "data:text/html") {
			t.Fatalf("RichText kept data: URL: %q", got)
		}
	})
	t.Run("vbscript_url_neutralized", func(t *testing.T) {
		got := sanitize.RichText.Sanitize("[x](vbscript:msgbox(1))")
		if strings.Contains(got, "vbscript:") {
			t.Fatalf("RichText kept vbscript: URL: %q", got)
		}
	})
	t.Run("normalizes_br_close_form", func(t *testing.T) {
		// bluemonday writes <br/>, Milkdown round-trips on <br />.
		got := sanitize.RichText.Sanitize("a<br/>b")
		if !strings.Contains(got, "<br />") {
			t.Fatalf("RichText didn't normalize <br/>: %q", got)
		}
	})
	t.Run("empty_and_null_string", func(t *testing.T) {
		if got := sanitize.RichText.Sanitize(""); got != "" {
			t.Fatalf("RichText empty: %q", got)
		}
		if got := sanitize.RichText.Sanitize("null"); got != "" {
			t.Fatalf("RichText 'null' string should clear: %q", got)
		}
	})
	t.Run("length_cap_long_text", func(t *testing.T) {
		got := sanitize.RichText.Sanitize(strings.Repeat("z", sanitize.LongTextMaxBytes+1024))
		if len(got) != sanitize.LongTextMaxBytes {
			t.Fatalf("RichText length = %d, want %d", len(got), sanitize.LongTextMaxBytes)
		}
	})
}

// TestLongDocument — long-form Markdown document policy for pages and
// runbooks. It shares RichText's content shape but has a larger length cap.
func TestLongDocument(t *testing.T) {
	t.Run("preserves_commonmark_autolink", func(t *testing.T) {
		input := "See <https://example.com/runbook>"
		if got := sanitize.LongDocument.Sanitize(input); got != input {
			t.Fatalf("LongDocument(%q) = %q, want the autolink preserved", input, got)
		}
	})
	t.Run("strips_script_keeps_br", func(t *testing.T) {
		got := sanitize.LongDocument.Sanitize("a<br />b<script>x</script>")
		if strings.Contains(got, "<script") {
			t.Fatalf("LongDocument kept <script>: %q", got)
		}
		if !strings.Contains(got, "<br />") {
			t.Fatalf("LongDocument dropped <br />: %q", got)
		}
	})
	t.Run("length_cap_document", func(t *testing.T) {
		const wantMaxBytes = 1 << 20
		got := sanitize.LongDocument.Sanitize(strings.Repeat("y", wantMaxBytes+1024))
		if len(got) != wantMaxBytes {
			t.Fatalf("LongDocument length = %d, want %d", len(got), wantMaxBytes)
		}
	})
}

// TestComment — user-submitted comment policy. Strips every HTML tag
// + neutralizes dangerous Markdown URLs, caps at LongTextMaxBytes.
func TestComment(t *testing.T) {
	t.Run("preserves_commonmark_autolink", func(t *testing.T) {
		input := "See <https://example.com/comment>"
		if got := sanitize.Comment.Sanitize(input); got != input {
			t.Fatalf("Comment(%q) = %q, want the autolink preserved", input, got)
		}
	})
	t.Run("strips_all_html", func(t *testing.T) {
		// Unlike RichText, Comment does NOT preserve <br />.
		got := sanitize.Comment.Sanitize("hi<br />there<script>x</script>")
		if strings.Contains(got, "<br") || strings.Contains(got, "<script") {
			t.Fatalf("Comment kept html: %q", got)
		}
	})
	t.Run("javascript_url_neutralized", func(t *testing.T) {
		got := sanitize.Comment.Sanitize("[x](javascript:alert(1))")
		if strings.Contains(got, "javascript:") {
			t.Fatalf("Comment kept javascript: URL: %q", got)
		}
	})
	t.Run("length_cap_long_text", func(t *testing.T) {
		got := sanitize.Comment.Sanitize(strings.Repeat("c", sanitize.LongTextMaxBytes+1024))
		if len(got) != sanitize.LongTextMaxBytes {
			t.Fatalf("Comment length = %d, want %d", len(got), sanitize.LongTextMaxBytes)
		}
	})
}

// TestMarkdownURLOnly — leave HTML alone, only neutralize bad URL
// schemes in Markdown link/image syntax.
func TestMarkdownURLOnly(t *testing.T) {
	t.Run("html_untouched_url_neutralized", func(t *testing.T) {
		got := sanitize.MarkdownURLOnly.Sanitize("<b>bold</b> [x](javascript:alert(1))")
		if !strings.Contains(got, "<b>") {
			t.Fatalf("MarkdownURLOnly stripped HTML it shouldn't: %q", got)
		}
		if strings.Contains(got, "javascript:") {
			t.Fatalf("MarkdownURLOnly kept javascript URL: %q", got)
		}
	})
}

// TestApply — the in-place pointer helper used by the canonical
// "sanitize this entity's text fields" pattern.
func TestApply(t *testing.T) {
	t.Run("nil_pointer_noop", func(t *testing.T) {
		sanitize.Apply(nil, sanitize.PlainTextField) // must not panic
	})
	t.Run("mutates_in_place", func(t *testing.T) {
		s := "<script>x</script>Title"
		sanitize.Apply(&s, sanitize.PlainTextField)
		if s != "Title" {
			t.Fatalf("Apply didn't mutate: %q", s)
		}
	})
}

// TestApplyAllWithWarnings — labeled pairs produce user-facing
// warning copy when the value changes. Empty labels are silent even
// when the value changed. Unchanged values never produce warnings.
func TestApplyAllWithWarnings(t *testing.T) {
	t.Run("html_strip_yields_warning", func(t *testing.T) {
		title := "<script>alert(1)</script>Title"
		warnings := sanitize.ApplyAllWithWarnings(
			sanitize.Pair{Target: &title, Policy: sanitize.PlainTextField, Label: "Title"},
		)
		if len(warnings) != 1 {
			t.Fatalf("want 1 warning, got %d: %v", len(warnings), warnings)
		}
		if !strings.Contains(warnings[0], "HTML formatting removed") {
			t.Fatalf("warning didn't mention HTML stripping: %q", warnings[0])
		}
	})

	t.Run("truncation_yields_warning_with_length", func(t *testing.T) {
		long := strings.Repeat("a", 300)
		warnings := sanitize.ApplyAllWithWarnings(
			sanitize.Pair{Target: &long, Policy: sanitize.PlainTextField, Label: "Title"},
		)
		if len(warnings) != 1 {
			t.Fatalf("want 1 warning, got %d", len(warnings))
		}
		if !strings.Contains(warnings[0], "shortened to 256") {
			t.Fatalf("warning didn't mention truncation to 256: %q", warnings[0])
		}
	})

	t.Run("html_and_truncation_combined", func(t *testing.T) {
		mixed := "<b>" + strings.Repeat("z", 300) + "</b>"
		warnings := sanitize.ApplyAllWithWarnings(
			sanitize.Pair{Target: &mixed, Policy: sanitize.PlainTextField, Label: "Title"},
		)
		if len(warnings) != 1 {
			t.Fatalf("want 1 warning, got %d", len(warnings))
		}
		if !strings.Contains(warnings[0], "HTML") || !strings.Contains(warnings[0], "shortened") {
			t.Fatalf("warning didn't mention both HTML + shortening: %q", warnings[0])
		}
	})

	t.Run("clean_input_no_warning", func(t *testing.T) {
		clean := "Just a plain title"
		warnings := sanitize.ApplyAllWithWarnings(
			sanitize.Pair{Target: &clean, Policy: sanitize.PlainTextField, Label: "Title"},
		)
		if len(warnings) != 0 {
			t.Fatalf("clean input must produce no warnings, got %v", warnings)
		}
	})

	t.Run("empty_label_silent_even_on_change", func(t *testing.T) {
		dirty := "<script>x</script>silent"
		warnings := sanitize.ApplyAllWithWarnings(
			sanitize.Pair{Target: &dirty, Policy: sanitize.PlainTextField}, // no Label
		)
		if len(warnings) != 0 {
			t.Fatalf("unlabeled pair must not produce warnings, got %v", warnings)
		}
		if dirty != "silent" {
			t.Fatalf("mutation still happened: %q", dirty)
		}
	})

	t.Run("mixed_labeled_and_unlabeled", func(t *testing.T) {
		title := "<b>Hello</b>"
		desc := "<script>x</script>plain"
		warnings := sanitize.ApplyAllWithWarnings(
			sanitize.Pair{Target: &title, Policy: sanitize.PlainTextField, Label: "Title"},
			sanitize.Pair{Target: &desc, Policy: sanitize.RichText}, // no label
		)
		if len(warnings) != 1 {
			t.Fatalf("want 1 labeled warning, got %d: %v", len(warnings), warnings)
		}
		if !strings.Contains(warnings[0], "Title") {
			t.Fatalf("warning didn't reference the labeled field: %q", warnings[0])
		}
	})
}

// TestApplyAll — batch in-place sanitization across multiple fields
// with mixed policies. Exercises the canonical service-layer call
// shape.
func TestApplyAll(t *testing.T) {
	title := "<script>x</script>MyAsset"
	description := "Hello<script>y</script>World<br />Next"
	tag := "<img src=x onerror=alert(1)>LAP-001"

	sanitize.ApplyAll(
		sanitize.Pair{Target: &title, Policy: sanitize.PlainTextField},
		sanitize.Pair{Target: &description, Policy: sanitize.RichText},
		sanitize.Pair{Target: &tag, Policy: sanitize.ShortIdentifier},
	)

	if strings.Contains(title, "<script") {
		t.Fatalf("title leak: %q", title)
	}
	if strings.Contains(description, "<script") {
		t.Fatalf("description leak: %q", description)
	}
	if !strings.Contains(description, "<br />") {
		t.Fatalf("description dropped <br />: %q", description)
	}
	if strings.Contains(tag, "onerror") {
		t.Fatalf("tag leak: %q", tag)
	}
	if tag != "LAP-001" {
		t.Fatalf("tag normalization unexpected: %q", tag)
	}
}
