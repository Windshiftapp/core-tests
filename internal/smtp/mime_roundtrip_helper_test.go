//go:build test

package smtp

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"
	"testing"
)

// parsedMimeMessage is the received view of a generated message: the headers
// net/mail parsed and the decoded bodies multipart.Reader returned.
type parsedMimeMessage struct {
	header *mail.Header
	parts  map[string]string
}

// parseGeneratedMime parses a generated message the way a receiving client
// would and fails the test when the MIME structure is not well formed. This is
// the contract for outbound mail: the boundary announced in Content-Type must
// match the delimiters in the body, and each declared part must decode.
func parseGeneratedMime(t *testing.T, message []byte) parsedMimeMessage {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(message))
	if err != nil {
		t.Fatalf("net/mail rejected the generated message: %v\n%s", err, message)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("Content-Type %q is not parseable: %v", msg.Header.Get("Content-Type"), err)
	}
	if mediaType != "multipart/alternative" {
		t.Fatalf("Content-Type media type = %q, want multipart/alternative", mediaType)
	}
	boundary := params["boundary"]
	if boundary == "" {
		t.Fatalf("Content-Type has no boundary parameter: %q", msg.Header.Get("Content-Type"))
	}

	body, err := io.ReadAll(msg.Body)
	if err != nil {
		t.Fatalf("read message body: %v", err)
	}
	encodings, missing := declaredPartEncodings(message, boundary)
	if missing {
		t.Fatalf("a part does not declare Content-Transfer-Encoding:\n%s", message)
	}

	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	parts := make(map[string]string, 2)
	for index := 0; ; index++ {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("multipart reader rejected the generated body: %v\n%s", err, message)
		}
		partType, _, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("part Content-Type %q is not parseable: %v", part.Header.Get("Content-Type"), err)
		}
		if index >= len(encodings) {
			t.Fatalf("multipart reader returned more parts than the raw body declares")
		}
		decoded, err := decodePartBody(encodings[index], part)
		if err != nil {
			t.Fatalf("decode %s part: %v", partType, err)
		}
		if _, duplicate := parts[partType]; duplicate {
			t.Fatalf("message contains more than one %s part", partType)
		}
		parts[partType] = decoded
	}
	if len(parts) != 2 {
		t.Fatalf("message contains %d parts, want text/plain and text/html", len(parts))
	}
	return parsedMimeMessage{header: &msg.Header, parts: parts}
}

// decodePartBody decodes a part according to its declared transfer encoding.
// Only the encodings the builder may emit are supported; anything else is a
// failure rather than a pass-through. MIME bodies use CRLF line endings, so
// decoded text is normalized to LF before comparing against the input.
func decodePartBody(encoding string, part io.Reader) (string, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		decoded, err := io.ReadAll(quotedprintable.NewReader(part))
		return normalizeCRLF(string(decoded)), err
	case "", "7bit", "8bit", "binary":
		decoded, err := io.ReadAll(part)
		return normalizeCRLF(string(decoded)), err
	default:
		return "", &unsupportedEncodingError{encoding: encoding}
	}
}

func normalizeCRLF(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

type unsupportedEncodingError struct{ encoding string }

func (e *unsupportedEncodingError) Error() string {
	return "unsupported Content-Transfer-Encoding " + e.encoding
}

// assertNoOverlongLines fails when any physical line exceeds the 998-octet
// SMTP limit, including the header block and the multipart delimiters.
func assertNoOverlongLines(t *testing.T, message []byte) {
	t.Helper()
	for _, line := range strings.Split(string(message), "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line exceeds 998 octets (%d): %q", len(line), line)
		}
	}
}

// messageHeaderBlock returns everything before the blank line that separates
// the headers from the body.
func messageHeaderBlock(t *testing.T, message []byte) string {
	t.Helper()
	index := bytes.Index(message, []byte("\r\n\r\n"))
	if index < 0 {
		t.Fatalf("message has no header/body separator:\n%s", message)
	}
	return string(message[:index])
}

// messageIDPattern matches one RFC 5322 msg-id, bracketed form included.
var messageIDPattern = regexp.MustCompile(`<[^<>\s]+>`)

// parseMessageIDList extracts the message IDs from a References header, which
// may be folded across several lines.
func parseMessageIDList(t *testing.T, value string) []string {
	t.Helper()
	ids := messageIDPattern.FindAllString(value, -1)
	for i, id := range ids {
		ids[i] = strings.TrimSuffix(strings.TrimPrefix(id, "<"), ">")
	}
	return ids
}

// singleMailbox parses a single-address header and fails when the value was
// read as an address list.
func singleMailbox(t *testing.T, header *mail.Header, name string) *mail.Address {
	t.Helper()
	raw := header.Get(name)
	if raw == "" {
		t.Fatalf("%s header is missing", name)
	}
	addresses, err := header.AddressList(name)
	if err != nil {
		t.Fatalf("parse %s address list from %q: %v", name, raw, err)
	}
	if len(addresses) != 1 {
		t.Fatalf("%s %q parsed as %d mailboxes, want 1: %v", name, raw, len(addresses), addresses)
	}
	return addresses[0]
}
