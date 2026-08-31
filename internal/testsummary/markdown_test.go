//go:build test

package testsummary

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"windshift/internal/repository"
)

func TestRenderMarkdown(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 6, 17, 9, 30, 0, 0, time.UTC)
	end := start.Add(95 * time.Second)

	got := RenderMarkdown(&repository.MarkdownRunHeader{
		RunName:   "Release smoke",
		SetName:   "Core checks",
		StartedAt: sql.NullTime{Time: start, Valid: true},
		EndedAt:   sql.NullTime{Time: end, Valid: true},
	}, []repository.MarkdownResult{
		{Title: "Login | happy path", Status: "passed"},
		{Title: "Payment failure", Status: "failed", ActualResult: "500", Notes: "needs\ntriage"},
		{Title: "Email", Status: "blocked", Notes: "provider down"},
	})

	for _, want := range []string{
		"# Test Run Summary: Release smoke",
		"**Test Set:** Core checks",
		"**Duration:** 1m35s",
		"| ✅ Passed | 1 | 33.3% |",
		"### ❌ Payment failure",
		"### ⚠️ Email",
		"| Login \\| happy path | ✅ Passed | - |",
		"| Payment failure | ❌ Failed | needs triage |",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q:\n%s", want, got)
		}
	}
}

func TestRenderMarkdownEscapesStoredValues(t *testing.T) {
	t.Parallel()

	header := &repository.MarkdownRunHeader{
		RunName: "Release [click](javascript:alert(1))",
		SetName: "Regression\n## Injected heading",
	}
	results := []repository.MarkdownResult{
		{
			Title:        "- case | `code` ![image](https://example.com/pixel.png)",
			Status:       "failed",
			ActualResult: "<img src=x onerror=alert(1)>\n# Injected result",
			Notes:        "![note](javascript:alert(1))",
		},
	}

	markdown := RenderMarkdown(header, results)

	for _, injected := range []string{
		"[click](javascript:alert(1))",
		"\n## Injected heading",
		"![image](https://example.com/pixel.png)",
		"<img src=x onerror=alert(1)>",
		"\n# Injected result",
		"![note](javascript:alert(1))",
	} {
		if strings.Contains(markdown, injected) {
			t.Fatalf("summary contains active Markdown %q:\n%s", injected, markdown)
		}
	}

	for _, literal := range []string{
		`Release \[click\]\(javascript\:alert\(1\)\)`,
		`Regression \#\# Injected heading`,
		"\\<img src\\=x onerror\\=alert\\(1\\)\\>  \n\\# Injected result",
		`\!\[note\]\(javascript\:alert\(1\)\)`,
		"\\- case \\| \\`code\\` \\!\\[image\\]",
		"https\\:\u200b\\/\\/example\\.com\\/pixel\\.png",
	} {
		if !strings.Contains(markdown, literal) {
			t.Fatalf("summary does not preserve escaped text %q:\n%s", literal, markdown)
		}
	}
}
