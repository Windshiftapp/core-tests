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
