//go:build test

package email_test

import (
	"errors"
	"testing"

	"windshift/internal/email"
	"windshift/internal/models"
)

func TestResolveEmailDisposition(t *testing.T) {
	tests := []struct {
		name   string
		config *models.ChannelConfig
		want   string
	}{
		{name: "nil config leaves", config: nil, want: models.EmailDispositionLeave},
		{
			name:   "explicit enum wins over legacy booleans",
			config: &models.ChannelConfig{EmailProcessingDisposition: models.EmailDispositionLeave, EmailMarkAsRead: true},
			want:   models.EmailDispositionLeave,
		},
		{
			name:   "legacy delete",
			config: &models.ChannelConfig{EmailDeleteAfterProcess: true},
			want:   models.EmailDispositionDelete,
		},
		{
			name:   "legacy mark read",
			config: &models.ChannelConfig{EmailMarkAsRead: true},
			want:   models.EmailDispositionMarkRead,
		},
		{
			name:   "legacy both prefers delete",
			config: &models.ChannelConfig{EmailMarkAsRead: true, EmailDeleteAfterProcess: true},
			want:   models.EmailDispositionDelete,
		},
		{name: "legacy none leaves", config: &models.ChannelConfig{}, want: models.EmailDispositionLeave},
		{
			name:   "invalid enum falls back to legacy",
			config: &models.ChannelConfig{EmailProcessingDisposition: "purge", EmailMarkAsRead: true},
			want:   models.EmailDispositionMarkRead,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := email.ResolveEmailDisposition(tt.config); got != tt.want {
				t.Errorf("ResolveEmailDisposition() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateConfigForEnableRejectsUnknownDisposition(t *testing.T) {
	channel := &models.Channel{Type: "email", Direction: "inbound"}
	itemTypeID := 1
	base := func() *models.ChannelConfig {
		return &models.ChannelConfig{
			EmailWorkspaceID: 1,
			EmailItemTypeID:  &itemTypeID,
			EmailMailbox:     "INBOX",
			EmailAuthMethod:  "basic",
			IMAPHost:         "imap.example.com",
			IMAPUsername:     "support@example.com",
			IMAPPassword:     "secret",
			IMAPPort:         993,
			IMAPEncryption:   "ssl",
		}
	}

	if err := email.ValidateConfigForEnable(channel, base()); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	cfg := base()
	cfg.EmailProcessingDisposition = "purge"
	if err := email.ValidateConfigForEnable(channel, cfg); !errors.Is(err, email.ErrConfigNotReady) {
		t.Fatalf("err = %v, want ErrConfigNotReady", err)
	}
}
