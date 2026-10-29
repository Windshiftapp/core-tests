package smtp

import (
	"strings"
	"testing"
)

func replyToMessage(t *testing.T, from, replyTo string) string {
	t.Helper()
	return buildThreadedMimeMessage(t, from, "Sender", ThreadedEmailParams{
		ToEmail:      "customer@example.com",
		ToName:       "Customer",
		ReplyToEmail: replyTo,
		Subject:      "Re: Ticket",
		HTMLBody:     "<p>Hi</p>",
		TextBody:     "Hi",
		MessageID:    "<msg-1@example.com>",
	})
}

func TestBuildMime_EmitsReplyToWhenDistinctFromSender(t *testing.T) {
	msg := replyToMessage(t, "notifications@example.com", "support@example.com")
	if !strings.Contains(msg, "Reply-To: support@example.com") {
		t.Fatalf("expected Reply-To header, got:\n%s", msg)
	}
}

func TestBuildMime_OmitsReplyToWhenSameAsSender(t *testing.T) {
	msg := replyToMessage(t, "Support@Example.com", "support@example.com")
	if strings.Contains(msg, "Reply-To:") {
		t.Fatalf("Reply-To must be omitted when it equals From, got:\n%s", msg)
	}
}

func TestBuildMime_OmitsReplyToWhenEmpty(t *testing.T) {
	msg := replyToMessage(t, "notifications@example.com", "")
	if strings.Contains(msg, "Reply-To:") {
		t.Fatalf("Reply-To must be omitted when unset, got:\n%s", msg)
	}
}

// The reply address is derived from config and must not be able to inject a
// second header through CRLF.
func TestBuildMime_ReplyToRejectsCRLFInjection(t *testing.T) {
	msg := replyToMessage(t, "notifications@example.com", "support@example.com\r\nBcc: attacker@evil.example")
	if strings.Contains(msg, "\r\nBcc:") {
		t.Fatalf("CRLF injection via Reply-To was not neutralized:\n%s", msg)
	}
	if strings.Count(msg, "Reply-To:") != 1 {
		t.Fatalf("expected exactly one Reply-To header, got:\n%s", msg)
	}
}
