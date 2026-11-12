//go:build test

package markdown

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		contains   []string
		notContain []string
	}{
		{
			name:     "angle brackets in inline code",
			source:   "`Promise<Anything>`",
			contains: []string{"<code>Promise&lt;Anything&gt;</code>"},
		},
		{
			name:     "raw HTML is visible",
			source:   "before <script>alert(1)</script> after",
			contains: []string{"&lt;script&gt;alert(1)&lt;/script&gt;"},
			notContain: []string{
				"<script>",
			},
		},
		{
			name:       "dangerous link loses href",
			source:     "[click](javascript:alert(1))",
			contains:   []string{"<p>click</p>"},
			notContain: []string{"javascript:", "href="},
		},
		{
			name:       "SVG data image is rejected",
			source:     "![x](data:image/svg+xml;base64,PHN2Zz4=)",
			notContain: []string{"data:image/svg"},
		},
		{
			name:     "raster data image is allowed",
			source:   "![x](data:image/png;base64,iVBORw0KGgo=)",
			contains: []string{`src="data:image/png;base64,iVBORw0KGgo="`},
		},
		{
			name:       "protocol relative URL is rejected",
			source:     "[click](//evil.example/path)",
			notContain: []string{"evil.example", "href="},
		},
		{
			name:     "numeric page URL is allowed",
			source:   "[plan](page:185)",
			contains: []string{`href="page:185"`},
		},
		{
			name:       "non-numeric page URL is rejected",
			source:     "[click](page:javascript)",
			notContain: []string{"href="},
		},
		{
			name:       "backslash relative URL is rejected",
			source:     `[click](\\evil.example/path)`,
			notContain: []string{"href="},
		},
		{
			name:       "encoded backslash URL is rejected",
			source:     `[click](/%5cevil.example/path)`,
			notContain: []string{"href="},
		},
		{
			name:       "data URL is rejected for links",
			source:     `[click](data:image/png;base64,iVBORw0KGgo=)`,
			notContain: []string{"href="},
		},
		{
			name:     "GFM table and task list are supported",
			source:   "| A | B |\n| - | - |\n| 1 | 2 |\n\n- [x] done",
			contains: []string{"<table>", "<td>1</td>", `type="checkbox"`, "checked", "disabled"},
		},
		{
			name:     "Milkdown break is structural",
			source:   "one<br />two",
			contains: []string{"one<br>two"},
		},
		{
			name:       "mention id token renders as display name",
			source:     "hi @{John Doe#42}!",
			contains:   []string{"@John Doe"},
			notContain: []string{"#42", "{"},
		},
		{
			name:     "mention id token inside inline code is literal",
			source:   "`@{John Doe#42}`",
			contains: []string{"<code>@{John Doe#42}</code>"},
		},
		{
			name:     "mention id token inside fenced code is literal",
			source:   "```\n@{John Doe#42}\n```",
			contains: []string{"@{John Doe#42}"},
		},
		{
			name:     "mention id token glued to a word is not rewritten",
			source:   "foo@{John Doe#42}",
			contains: []string{"foo@{John Doe#42}"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			html, err := Render(test.source)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			for _, expected := range test.contains {
				if !strings.Contains(html, expected) {
					t.Errorf("Render() = %q, want substring %q", html, expected)
				}
			}
			for _, forbidden := range test.notContain {
				if strings.Contains(html, forbidden) {
					t.Errorf("Render() = %q, must not contain %q", html, forbidden)
				}
			}
		})
	}
}

func TestStripMentionIDs(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"embedded id", "hi @{John Doe#42}!", "hi @John Doe!"},
		{"legacy forms untouched", `hi @jdoe and @"Jane Doe"`, `hi @jdoe and @"Jane Doe"`},
		{"multiple tokens", "@{A B#1} and @{C D#2}", "@A B and @C D"},
		{"missing id untouched", "@{John Doe}", "@{John Doe}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := StripMentionIDs(test.source); got != test.want {
				t.Fatalf("StripMentionIDs(%q) = %q, want %q", test.source, got, test.want)
			}
		})
	}
}
