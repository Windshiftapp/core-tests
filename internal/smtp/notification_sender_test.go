package smtp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"windshift/internal/models"
	"windshift/internal/utils"
)

// buildThreadedMimeMessage is a thin adapter over the package-private
// buildMime / mimeOptions used by the production threaded-send path. It
// preserves the (fromEmail, fromName, ThreadedEmailParams) shape that the
// existing tests are written against without forcing them to construct
// mimeOptions inline.
func buildThreadedMimeMessage(fromEmail, fromName string, p ThreadedEmailParams) string {
	return buildMime(mimeOptions{
		FromEmail:  fromEmail,
		FromName:   fromName,
		ToEmail:    p.ToEmail,
		ToName:     p.ToName,
		Subject:    p.Subject,
		HTMLBody:   p.HTMLBody,
		TextBody:   p.TextBody,
		MessageID:  p.MessageID,
		InReplyTo:  p.InReplyTo,
		References: p.References,
	})
}

func TestBuildThreadedMimeMessage_ContainsHeaders(t *testing.T) {
	params := ThreadedEmailParams{
		ToEmail:    "to@example.com",
		ToName:     "Recipient",
		Subject:    "Re: Test",
		HTMLBody:   "<p>Hello</p>",
		TextBody:   "Hello",
		MessageID:  "<msg-123@example.com>",
		InReplyTo:  "<orig-456@example.com>",
		References: []string{"<orig-456@example.com>", "<reply-789@example.com>"},
	}

	msg := buildThreadedMimeMessage("from@example.com", "Sender", params)

	if !strings.Contains(msg, "Message-ID: <msg-123@example.com>") {
		t.Error("Expected Message-ID header in output")
	}
	if !strings.Contains(msg, "In-Reply-To: <orig-456@example.com>") {
		t.Error("Expected In-Reply-To header in output")
	}
	if !strings.Contains(msg, "References: <orig-456@example.com> <reply-789@example.com>") {
		t.Error("Expected References header with both message IDs")
	}
	if !strings.Contains(msg, "Subject: Re: Test") {
		t.Error("Expected Subject header in output")
	}
}

func TestBuildThreadedMimeMessage_OmitsEmptyHeaders(t *testing.T) {
	params := ThreadedEmailParams{
		ToEmail:  "to@example.com",
		Subject:  "Test",
		HTMLBody: "<p>Hi</p>",
		TextBody: "Hi",
		// MessageID, InReplyTo, References all empty
	}

	msg := buildThreadedMimeMessage("from@example.com", "", params)

	if strings.Contains(msg, "Message-ID:") {
		t.Error("Expected no Message-ID header when MessageID is empty")
	}
	if strings.Contains(msg, "In-Reply-To:") {
		t.Error("Expected no In-Reply-To header when InReplyTo is empty")
	}
	if strings.Contains(msg, "References:") {
		t.Error("Expected no References header when References is empty")
	}
}

func TestBuildThreadedMimeMessage_MultipartStructure(t *testing.T) {
	params := ThreadedEmailParams{
		ToEmail:  "to@example.com",
		Subject:  "Test",
		HTMLBody: "<p>HTML content</p>",
		TextBody: "Plain text content",
	}

	msg := buildThreadedMimeMessage("from@example.com", "Sender", params)

	if !strings.Contains(msg, "Content-Type: multipart/alternative") {
		t.Error("Expected multipart/alternative content type")
	}
	if !strings.Contains(msg, "Content-Type: text/plain; charset=UTF-8") {
		t.Error("Expected text/plain part")
	}
	if !strings.Contains(msg, "Content-Type: text/html; charset=UTF-8") {
		t.Error("Expected text/html part")
	}
	if !strings.Contains(msg, "Plain text content") {
		t.Error("Expected text body content in message")
	}
	if !strings.Contains(msg, "<p>HTML content</p>") {
		t.Error("Expected HTML body content in message")
	}
}

func TestBuildThreadedMimeMessage_FromName(t *testing.T) {
	params := ThreadedEmailParams{
		ToEmail:  "to@example.com",
		ToName:   "Recipient Name",
		Subject:  "Test",
		HTMLBody: "<p>Hi</p>",
		TextBody: "Hi",
	}

	t.Run("WithFromName", func(t *testing.T) {
		msg := buildThreadedMimeMessage("from@example.com", "Sender Name", params)
		if !strings.Contains(msg, "From: Sender Name <from@example.com>") {
			t.Error("Expected From header with display name")
		}
	})

	t.Run("WithoutFromName", func(t *testing.T) {
		msg := buildThreadedMimeMessage("from@example.com", "", params)
		if !strings.Contains(msg, "From: from@example.com") {
			t.Error("Expected From header with email only")
		}
		if strings.Contains(msg, "From:  <from@example.com>") {
			t.Error("Should not have empty name in From header")
		}
	})

	t.Run("ToWithName", func(t *testing.T) {
		msg := buildThreadedMimeMessage("from@example.com", "", params)
		if !strings.Contains(msg, "To: Recipient Name <to@example.com>") {
			t.Error("Expected To header with display name")
		}
	})

	t.Run("ToWithoutName", func(t *testing.T) {
		paramsNoName := params
		paramsNoName.ToName = ""
		msg := buildThreadedMimeMessage("from@example.com", "", paramsNoName)
		if !strings.Contains(msg, "To: to@example.com") {
			t.Error("Expected To header with email only")
		}
	})
}

// fakeEncryptor satisfies Encryptor with a deterministic AES-GCM round-trip
// so we can verify decryptOrLegacy + dispatch's password handling without
// pulling in the real *sso.SecretEncryption.
type fakeEncryptor struct {
	key []byte
}

func newFakeEncryptor(t *testing.T) *fakeEncryptor {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return &fakeEncryptor{key: k}
}

func (f *fakeEncryptor) Encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(f.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

func (f *fakeEncryptor) Decrypt(ciphertext string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(f.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, body := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func TestDispatch_RejectsEmptyEncryption(t *testing.T) {
	// The default branch in the encryption switch used to silently fall
	// through to plaintext smtp.SendMail, leaking AUTH PLAIN credentials on
	// any deployment with a typo'd or unset SMTPEncryption value.
	s := &NotificationSMTPSender{}
	cfg := &models.ChannelConfig{
		SMTPHost:       "smtp.example.com",
		SMTPPort:       25,
		SMTPFromEmail:  "from@example.com",
		SMTPEncryption: "",
	}
	err := s.dispatch(cfg, "to@example.com", "BODY")
	if err == nil {
		t.Fatal("expected error for empty SMTPEncryption, got nil")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("expected encryption-not-allowed error, got %v", err)
	}
}

func TestDispatch_RejectsUnknownEncryption(t *testing.T) {
	s := &NotificationSMTPSender{}
	cfg := &models.ChannelConfig{
		SMTPHost:       "smtp.example.com",
		SMTPPort:       25,
		SMTPFromEmail:  "from@example.com",
		SMTPEncryption: "plain", // typo
	}
	err := s.dispatch(cfg, "to@example.com", "BODY")
	if err == nil {
		t.Fatal("expected error for unknown SMTPEncryption, got nil")
	}
	if !strings.Contains(err.Error(), `"plain"`) {
		t.Errorf("expected error to mention bad value, got %v", err)
	}
}

func TestDispatch_RejectsPlaintextAuthentication(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
	}{
		{name: "username", username: "relay-user"},
		{name: "password", password: "relay-password"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &NotificationSMTPSender{}
			cfg := &models.ChannelConfig{
				SMTPHost:       "smtp.example.com",
				SMTPPort:       25,
				SMTPUsername:   tt.username,
				SMTPPassword:   tt.password,
				SMTPFromEmail:  "from@example.com",
				SMTPEncryption: "none",
			}

			err := s.dispatch(cfg, "to@example.com", "BODY")
			if err == nil {
				t.Fatal("expected plaintext authentication to be rejected")
			}
			if got, want := err.Error(), "SMTP authentication requires TLS"; got != want {
				t.Fatalf("dispatch error = %q, want %q", got, want)
			}
		})
	}
}

func TestDispatch_RejectsLoopbackHost(t *testing.T) {
	utils.SetAllowLocalConnections(false)
	defer utils.SetAllowLocalConnections(true)

	for _, mode := range []string{"ssl", "none"} {
		t.Run(mode, func(t *testing.T) {
			// SMTPHost is admin-configurable through PUT /channels/{id}/config.
			// When local connections are explicitly disabled, every transport
			// must reject loopback before the TCP handshake.
			s := &NotificationSMTPSender{}
			cfg := &models.ChannelConfig{
				SMTPHost:       "127.0.0.1",
				SMTPPort:       1,
				SMTPFromEmail:  "from@example.com",
				SMTPEncryption: mode,
			}
			err := s.dispatch(cfg, "to@example.com", "BODY")
			if err == nil {
				t.Fatal("expected SSRF guard to reject loopback, got nil error")
			}
			if !errors.Is(err, utils.ErrBlockedSSRFAddr) && !strings.Contains(err.Error(), "blocked IP range") {
				t.Errorf("expected ErrBlockedSSRFAddr, got %v", err)
			}
		})
	}
}

func TestDecryptOrLegacy_PassthroughOnEmpty(t *testing.T) {
	// Empty value is a no-op even with an encryptor wired — keeps the
	// "no SMTP password configured" path silent.
	enc := newFakeEncryptor(t)
	got, err := decryptOrLegacy(enc, "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty passthrough, got %q", got)
	}
}

func TestDecryptOrLegacy_PassthroughOnLegacyPlaintext(t *testing.T) {
	// Pre-migration rows hold short plaintext passwords. The 28-byte/base64
	// heuristic must keep returning them verbatim instead of failing decrypt.
	enc := newFakeEncryptor(t)
	got, err := decryptOrLegacy(enc, "shortpass")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "shortpass" {
		t.Errorf("expected legacy plaintext passthrough, got %q", got)
	}
}

func TestDecryptOrLegacy_RoundTrip(t *testing.T) {
	enc := newFakeEncryptor(t)
	encrypted, err := enc.Encrypt("hunter2-with-some-padding-to-hit-min")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	plain, err := decryptOrLegacy(enc, encrypted)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if plain != "hunter2-with-some-padding-to-hit-min" {
		t.Errorf("round-trip mismatch: got %q", plain)
	}
}
