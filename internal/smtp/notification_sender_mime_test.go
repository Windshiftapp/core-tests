//go:build test

package smtp

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"
	"testing"
)

// The generated message is the product here, so the contract is a round trip
// through the same libraries a receiver uses: net/mail for the header block
// and multipart.Reader for the body. String matching only survives where the
// exact wire form is itself the contract (boundary shape, folding).

func TestBuildMimeRoundTripsThroughStandardParsers(t *testing.T) {
	tests := []struct {
		name     string
		opts     mimeOptions
		wantFrom *mail.Address
		wantTo   *mail.Address
	}{
		{
			name: "transactional send",
			opts: mimeOptions{
				FromEmail: "notifications@example.com",
				FromName:  "Windshift",
				ToEmail:   "customer@example.com",
				Subject:   "Your invitation",
				TextBody:  "Hello customer,\n\nwelcome aboard.\n",
				HTMLBody:  "<p>Hello customer,</p><p>welcome aboard.</p>",
			},
			wantFrom: &mail.Address{Name: "Windshift", Address: "notifications@example.com"},
			wantTo:   &mail.Address{Address: "customer@example.com"},
		},
		{
			name: "display names with RFC 5322 specials",
			opts: mimeOptions{
				FromEmail: "notifications@example.com",
				FromName:  `Support, "The Team"`,
				ToEmail:   "customer@example.com",
				ToName:    "Lastname, Firstname",
				Subject:   "We received your request",
				TextBody:  "hi",
				HTMLBody:  "<p>hi</p>",
			},
			wantFrom: &mail.Address{Name: `Support, "The Team"`, Address: "notifications@example.com"},
			wantTo:   &mail.Address{Name: "Lastname, Firstname", Address: "customer@example.com"},
		},
		{
			name: "non-ASCII subject and display names",
			opts: mimeOptions{
				FromEmail: "notifications@example.com",
				FromName:  "Björn Müller",
				ToEmail:   "customer@example.com",
				ToName:    "Zoë 王",
				Subject:   "Re: Grüße aus Köln 🎉",
				TextBody:  "Hallo,\n",
				HTMLBody:  "<p>Hallo,</p>",
			},
			wantFrom: &mail.Address{Name: "Björn Müller", Address: "notifications@example.com"},
			wantTo:   &mail.Address{Name: "Zoë 王", Address: "customer@example.com"},
		},
		{
			name: "threaded reply",
			opts: mimeOptions{
				FromEmail:    "notifications@example.com",
				ToEmail:      "customer@example.com",
				ToName:       "Customer",
				ReplyToEmail: "support@example.com",
				ReplyToName:  "Windshift Support",
				Subject:      "Re: Ticket 42",
				TextBody:     "reply body",
				HTMLBody:     "<p>reply body</p>",
				MessageID:    "out-1@example.com",
				InReplyTo:    "in-1@example.com",
				References:   []string{"in-0@example.com", "in-1@example.com"},
			},
			wantFrom: &mail.Address{Address: "notifications@example.com"},
			wantTo:   &mail.Address{Name: "Customer", Address: "customer@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, err := buildMime(tt.opts)
			if err != nil {
				t.Fatalf("buildMime: %v", err)
			}

			parsed := parseGeneratedMime(t, message)

			if got, want := parsed.parts["text/plain"], normalizeCRLF(tt.opts.TextBody); got != want {
				t.Fatalf("text/plain part = %q, want %q", got, want)
			}
			if got, want := parsed.parts["text/html"], normalizeCRLF(tt.opts.HTMLBody); got != want {
				t.Fatalf("text/html part = %q, want %q", got, want)
			}

			from := singleMailbox(t, parsed.header, "From")
			if from.Address != tt.wantFrom.Address || from.Name != tt.wantFrom.Name {
				t.Fatalf("From = %q <%q>, want %q <%q>", from.Name, from.Address, tt.wantFrom.Name, tt.wantFrom.Address)
			}
			to := singleMailbox(t, parsed.header, "To")
			if to.Address != tt.wantTo.Address || to.Name != tt.wantTo.Name {
				t.Fatalf("To = %q <%q>, want %q <%q>", to.Name, to.Address, tt.wantTo.Name, tt.wantTo.Address)
			}

			subject, err := (&mime.WordDecoder{}).DecodeHeader(parsed.header.Get("Subject"))
			if err != nil {
				t.Fatalf("decode Subject %q: %v", parsed.header.Get("Subject"), err)
			}
			if subject != tt.opts.Subject {
				t.Fatalf("Subject = %q, want %q", subject, tt.opts.Subject)
			}

			if tt.opts.ReplyToEmail != "" {
				replyTo := singleMailbox(t, parsed.header, "Reply-To")
				if replyTo.Address != tt.opts.ReplyToEmail || replyTo.Name != tt.opts.ReplyToName {
					t.Fatalf("Reply-To = %q <%q>, want %q <%q>", replyTo.Name, replyTo.Address, tt.opts.ReplyToName, tt.opts.ReplyToEmail)
				}
			} else if parsed.header.Get("Reply-To") != "" {
				t.Fatalf("unexpected Reply-To %q", parsed.header.Get("Reply-To"))
			}

			if tt.opts.MessageID != "" {
				if got, want := parsed.header.Get("Message-ID"), "<"+tt.opts.MessageID+">"; got != want {
					t.Fatalf("Message-ID = %q, want %q", got, want)
				}
				if got, want := parsed.header.Get("In-Reply-To"), "<"+tt.opts.InReplyTo+">"; got != want {
					t.Fatalf("In-Reply-To = %q, want %q", got, want)
				}
				// Folded References must survive as a list of message IDs, and the
				// inbound parser must recover exactly the IDs we sent so replies
				// stay in the same thread.
				refs := parseMessageIDList(t, parsed.header.Get("References"))
				if strings.Join(refs, " ") != strings.Join(tt.opts.References, " ") {
					t.Fatalf("References = %v, want %v", refs, tt.opts.References)
				}
			}
		})
	}
}

var hexBoundaryPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// A boundary that starts with a hyphen run made SES reject the whole message
// (#372), and a boundary reused across messages makes accidental delimiter
// collisions more likely. It must be random hex with no leading hyphen.
func TestBuildMimeGeneratesRandomHexBoundary(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 50; i++ {
		message, err := buildMime(mimeOptions{
			FromEmail: "team@example.com",
			ToEmail:   "customer@example.com",
			Subject:   "boundary",
			TextBody:  "text",
			HTMLBody:  "<p>text</p>",
		})
		if err != nil {
			t.Fatalf("buildMime: %v", err)
		}

		msg, err := mail.ReadMessage(bytes.NewReader(message))
		if err != nil {
			t.Fatalf("net/mail rejected the message: %v", err)
		}
		_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("parse Content-Type: %v", err)
		}
		boundary := params["boundary"]
		if !hexBoundaryPattern.MatchString(boundary) {
			t.Fatalf("boundary %q is not 32 hex characters", boundary)
		}
		if strings.HasPrefix(boundary, "-") {
			t.Fatalf("boundary %q starts with a hyphen", boundary)
		}
		if _, duplicate := seen[boundary]; duplicate {
			t.Fatalf("boundary %q was reused across messages", boundary)
		}
		seen[boundary] = struct{}{}

		// The delimiters must use the same value the Content-Type announces.
		if !bytes.Contains(message, []byte("\r\n--"+boundary+"\r\n")) {
			t.Fatalf("message body has no opening delimiter for %q:\n%s", boundary, message)
		}
		if !bytes.HasSuffix(message, []byte("\r\n--"+boundary+"--\r\n")) {
			t.Fatalf("message does not close with the delimiter for %q:\n%s", boundary, message)
		}
	}
}

// The body used to be declared 8bit unconditionally, so long lines and
// non-ASCII text were only accepted by lenient receivers.
func TestBuildMimeTransferEncodesParts(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "very long line", body: strings.Repeat("windshift ", 400)},
		{name: "single unbroken token", body: strings.Repeat("x", 5000)},
		{name: "non-ASCII", body: "Grüße aus Köln – 東京 – ✅\n"},
		{name: "trailing spaces and equals", body: "value = 1   \r\nnext == 2  \r\n"},
		{name: "leading dots", body: ".hidden\r\n..double\r\nnormal\r\n"},
		{name: "mixed", body: strings.Repeat("Björn ", 200) + "\n" + strings.Repeat("-", 1500)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, err := buildMime(mimeOptions{
				FromEmail: "team@example.com",
				ToEmail:   "customer@example.com",
				Subject:   "encoding",
				TextBody:  tt.body,
				HTMLBody:  "<p>" + tt.body + "</p>",
			})
			if err != nil {
				t.Fatalf("buildMime: %v", err)
			}

			assertNoOverlongLines(t, message)

			msg, err := mail.ReadMessage(bytes.NewReader(message))
			if err != nil {
				t.Fatalf("net/mail rejected the message: %v", err)
			}
			_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
			if err != nil {
				t.Fatalf("parse Content-Type: %v", err)
			}
			reader := multipart.NewReader(msg.Body, params["boundary"])
			for {
				part, err := reader.NextPart()
				if err != nil {
					break
				}
				encoding := part.Header.Get("Content-Transfer-Encoding")
				if strings.EqualFold(encoding, "8bit") || strings.EqualFold(encoding, "binary") {
					t.Fatalf("part declares %q, which receivers may reject", encoding)
				}
				decoded, err := decodePartBody(encoding, part)
				if err != nil {
					t.Fatalf("decode part: %v", err)
				}
				want := normalizeCRLF(tt.body)
				if strings.HasPrefix(part.Header.Get("Content-Type"), "text/html") {
					want = normalizeCRLF("<p>" + tt.body + "</p>")
				}
				if decoded != want {
					t.Fatalf("decoded part differs from input:\n got %q\nwant %q", decoded, want)
				}
			}
		})
	}
}

// sesRejectionPattern mirrors the SES 554 the #372 report quoted:
// "In parameter list <; boundary=...>, expected ';', got "=". It is a shape
// check on the emitted Content-Type line, not a full parser.
var sesRejectionPattern = regexp.MustCompile(`boundary=----=`)

// strictViolations lists every rule the captured-SES-style validator finds
// broken. It returns problems instead of failing the test so the rules can be
// asserted directly, including on fixtures that must be rejected.
func strictViolations(message []byte) []string {
	var problems []string
	if sesRejectionPattern.Match(message) {
		problems = append(problems, "boundary starts with a hyphen run")
	}
	for _, line := range strings.Split(string(message), "\n") {
		if line != "" && !strings.HasSuffix(line, "\r") {
			problems = append(problems, fmt.Sprintf("line is not CRLF terminated: %q", line))
			break
		}
	}
	for _, line := range strings.Split(string(message), "\r\n") {
		if len(line) > 998 {
			problems = append(problems, fmt.Sprintf("line exceeds 998 octets (%d)", len(line)))
			break
		}
	}
	if idx := bytes.Index(message, []byte("\r\n\r\n")); idx < 0 {
		return append(problems, "message has no header/body separator")
	}
	msg, err := mail.ReadMessage(bytes.NewReader(message))
	if err != nil {
		return append(problems, fmt.Sprintf("net/mail rejected the message: %v", err))
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		return append(problems, fmt.Sprintf("Content-Type is not a valid parameter list: %v", err))
	}
	if mediaType != "multipart/alternative" {
		problems = append(problems, fmt.Sprintf("Content-Type media type is %q", mediaType))
	}
	boundary := params["boundary"]
	if boundary == "" {
		return append(problems, "Content-Type has no boundary parameter")
	}
	if strings.HasPrefix(boundary, "-") {
		problems = append(problems, fmt.Sprintf("boundary %q starts with a hyphen run", boundary))
	}

	body := string(message[bytes.Index(message, []byte("\r\n\r\n"))+4:])
	delimiters, parts := 0, 0
	for _, line := range strings.Split(body, "\r\n") {
		if !strings.HasPrefix(line, "--") {
			continue
		}
		switch line {
		case "--" + boundary:
			delimiters++
			parts++
		case "--" + boundary + "--":
			delimiters++
		default:
			problems = append(problems, fmt.Sprintf("unexpected delimiter %q for boundary %q", line, boundary))
		}
	}
	if delimiters != 3 || parts != 2 {
		problems = append(problems, fmt.Sprintf("body has %d delimiters / %d parts, want 3 / 2", delimiters, parts))
	}
	encodings, missing := declaredPartEncodings(message, boundary)
	if missing {
		problems = append(problems, "a part does not declare Content-Transfer-Encoding")
	}
	for _, encoding := range encodings {
		if encoding != "quoted-printable" {
			problems = append(problems, fmt.Sprintf("part declares Content-Transfer-Encoding %q", encoding))
		}
	}
	return problems
}

// validateStrictMessage stands in for a provider that is stricter than Gmail.
// It enforces the rules the reported failures came from: the Content-Type
// parameter syntax, a boundary that matches the body delimiters, one CRLF
// terminated part per declared body, and the 998-octet line limit.
func validateStrictMessage(t *testing.T, message []byte) {
	t.Helper()

	if sesRejectionPattern.Match(message) {
		t.Fatalf("message would be rejected as malformed MIME (boundary=----=):\n%s", message)
	}
	if bytes.Contains(message, []byte("\n")) && bytes.Contains(message, []byte("\r\n\n")) {
		t.Fatalf("message contains a bare LF line ending:\n%q", message)
	}
	for _, line := range strings.Split(string(message), "\n") {
		if line != "" && !strings.HasSuffix(line, "\r") {
			t.Fatalf("line is not CRLF terminated: %q", line)
		}
	}
	assertNoOverlongLines(t, message)

	// The header block must be a run of RFC 5322 fields: a token, a colon, and
	// no line that looks like a continuation without a preceding field.
	headerBlock := messageHeaderBlock(t, message)
	fields := 0
	for _, line := range strings.Split(headerBlock, "\r\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon <= 0 {
			t.Fatalf("header line is not a field: %q", line)
		}
		if strings.ContainsAny(line[:colon], " =;<>") {
			t.Fatalf("header field name is not a token: %q", line[:colon])
		}
		fields++
	}
	if fields == 0 {
		t.Fatal("message has no header fields")
	}

	msg, err := mail.ReadMessage(bytes.NewReader(message))
	if err != nil {
		t.Fatalf("net/mail rejected the message: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("Content-Type is not a valid parameter list: %v", err)
	}
	if mediaType != "multipart/alternative" {
		t.Fatalf("Content-Type media type = %q, want multipart/alternative", mediaType)
	}
	boundary := params["boundary"]
	if boundary == "" {
		t.Fatalf("Content-Type has no boundary parameter: %q", msg.Header.Get("Content-Type"))
	}
	if strings.HasPrefix(boundary, "-") {
		t.Fatalf("boundary %q starts with a hyphen run", boundary)
	}

	// Walk the raw body so the delimiters are checked as bytes on the wire,
	// not just as whatever the reader happened to tolerate.
	rawBody := message[bytes.Index(message, []byte("\r\n\r\n"))+4:]
	scanner := bufio.NewScanner(bytes.NewReader(rawBody))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	delimiters := 0
	parts := 0
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if !strings.HasPrefix(line, "--") {
			continue
		}
		if line == "--"+boundary {
			delimiters++
			parts++
			continue
		}
		if line == "--"+boundary+"--" {
			delimiters++
			continue
		}
		t.Fatalf("unexpected delimiter %q for boundary %q", line, boundary)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan body: %v", err)
	}
	if delimiters != 3 || parts != 2 {
		t.Fatalf("body has %d delimiters / %d parts, want 3 / 2 for boundary %q", delimiters, parts, boundary)
	}

	// Every text part must declare quoted-printable and decode under it.
	encodings := partEncodings(t, message)
	if len(encodings) != 2 {
		t.Fatalf("%d parts, want 2", len(encodings))
	}
	for i, encoding := range encodings {
		if encoding != "quoted-printable" {
			t.Fatalf("part %d declares Content-Transfer-Encoding %q, want quoted-printable", i, encoding)
		}
	}

	reader := multipart.NewReader(msg.Body, boundary)
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		if _, err := io.ReadAll(quotedprintable.NewReader(part)); err != nil {
			t.Fatalf("quoted-printable part does not decode: %v", err)
		}
	}
}

// partEncodings returns the Content-Transfer-Encoding declared by each part,
// read from the raw message. multipart.Reader drops the header from what it
// returns because net/textproto does not canonicalize that key, so the parsed
// header alone cannot tell whether the part was declared correctly.
func partEncodings(t *testing.T, message []byte) []string {
	t.Helper()
	boundary := partBoundary(t, message)
	encodings, missing := declaredPartEncodings(message, boundary)
	if missing {
		t.Fatalf("part does not declare Content-Transfer-Encoding:\n%s", message)
	}
	return encodings
}

// declaredPartEncodings splits the raw body on the boundary and returns the
// transfer encoding of each part, reporting whether any part left it out.
func declaredPartEncodings(message []byte, boundary string) (encodings []string, missing bool) {
	body := string(message[bytes.Index(message, []byte("\r\n\r\n"))+4:])

	for _, block := range strings.Split(body, "--"+boundary) {
		// Each block is the part content that followed one delimiter line.
		block = strings.TrimPrefix(block, "\r\n")
		if block == "" || strings.HasPrefix(block, "--") {
			continue // closing delimiter or epilogue
		}
		headerBlock, _, found := strings.Cut(block, "\r\n\r\n")
		if !found {
			return encodings, true
		}
		encoding := ""
		for _, line := range strings.Split(headerBlock, "\r\n") {
			if value, ok := strings.CutPrefix(line, "Content-Transfer-Encoding:"); ok {
				encoding = strings.TrimSpace(value)
			}
		}
		if encoding == "" {
			return encodings, true
		}
		encodings = append(encodings, encoding)
	}
	return encodings, false
}

func partBoundary(t *testing.T, message []byte) string {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(message))
	if err != nil {
		t.Fatalf("net/mail rejected the message: %v", err)
	}
	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse Content-Type: %v", err)
	}
	return params["boundary"]
}

func TestBuildMimePassesStrictProviderValidation(t *testing.T) {
	message, err := buildMime(mimeOptions{
		FromEmail: "notifications@example.com",
		FromName:  "Windshift Support",
		ToEmail:   "customer@example.com",
		ToName:    "Lastname, Firstname",
		Subject:   "Re: Grüße — your request",
		TextBody:  strings.Repeat("A long sentence that would exceed the line limit if it were not encoded. ", 30) + "\nGrüße\n",
		HTMLBody:  "<p>" + strings.Repeat("long html ", 200) + "</p>",
		MessageID: "out-9@example.com",
		InReplyTo: "in-9@example.com",
		References: []string{
			"in-7@example.com",
			"in-8@example.com",
			"in-9@example.com",
		},
	})
	if err != nil {
		t.Fatalf("buildMime: %v", err)
	}

	validateStrictMessage(t, message)
}

// The boundary the builder used to emit, reproduced verbatim. Feeding it
// through the same strict validator proves the #372 failure mode is covered:
// this test fails on the old shape and passes on the new one.
func legacyBoundaryMessage(boundary, encoding string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: team@example.com\r\nTo: customer@example.com\r\nSubject: legacy\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%s\r\n\r\n", boundary)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: %s\r\n\r\nhi\r\n\r\n", boundary, encoding)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: %s\r\n\r\n<p>hi</p>\r\n\r\n", boundary, encoding)
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

// The fixture is the message the builder produced before this change. The
// validator must reject it for the reported reason, and must accept the same
// bytes once the boundary and transfer encoding are the ones we emit now.
func TestStrictValidatorRejectsLegacyBoundaryShape(t *testing.T) {
	legacy := legacyBoundaryMessage("----=_NextPart_1717171717171717", "8bit")
	if !sesRejectionPattern.Match(legacy) {
		t.Fatalf("legacy fixture does not reproduce the reported boundary shape:\n%s", legacy)
	}

	problems := strictViolations(legacy)
	if len(problems) == 0 {
		t.Fatal("strict validator accepted the legacy boundary shape")
	}
	if !strings.Contains(strings.Join(problems, "\n"), "hyphen run") {
		t.Fatalf("validator rejected the fixture for another reason: %v", problems)
	}

	valid := legacyBoundaryMessage(hex.EncodeToString(bytes.Repeat([]byte{0xab}, 16)), "quoted-printable")
	validateStrictMessage(t, valid)
}
