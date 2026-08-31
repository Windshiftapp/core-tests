package email

import "testing"

func TestParserFallsBackToRawHeadersWhenEnvelopeIsMissing(t *testing.T) {
	raw := []byte("Date: Fri, 17 Jul 2026 10:00:00 +0200\r\n" +
		"From: =?UTF-8?Q?Bj=C3=B6rn_Customer?= <customer@example.com>\r\n" +
		"To: Intake <ingest@example.com>\r\n" +
		"Subject: =?UTF-8?Q?Pr=C3=BCfung_request?=\r\n" +
		"Message-ID: <reply@example.com>\r\n" +
		"In-Reply-To: <previous@example.com>\r\n" +
		"References: <root@example.com> <previous@example.com>\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n\r\nBody")

	parsed := NewParser().Parse(&FetchedMessage{UID: 9, Raw: raw})
	if parsed.MessageID != "<reply@example.com>" || parsed.InReplyTo != "<previous@example.com>" {
		t.Fatalf("thread headers = Message-ID %q, In-Reply-To %q", parsed.MessageID, parsed.InReplyTo)
	}
	if len(parsed.References) != 2 || parsed.References[0] != "<root@example.com>" || parsed.References[1] != "<previous@example.com>" {
		t.Fatalf("References = %v", parsed.References)
	}
	if parsed.Subject != "Prüfung request" || parsed.From.Name != "Björn Customer" || parsed.From.Address != "customer@example.com" {
		t.Fatalf("raw-header envelope = subject %q, from %#v", parsed.Subject, parsed.From)
	}
	if len(parsed.To) != 1 || parsed.To[0].Address != "ingest@example.com" {
		t.Fatalf("To = %#v", parsed.To)
	}
	if parsed.Date.IsZero() {
		t.Fatalf("Date = %v", parsed.Date)
	}
	if parsed.PlainBody != "Body" {
		t.Fatalf("body = %q", parsed.PlainBody)
	}
}

func TestCanonicalMessageIDRejectsHeaderSyntax(t *testing.T) {
	for _, invalid := range []string{"", "<>", "two words@example.com", "a@example.com\r\nBcc: victim@example.com"} {
		if got := canonicalMessageID(invalid); got != "" {
			t.Fatalf("canonicalMessageID(%q) = %q, want empty", invalid, got)
		}
	}
	for input, want := range map[string]string{
		"message@example.com":   "<message@example.com>",
		"<message@example.com>": "<message@example.com>",
	} {
		if got := canonicalMessageID(input); got != want {
			t.Fatalf("canonicalMessageID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDecodeHeaderWordSupportsLegacyCharsets(t *testing.T) {
	if got := decodeHeaderWord("=?windows-1252?Q?Pr=FCfung?="); got != "Prüfung" {
		t.Fatalf("decoded windows-1252 header = %q", got)
	}
}
