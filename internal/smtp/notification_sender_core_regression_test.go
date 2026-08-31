package smtp

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/utils"
)

func TestSMTPTLSConfigRequiresExplicitVerificationBypass(t *testing.T) {
	tests := []struct {
		name               string
		skipTLSVerify      bool
		wantInsecureVerify bool
	}{
		{name: "verification enabled by default", skipTLSVerify: false, wantInsecureVerify: false},
		{name: "verification bypass explicitly enabled", skipTLSVerify: true, wantInsecureVerify: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			utils.SetSkipTLSVerify(tt.skipTLSVerify)
			t.Cleanup(func() { utils.SetSkipTLSVerify(false) })
			cfg := smtpTLSConfig("smtp.internal.example:465", false)
			if cfg.InsecureSkipVerify != tt.wantInsecureVerify {
				t.Fatalf("InsecureSkipVerify = %v, want %v", cfg.InsecureSkipVerify, tt.wantInsecureVerify)
			}
			if cfg.ServerName != "smtp.internal.example" {
				t.Fatalf("ServerName = %q, want smtp.internal.example", cfg.ServerName)
			}
			if cfg.MinVersion != tls.VersionTLS12 {
				t.Fatalf("MinVersion = %d, want TLS 1.2", cfg.MinVersion)
			}
		})
	}
}

func TestSMTPTLSConfigAcceptsSelfSignedCertificateOnlyWhenEnabled(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.StartTLS()
	t.Cleanup(server.Close)

	utils.SetSkipTLSVerify(false)
	t.Cleanup(func() { utils.SetSkipTLSVerify(false) })
	verifiedConn, err := tls.Dial("tcp", server.Listener.Addr().String(), smtpTLSConfig(server.Listener.Addr().String(), false))
	if err == nil {
		_ = verifiedConn.Close()
		t.Fatal("self-signed certificate was accepted while verification was enabled")
	}

	utils.SetSkipTLSVerify(true)
	insecureConn, err := tls.Dial("tcp", server.Listener.Addr().String(), smtpTLSConfig(server.Listener.Addr().String(), false))
	if err != nil {
		t.Fatalf("self-signed certificate was rejected with verification bypass enabled: %v", err)
	}
	if err := insecureConn.Close(); err != nil {
		t.Fatalf("close TLS connection: %v", err)
	}
}

func TestSMTPTLSConfigAllowsChannelVerificationBypass(t *testing.T) {
	utils.SetSkipTLSVerify(false)
	t.Cleanup(func() { utils.SetSkipTLSVerify(false) })

	cfg := smtpTLSConfig("smtp.internal.example:465", true)
	if !cfg.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify = false, want true for SMTP channel opt-out")
	}
	if cfg.ServerName != "smtp.internal.example" {
		t.Fatalf("ServerName = %q, want smtp.internal.example", cfg.ServerName)
	}
}

func TestBuildMimeCanonicalizesThreadingMessageIDs(t *testing.T) {
	mime := buildMime(mimeOptions{
		FromEmail: "team@example.com", ToEmail: "customer@example.com",
		Subject: "Reply", TextBody: "text", HTMLBody: "<p>text</p>",
		MessageID: "outbound@example.com", InReplyTo: "legacy-inbound@example.com",
		References: []string{"first@example.com", "<second@example.com>"},
	})
	for _, header := range []string{
		"Message-ID: <outbound@example.com>\r\n",
		"In-Reply-To: <legacy-inbound@example.com>\r\n",
		"References: <first@example.com> <second@example.com>\r\n",
	} {
		if !strings.Contains(mime, header) {
			t.Fatalf("MIME message is missing %q:\n%s", header, mime)
		}
	}
}

func TestNormalizeEnvelopeAddressRejectsHeaderSyntax(t *testing.T) {
	if got, err := normalizeEnvelopeAddress("  sender@example.com  "); err != nil || got != "sender@example.com" {
		t.Fatalf("valid address = (%q, %v)", got, err)
	}
	for _, invalid := range []string{
		"Sender <sender@example.com>",
		"one@example.com, two@example.com",
		"sender@example.com\r\nRCPT TO:<victim@example.com>",
		"",
	} {
		if _, err := normalizeEnvelopeAddress(invalid); err == nil {
			t.Fatalf("invalid address %q unexpectedly accepted", invalid)
		}
	}
}

func TestEncryptionModeAllowedAcceptsSupportedModes(t *testing.T) {
	for _, mode := range []string{"tls", "starttls", "ssl", "none"} {
		if !EncryptionModeAllowed(mode) {
			t.Fatalf("supported mode %q was rejected", mode)
		}
	}
	if EncryptionModeAllowed("bogus") {
		t.Fatal("unknown SMTP mode was accepted")
	}
}

func TestGetSMTPConfigUsesExplicitDefault(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "smtp.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if _, err := db.ExecWrite("DELETE FROM channels WHERE type = 'smtp' AND direction = 'outbound'"); err != nil {
		t.Fatalf("clear smtp channels: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO channels (name, type, direction, status, is_default, config, updated_at)
		VALUES ('default', 'smtp', 'outbound', 'enabled', true,
		        '{"smtp_host":"default.example","smtp_port":587,"smtp_from_email":"default@example.com"}',
		        ?)
	`, time.Now().UTC().Add(-24*time.Hour)); err != nil {
		t.Fatalf("insert default SMTP channel: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO channels (name, type, direction, status, is_default, config, updated_at)
		VALUES ('newer non-default', 'smtp', 'outbound', 'enabled', false,
		        '{"smtp_host":"attacker.example","smtp_port":587,"smtp_from_email":"attacker@example.com"}',
		        CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("insert non-default SMTP channel: %v", err)
	}

	cfg, err := NewNotificationSMTPSender(db).getSMTPConfig()
	if err != nil {
		t.Fatalf("getSMTPConfig: %v", err)
	}
	if cfg.SMTPHost != "default.example" {
		t.Fatalf("SMTP host = %q, want explicit default", cfg.SMTPHost)
	}
}
