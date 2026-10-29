//go:build test

package smtp

import (
	"net/mail"
	"strings"
	"testing"
)

// parseMailboxHeader parses a single-address header value the way a receiving
// MTA would, so an address list smuggled into a display name shows up as more
// than one mailbox.
func parseMailboxHeader(t *testing.T, headerValue string) []*mail.Address {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader("From: " + headerValue + "\r\nSubject: x\r\n\r\nbody\r\n"))
	if err != nil {
		t.Fatalf("parse message with header %q: %v", headerValue, err)
	}
	addresses, err := msg.Header.AddressList("From")
	if err != nil {
		t.Fatalf("parse address list from %q: %v", headerValue, err)
	}
	return addresses
}

// A portal customer whose display name carries RFC 5322 specials (Entra sends
// "Lastname, Firstname") used to produce an unquoted phrase. Receivers split
// the header into an address list, so intake receipts were addressed to the
// bare last name instead of the customer address.
func TestEncodeMailboxQuotesDisplayNameWithSpecials(t *testing.T) {
	tests := []struct {
		name        string
		displayName string
		wantName    string
		wantEncoded bool
	}{
		{name: "comma separated name", displayName: "Lastname, Firstname", wantName: "Lastname, Firstname"},
		{name: "semicolon", displayName: "Semi; colon", wantName: "Semi; colon"},
		{name: "angle brackets and at sign", displayName: "a<b@c", wantName: "a<b@c"},
		{name: "double quotes", displayName: `Quote "x" y`, wantName: `Quote "x" y`},
		{name: "plain name", displayName: "Plain Name", wantName: "Plain Name"},
		{name: "non-ascii name", displayName: "Björn Müller", wantName: "Björn Müller", wantEncoded: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := encodeMailbox(tt.displayName, "customer@example.com")

			addresses := parseMailboxHeader(t, encoded)
			if len(addresses) != 1 {
				t.Fatalf("encoded %q parsed as %d mailboxes, want 1: %v", encoded, len(addresses), addresses)
			}
			if addresses[0].Address != "customer@example.com" {
				t.Fatalf("address = %q, want customer@example.com", addresses[0].Address)
			}
			if addresses[0].Name != tt.wantName {
				t.Fatalf("display name = %q, want %q", addresses[0].Name, tt.wantName)
			}
			if gotEncoded := strings.HasPrefix(encoded, "=?"); gotEncoded != tt.wantEncoded {
				t.Fatalf("encoded-word used = %v, want %v (%q)", gotEncoded, tt.wantEncoded, encoded)
			}
		})
	}
}

func TestEncodeMailboxOmitsMissingDisplayName(t *testing.T) {
	if got := encodeMailbox("", "customer@example.com"); got != "customer@example.com" {
		t.Fatalf("encodeMailbox with empty name = %q, want bare address", got)
	}
}

// The receipt that started this bug report must address exactly one mailbox
// and keep the customer address, so the To header is never rewritten into a
// bare last name by the receiving MTA.
func TestBuildMimeAddressesSingleRecipientForCommaDisplayName(t *testing.T) {
	message := buildTestMime(t, mimeOptions{
		FromEmail: "team@example.com",
		FromName:  "Windshift Support",
		ToEmail:   "customer@example.com",
		ToName:    "Lastname, Firstname",
		Subject:   "We received your request",
		HTMLBody:  "<p>hi</p>",
		TextBody:  "hi",
	})

	// net/mail stops reading headers at the first empty line, so a message is
	// only checked as a whole once the address headers are correct on their
	// own.
	msg, err := mail.ReadMessage(strings.NewReader(message))
	if err != nil {
		t.Fatalf("net/mail rejected the message: %v", err)
	}
	to := singleMailbox(t, &msg.Header, "To")
	if to.Address != "customer@example.com" {
		t.Fatalf("To address = %q, want customer@example.com", to.Address)
	}
	if to.Name != "Lastname, Firstname" {
		t.Fatalf("To display name = %q, want Lastname, Firstname", to.Name)
	}
}
