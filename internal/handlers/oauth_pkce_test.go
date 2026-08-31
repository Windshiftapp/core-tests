package handlers

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func s256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestRequireS256(t *testing.T) {
	if err := requireS256("S256"); err != nil {
		t.Fatalf("S256 should be accepted, got %v", err)
	}
	for _, method := range []string{"", "plain", "PLAIN", "s256", "S128"} {
		if err := requireS256(method); err == nil {
			t.Fatalf("method %q should be rejected", method)
		}
	}
}

func TestVerifyPKCE_S256RoundTrip(t *testing.T) {
	verifier := "a-high-entropy-verifier-value-1234567890"
	challenge := s256Challenge(verifier)

	if err := verifyPKCE(challenge, "S256", verifier); err != nil {
		t.Fatalf("matching S256 verifier should pass, got %v", err)
	}
	if err := verifyPKCE(challenge, "S256", "wrong-verifier"); err == nil {
		t.Fatal("mismatched verifier should fail")
	}
}

func TestVerifyPKCE_RejectsPlainAndEmptyMethods(t *testing.T) {
	// For a stored "plain" method the challenge equals the verifier; even an
	// exact match must be rejected now that plain PKCE is unsupported.
	verifier := "plain-verifier"
	for _, method := range []string{"plain", "", "S128"} {
		if err := verifyPKCE(verifier, method, verifier); err == nil {
			t.Fatalf("method %q must be rejected by verifyPKCE", method)
		}
	}
}
