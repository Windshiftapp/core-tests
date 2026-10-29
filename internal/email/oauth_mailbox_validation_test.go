//go:build test

package email_test

import (
	"errors"
	"testing"

	"windshift/internal/email"
	"windshift/internal/models"
)

// TestValidateConfigForEnableRejectsMailboxMismatch pins the invariant that a
// connection verified for one mailbox cannot be enabled for another: changing
// the typed mailbox must force a reconnect rather than silently polling the old
// address.
func TestValidateConfigForEnableRejectsMailboxMismatch(t *testing.T) {
	channel := &models.Channel{Type: "email", Direction: "inbound"}
	base := func() *models.ChannelConfig {
		return &models.ChannelConfig{
			EmailAuthMethod:        "oauth",
			EmailOAuthProviderType: models.EmailProviderTypeMicrosoft,
			EmailOAuthClientID:     "client",
			EmailOAuthClientSecret: "encrypted",
			EmailOAuthRefreshToken: "refresh",
			EmailOAuthMailbox:      "support@example.com",
			EmailOAuthEmail:        "support@example.com",
		}
	}

	if err := email.ValidateConfigForEnable(channel, base()); err != nil {
		t.Fatalf("matching mailbox rejected: %v", err)
	}

	mismatch := base()
	mismatch.EmailOAuthMailbox = "other@example.com"
	if err := email.ValidateConfigForEnable(channel, mismatch); !errors.Is(err, email.ErrConfigNotReady) {
		t.Fatalf("mismatched mailbox err = %v, want ErrConfigNotReady", err)
	}

	// Channels connected before email_oauth_mailbox existed have no typed
	// address; their verified address must keep enabling.
	legacy := base()
	legacy.EmailOAuthMailbox = ""
	if err := email.ValidateConfigForEnable(channel, legacy); err != nil {
		t.Fatalf("legacy config without a typed mailbox rejected: %v", err)
	}
}
