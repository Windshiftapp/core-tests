//go:build test

package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// channelPrepareHarness wires a real database, permission cache, channel
// service, and the config-update service under test. grantAdmin controls
// whether the seeded user is a system administrator; the permission service
// must be constructed after granting so its cache observes the role.
type channelPrepareHarness struct {
	tdb      *testutils.TestDB
	data     testutils.TestDataSet
	service  *ChannelConfigUpdateService
	channels *ChannelService
	ctx      context.Context
}

func newChannelPrepareHarness(t *testing.T, grantAdmin bool) *channelPrepareHarness {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	data := tdb.SeedTestData(t)
	if grantAdmin {
		if _, err := tdb.ExecWrite(`
			INSERT INTO user_global_permissions (user_id, permission_id)
			SELECT ?, id FROM permissions WHERE permission_key = 'system.admin'
		`, data.UserID); err != nil {
			t.Fatalf("grant system administrator: %v", err)
		}
	}
	permissions, err := NewPermissionService(tdb.GetDatabase(), DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	channels := NewChannelService(tdb.GetDatabase(), permissions)
	service := NewChannelConfigUpdateService(channels, permissions)
	// The tests exercise the policy wiring, not the email package itself, so a
	// minimal validator stub stands in for email.ValidateConfigForEnable.
	service.SetEmailConfigValidator(func(channel *models.Channel, config *models.ChannelConfig) error {
		if channel.Type != "email" || channel.Direction != "inbound" {
			return nil
		}
		if config.EmailWorkspaceID == 0 {
			return fmt.Errorf("email_workspace_id is required")
		}
		return nil
	})
	return &channelPrepareHarness{
		tdb:      tdb,
		data:     data,
		service:  service,
		channels: channels,
		ctx:      context.Background(),
	}
}

func (h *channelPrepareHarness) createChannel(t *testing.T, channelType, direction string) *models.Channel {
	t.Helper()
	channel, err := h.channels.Create(h.ctx, ChannelCreateRequest{
		Name:      fmt.Sprintf("%s-%s", channelType, direction),
		Type:      channelType,
		Direction: direction,
	})
	if err != nil {
		t.Fatalf("create %s channel: %v", channelType, err)
	}
	return channel
}

func (h *channelPrepareHarness) setConfig(t *testing.T, channelID int, config string) {
	t.Helper()
	if err := h.channels.UpdateConfig(h.ctx, channelID, config); err != nil {
		t.Fatalf("update config of channel %d: %v", channelID, err)
	}
}

func (h *channelPrepareHarness) configError(t *testing.T, err error) *ChannelConfigError {
	t.Helper()
	var configErr *ChannelConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("error = %v, want *ChannelConfigError", err)
	}
	return configErr
}

func TestPrepareEnable_DisablingChannelNeedsNoValidation(t *testing.T) {
	h := newChannelPrepareHarness(t, false)
	channel := h.createChannel(t, "smtp", "outbound")
	if err := h.channels.SetStatus(h.ctx, channel.ID, "enabled"); err != nil {
		t.Fatalf("enable channel: %v", err)
	}

	validated, err := h.service.PrepareEnable(h.ctx, h.data.UserID, channel.ID)
	if err != nil {
		t.Fatalf("PrepareEnable(disabling) error = %v, want nil", err)
	}
	if validated != "" {
		t.Fatalf("validated config = %q, want empty for a disable transition", validated)
	}
}

func TestPrepareEnable_MissingChannelReturnsNotFound(t *testing.T) {
	h := newChannelPrepareHarness(t, false)
	if _, err := h.service.PrepareEnable(h.ctx, h.data.UserID, 99999); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("PrepareEnable(missing) error = %v, want repository.ErrNotFound", err)
	}
}

func TestPrepareEnable_EmailValidationFailureIsDomainError(t *testing.T) {
	h := newChannelPrepareHarness(t, false)
	channel := h.createChannel(t, "email", "inbound")

	_, err := h.service.PrepareEnable(h.ctx, h.data.UserID, channel.ID)
	configErr := h.configError(t, err)
	if configErr.Kind != ChannelConfigInvalid {
		t.Fatalf("error kind = %v, want ChannelConfigInvalid", configErr.Kind)
	}
	if configErr.Message == "" {
		t.Fatal("error message is empty, want the validator detail")
	}
}

func TestPrepareEnable_PortalReturnsStoredConfigForCAS(t *testing.T) {
	h := newChannelPrepareHarness(t, true)
	channel := h.createChannel(t, "portal", "inbound")
	config := fmt.Sprintf(`{"portal_slug":"alpha","portal_workspace_ids":[%d]}`, h.data.WorkspaceID)
	h.setConfig(t, channel.ID, config)

	validated, err := h.service.PrepareEnable(h.ctx, h.data.UserID, channel.ID)
	if err != nil {
		t.Fatalf("PrepareEnable(valid portal) error = %v, want nil", err)
	}
	if validated != config {
		t.Fatalf("validated config = %q, want the stored config %q for the CAS status write", validated, config)
	}
}

func TestPrepareEnable_PortalSlugConflictIsConflict(t *testing.T) {
	h := newChannelPrepareHarness(t, true)
	first := h.createChannel(t, "portal", "inbound")
	second := h.createChannel(t, "portal", "inbound")
	config := fmt.Sprintf(`{"portal_slug":"duplicate","portal_workspace_ids":[%d]}`, h.data.WorkspaceID)
	h.setConfig(t, first.ID, config)
	// The repository guards slug uniqueness on save, so a conflicting stored
	// config can only exist from a legacy database or a concurrent claim.
	// Write it directly to reproduce that state for the enable-time check.
	if _, err := h.tdb.Exec("UPDATE channels SET config = ? WHERE id = ?", config, second.ID); err != nil {
		t.Fatalf("force conflicting config on channel %d: %v", second.ID, err)
	}

	_, err := h.service.PrepareEnable(h.ctx, h.data.UserID, second.ID)
	configErr := h.configError(t, err)
	if configErr.Kind != ChannelConfigConflict {
		t.Fatalf("error kind = %v, want ChannelConfigConflict", configErr.Kind)
	}
}

func TestPrepareEnable_WebhookAutoTriggerRequiresAdmin(t *testing.T) {
	h := newChannelPrepareHarness(t, false)
	channel := h.createChannel(t, "webhook", "outbound")
	h.setConfig(t, channel.ID, `{"webhook_auto_trigger":true,"webhook_url":"https://example.com/hook"}`)

	_, err := h.service.PrepareEnable(h.ctx, h.data.UserID, channel.ID)
	configErr := h.configError(t, err)
	if configErr.Kind != ChannelConfigForbidden {
		t.Fatalf("error kind = %v, want ChannelConfigForbidden", configErr.Kind)
	}
}

func TestPrepareEnable_SMTPAcceptsWhitespaceAroundFromAddress(t *testing.T) {
	h := newChannelPrepareHarness(t, false)
	channel := h.createChannel(t, "smtp", "outbound")
	config := `{"smtp_host":"mail.example.com","smtp_port":587,"smtp_from_email":" sender@example.com ","smtp_encryption":"starttls"}`
	h.setConfig(t, channel.ID, config)

	validated, err := h.service.PrepareEnable(h.ctx, h.data.UserID, channel.ID)
	if err != nil {
		t.Fatalf("PrepareEnable(valid SMTP config) error = %v, want nil", err)
	}
	if validated != config {
		t.Fatalf("validated config = %q, want the stored config %q for the CAS status write", validated, config)
	}
}

func TestPrepareEnable_SMTPAcceptsPlaintextWithoutAuthentication(t *testing.T) {
	h := newChannelPrepareHarness(t, false)
	channel := h.createChannel(t, "smtp", "outbound")
	config := `{"smtp_host":"mail.example.com","smtp_port":25,"smtp_from_email":"sender@example.com","smtp_encryption":"none"}`
	h.setConfig(t, channel.ID, config)

	validated, err := h.service.PrepareEnable(h.ctx, h.data.UserID, channel.ID)
	if err != nil {
		t.Fatalf("PrepareEnable(plaintext SMTP config) error = %v, want nil", err)
	}
	if validated != config {
		t.Fatalf("validated config = %q, want %q", validated, config)
	}
}

func TestPrepareEnable_SMTPRejectsPlaintextAuthentication(t *testing.T) {
	tests := []struct {
		name       string
		credential string
	}{
		{name: "username", credential: `,"smtp_username":"relay-user"`},
		{name: "password", credential: `,"smtp_password":"relay-password"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newChannelPrepareHarness(t, false)
			channel := h.createChannel(t, "smtp", "outbound")
			config := `{"smtp_host":"mail.example.com","smtp_port":25,"smtp_from_email":"sender@example.com","smtp_encryption":"none"` + tt.credential + `}`
			h.setConfig(t, channel.ID, config)

			_, err := h.service.PrepareEnable(h.ctx, h.data.UserID, channel.ID)
			configErr := h.configError(t, err)
			if got, want := configErr.Message, "SMTP authentication requires TLS"; got != want {
				t.Fatalf("error = %q, want %q", got, want)
			}
		})
	}
}

func TestUpdate_PreservesStoredEncryptedSecretsOnPartialUpdate(t *testing.T) {
	h := newChannelPrepareHarness(t, true)
	channel := h.createChannel(t, "smtp", "outbound")
	initialConfig := `{"smtp_host":"mail.example.com","smtp_password":"enc:v1:existing-ciphertext"}`
	h.setConfig(t, channel.ID, initialConfig)

	var encryptedValues []string
	h.service.SetSecretEncryptor(func(secret string) (string, error) {
		encryptedValues = append(encryptedValues, secret)
		return "enc:v1:" + secret, nil
	})

	updated, err := h.service.Update(h.ctx, h.data.UserID, channel.ID, map[string]interface{}{
		"smtp_host": "mail-updated.example.com",
	})
	if err != nil {
		t.Fatalf("Update(partial config) error = %v, want nil", err)
	}
	if !updated {
		t.Fatal("Update(partial config) = false, want true")
	}
	if len(encryptedValues) != 0 {
		t.Fatalf("secret encryptor called with %v, want no calls for omitted secrets", encryptedValues)
	}

	storedJSON, err := h.channels.GetConfig(h.ctx, channel.ID)
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	var stored map[string]interface{}
	if err := json.Unmarshal([]byte(storedJSON), &stored); err != nil {
		t.Fatalf("decode stored config: %v", err)
	}
	if stored["smtp_host"] != "mail-updated.example.com" {
		t.Fatalf("smtp_host = %#v, want updated host", stored["smtp_host"])
	}
	if stored["smtp_password"] != "enc:v1:existing-ciphertext" {
		t.Fatalf("smtp_password = %#v, want original ciphertext preserved", stored["smtp_password"])
	}
}
