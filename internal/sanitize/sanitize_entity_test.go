package sanitize

import (
	"strings"
	"testing"
)

// entityPayload is the canonical finding payload: the < and > around an
// <img onerror> tag are HTML-entity-encoded, so bluemonday tokenizes them
// as character data and StrictPolicy passes them through untouched. The
// post-sanitize html.UnescapeString then reconstitutes a live tag.
const entityPayload = `&lt;img src=x onerror=alert(1)&gt;`

// stripAllTags removes every remaining <...> tag from s; used as an
// assertion helper (the sanitizer's contract is "no HTML in output").
func stripAllTags(t *testing.T, s string) string {
	t.Helper()
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
			continue
		}
		if !inTag {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func assertNoRawTag(t *testing.T, policy Policy, label string) {
	t.Helper()
	out := policy.Sanitize(entityPayload)
	if out == entityPayload {
		t.Errorf("%s: payload passed through unmodified: %q", label, out)
	}
	if strings.Contains(strings.ToLower(out), "<img") {
		t.Errorf("%s: live <img tag reconstituted after sanitize: %q", label, out)
	}
	if strings.Contains(strings.ToLower(out), "onerror") {
		t.Errorf("%s: onerror handler survived sanitize: %q", label, out)
	}
	// After stripping any tag residue, no raw tag markup should remain.
	if got := stripAllTags(t, out); got != out && strings.Contains(strings.ToLower(out), "<img") {
		t.Errorf("%s: output still contained tag markup after tag-strip: %q", label, out)
	}
}

func TestPlainTextFieldEntityEncodedXSS(t *testing.T) {
	assertNoRawTag(t, PlainTextField, "PlainTextField")
}

func TestShortIdentifierEntityEncodedXSS(t *testing.T) {
	assertNoRawTag(t, ShortIdentifier, "ShortIdentifier")
}

func TestRichTextEntityEncodedXSS(t *testing.T) {
	assertNoRawTag(t, RichText, "RichText")
}

func TestLongDocumentEntityEncodedXSS(t *testing.T) {
	assertNoRawTag(t, LongDocument, "LongDocument")
}

func TestCommentEntityEncodedXSS(t *testing.T) {
	assertNoRawTag(t, Comment, "Comment")
}

// Legit entity-encoded prose (e.g. "5 &lt; 6 &gt; 4") must survive as
// plain decoded text, not be eaten. Regression guard for the
// second-sanitize pass turning into over-stripping.
func TestPlainTextFieldPreservesDecodedEntities(t *testing.T) {
	got := PlainTextField.Sanitize("5 &lt; 6 &gt; 4")
	if want := "5 < 6 > 4"; got != want {
		t.Errorf("PlainTextField decoded-prose: got %q want %q", got, want)
	}
}

func TestRichTextPreservesDecodedEntities(t *testing.T) {
	got := RichText.Sanitize("5 &lt; 6 &gt; 4")
	if want := "5 < 6 > 4"; got != want {
		t.Errorf("RichText decoded-prose: got %q want %q", got, want)
	}
}

// brOnly path must still keep a real <br /> on round-trip (its whole
// reason for existing — Milkdown blank-line preservation).
func TestRichTextPreservesBreakTag(t *testing.T) {
	got := RichText.Sanitize("line one<br />line two")
	if !strings.Contains(got, "<br />") {
		t.Errorf("RichText lost <br />: got %q", got)
	}
	if strings.Contains(strings.ToLower(got), "<img") {
		t.Errorf("RichText leaked img: %q", got)
	}
}

// A javascript: Markdown link is neutralized regardless of HTML.
func TestCommentNeutralizesDangerousMarkdownURL(t *testing.T) {
	got := Comment.Sanitize("[click](javascript:alert(1))")
	if strings.Contains(strings.ToLower(got), "javascript:") {
		t.Errorf("Comment kept dangerous scheme: %q", got)
	}
}

func TestLongDocumentPreservesHTMLInsideFencedCodeBlock(t *testing.T) {
	input := "before <script>alert('x')</script>\n\n```html\n<div id=\"ws-form\"></div>\n<script src=\"/embed/windshift-forms.js\"></script>\n```\n\nafter"
	got := LongDocument.Sanitize(input)
	if strings.Contains(got, "before <script>") {
		t.Fatalf("LongDocument kept executable HTML outside code block: %q", got)
	}
	if !strings.Contains(got, "<div id=\"ws-form\"></div>") || !strings.Contains(got, "<script src=\"/embed/windshift-forms.js\"></script>") {
		t.Fatalf("LongDocument stripped HTML inside fenced code block: %q", got)
	}
}

func TestLongDocumentPreservesDangerousMarkdownURLInsideFencedCodeBlockOnly(t *testing.T) {
	input := "[bad](javascript:alert(1))\n\n```md\n[example](javascript:alert(1))\n```"
	got := LongDocument.Sanitize(input)
	if strings.Contains(got, "[bad](javascript:") {
		t.Fatalf("LongDocument kept dangerous Markdown URL outside code block: %q", got)
	}
	if !strings.Contains(got, "[example](javascript:alert(1))") {
		t.Fatalf("LongDocument rewrote Markdown URL inside fenced code block: %q", got)
	}
}

func TestLongDocumentCodeBlockPlaceholderDoesNotReplaceUserText(t *testing.T) {
	block := "```html\n<div>example</div>\n```\n"
	placeholder := codeBlockPlaceholder(block, 0, "")
	input := placeholder + "\n\n" + block
	got := LongDocument.Sanitize(input)
	if strings.Count(got, "<div>example</div>") != 1 {
		t.Fatalf("LongDocument restored code block into user text too: %q", got)
	}
	if !strings.Contains(got, placeholder) {
		t.Fatalf("LongDocument did not preserve user text resembling placeholder: %q", got)
	}
}
