//go:build test

package smtp

import (
	"strings"
	"testing"

	"windshift/internal/emailutil"
)

// TestMentionEmailRendersMessageAndActionLink covers the dedicated @mention
// template: it must carry the actual message body (Markdown rendered for HTML,
// source kept for text), the actor, and the deep link, without executing raw
// HTML from the message.
func TestMentionEmailRendersMessageAndActionLink(t *testing.T) {
	sender := &NotificationSMTPSender{}
	content := "Please review the [token refresh path](https://example.test/tokens).\n\n" +
		"<script>alert('x')</script>"
	data := struct {
		RecipientName string
		ActorName     string
		ItemKey       string
		ItemTitle     string
		SourceLabel   string
		Content       string
		ActionURL     string
	}{
		RecipientName: "Alex",
		ActorName:     "Sam Patel",
		ItemKey:       "ACME-42",
		ItemTitle:     "Cannot reset password",
		SourceLabel:   "a comment",
		Content:       content,
		ActionURL:     "https://app.example.com/workspaces/1/items/42",
	}

	subject, htmlBody, textBody, err := sender.RenderEmail(emailutil.TemplateMention, data)
	if err != nil {
		t.Fatalf("RenderEmail: %v", err)
	}
	if got, want := subject, "Sam Patel mentioned you in ACME-42"; got != want {
		t.Fatalf("subject = %q, want %q", got, want)
	}
	for _, want := range []string{
		"Hi Alex",
		"Sam Patel",
		"a comment",
		"ACME-42",
		"Cannot reset password",
		`<a href="https://example.test/tokens"`,
		`href="https://app.example.com/workspaces/1/items/42"`,
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
	if !strings.Contains(textBody, "Please review the [token refresh path](https://example.test/tokens).") {
		t.Fatalf("plain-text body should keep the Markdown source: %q", textBody)
	}
	if !strings.Contains(textBody, "View in Windshift: https://app.example.com/workspaces/1/items/42") {
		t.Fatalf("plain-text body missing action link: %q", textBody)
	}
}
