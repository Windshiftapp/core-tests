//go:build test

package smtp

import (
	"fmt"
	"strings"
	"testing"
)

// TestBuildMimeFoldsLongReferences covers the bug hunt finding that long
// threads produced a References header exceeding SMTP line limits. The header
// must be folded, every physical line must stay under the 998-octet hard
// limit, and no Message-ID may be lost.
func TestBuildMimeFoldsLongReferences(t *testing.T) {
	refs := make([]string, 40)
	for i := range refs {
		refs[i] = fmt.Sprintf("<ws-comment-%d-abcdef123456@example.com>", i)
	}

	msg := buildTestMime(t, mimeOptions{
		FromEmail:  "team@example.com",
		ToEmail:    "customer@example.com",
		Subject:    "Re: long thread",
		HTMLBody:   "<p>hi</p>",
		TextBody:   "hi",
		References: refs,
	})

	for _, line := range strings.Split(msg, "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line exceeds 998 octets (%d): %q", len(line), line)
		}
	}

	headers := messageHeaderBlock(t, []byte(msg))
	if !strings.Contains(headers, "References: <ws-comment-0-abcdef123456@example.com>") {
		t.Fatalf("References header missing first ID:\n%s", headers)
	}
	if !strings.Contains(headers, "\r\n <ws-comment-") {
		t.Fatalf("References header was not folded:\n%s", headers)
	}
	for i, ref := range refs {
		if !strings.Contains(headers, ref) {
			t.Fatalf("References header lost entry %d (%s)", i, ref)
		}
	}
}
