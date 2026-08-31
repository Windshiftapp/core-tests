package email

import (
	"strings"
	"testing"
)

func TestReadOAuthResponseBodyRejectsOversizedResponses(t *testing.T) {
	if _, err := readOAuthResponseBody(strings.NewReader(strings.Repeat("x", maxOAuthResponseBytes+1))); err == nil {
		t.Fatal("oversized OAuth response unexpectedly succeeded")
	}
	body, err := readOAuthResponseBody(strings.NewReader(`{"access_token":"ok"}`))
	if err != nil {
		t.Fatalf("bounded response: %v", err)
	}
	if string(body) != `{"access_token":"ok"}` {
		t.Fatalf("body = %q", body)
	}
}
