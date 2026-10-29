//go:build test

package smtp

import (
	"strings"
	"testing"

	"windshift/internal/emailutil"
)

// TestPortalReplyRendersMarkdown covers the bug hunt finding that portal
// Markdown was delivered as literal source. Links and tables must survive into
// the HTML part, and raw HTML must stay inert.
func TestPortalReplyRendersMarkdown(t *testing.T) {
	sender := &NotificationSMTPSender{}
	content := "See the [runbook](https://example.test/runbook) for details.\n\n" +
		"| Step | Owner |\n| --- | --- |\n| Deploy | Sam |\n\n" +
		"<script>alert('x')</script>"
	data := struct {
		AuthorName      string
		ItemKey         string
		ItemTitle       string
		Content         string
		OriginalSubject string
	}{
		AuthorName:      "Sam Patel",
		ItemKey:         "OPS-42",
		ItemTitle:       "Deploy checklist",
		Content:         content,
		OriginalSubject: "Deploy checklist",
	}

	_, htmlBody, textBody, err := sender.RenderEmail(emailutil.TemplatePortalReply, data)
	if err != nil {
		t.Fatalf("RenderEmail: %v", err)
	}
	for _, want := range []string{
		`<a href="https://example.test/runbook"`,
		"<table>",
		"<th",
		"Deploy",
		"Sam",
	} {
		if !strings.Contains(htmlBody, want) {
			t.Fatalf("HTML body missing %q\n%s", want, htmlBody)
		}
	}
	if strings.Contains(htmlBody, "<script>alert") {
		t.Fatalf("HTML body rendered executable script: %s", htmlBody)
	}
	if !strings.Contains(htmlBody, "&lt;script&gt;") {
		t.Fatalf("HTML body did not escape raw HTML: %s", htmlBody)
	}
	if !strings.Contains(textBody, "[runbook](https://example.test/runbook)") {
		t.Fatalf("plain-text body should keep the Markdown source: %q", textBody)
	}
}
