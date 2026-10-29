//go:build test

package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

func TestPrepareEnable_SMTPAcceptsExplicitAuthMethod(t *testing.T) {
	for _, method := range []string{"auto", "plain", "login", "cram-md5", "none"} {
		t.Run(method, func(t *testing.T) {
			h := newChannelPrepareHarness(t, false)
			channel := h.createChannel(t, "smtp", "outbound")
			config := fmt.Sprintf(`{"smtp_host":"mail.example.com","smtp_port":587,"smtp_from_email":"sender@example.com","smtp_encryption":"starttls","smtp_auth_method":%q}`, method)
			h.setConfig(t, channel.ID, config)

			if _, err := h.service.PrepareEnable(h.ctx, h.data.UserID, channel.ID); err != nil {
				t.Fatalf("PrepareEnable(auth=%s) error = %v, want nil", method, err)
			}
		})
	}
}

func TestPrepareEnable_SMTPRejectsUnknownAuthMethod(t *testing.T) {
	h := newChannelPrepareHarness(t, false)
	channel := h.createChannel(t, "smtp", "outbound")
	config := `{"smtp_host":"mail.example.com","smtp_port":587,"smtp_from_email":"sender@example.com","smtp_encryption":"starttls","smtp_auth_method":"bogus"}`
	h.setConfig(t, channel.ID, config)

	_, err := h.service.PrepareEnable(h.ctx, h.data.UserID, channel.ID)
	configErr := h.configError(t, err)
	if !strings.Contains(configErr.Message, "not allowed") {
		t.Fatalf("error = %q, want an auth-method not-allowed message", configErr.Message)
	}
}

// A non-admin channel manager may enable a mailbox whose routing is entirely
// on intakes they manage, even when the stale legacy email_workspace_id points
// at a workspace they do not administer.
func TestPrepareEnable_IntakeTargetsAuthorizeNonAdmin(t *testing.T) {
	h := newChannelPrepareHarness(t, false)
	channel := h.createChannel(t, "email", "inbound")

	var itemTypeID int
	if err := h.tdb.QueryRow(`SELECT id FROM item_types LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("load item type: %v", err)
	}

	// Legacy routing points at a workspace the actor does not administer. The
	// validator stub only requires a non-zero workspace, mirroring production
	// where routing comes from the intake.
	config := `{"email_workspace_id":999,"email_auth_method":"basic","imap_host":"imap.example.com","imap_username":"support@example.com","imap_password":"x","imap_port":993,"imap_encryption":"ssl"}`
	h.setConfig(t, channel.ID, config)

	t.Run("managed intake target enables", func(t *testing.T) {
		if _, err := h.tdb.Exec(`
			INSERT INTO intakes (mailbox_id, folder, workspace_id, item_type_id, status)
			VALUES (?, 'INBOX', ?, ?, 'enabled')
		`, channel.ID, h.data.WorkspaceID, itemTypeID); err != nil {
			t.Fatalf("seed intake: %v", err)
		}
		t.Cleanup(func() { _, _ = h.tdb.Exec(`DELETE FROM intakes WHERE mailbox_id = ?`, channel.ID) })

		validated, err := h.service.PrepareEnable(h.ctx, h.data.UserID, channel.ID)
		if err != nil {
			t.Fatalf("PrepareEnable(managed intake) error = %v, want nil", err)
		}
		if validated != config {
			t.Fatalf("validated config = %q, want stored config", validated)
		}
	})

	t.Run("unmanaged intake target is forbidden", func(t *testing.T) {
		if _, err := h.tdb.Exec(`INSERT INTO workspaces (id, name, key, active) VALUES (2, 'Other', 'OTH', true)`); err != nil {
			t.Fatalf("seed workspace: %v", err)
		}
		if _, err := h.tdb.Exec(`
			INSERT INTO intakes (mailbox_id, folder, workspace_id, item_type_id, status)
			VALUES (?, 'INBOX', 2, ?, 'enabled')
		`, channel.ID, itemTypeID); err != nil {
			t.Fatalf("seed intake: %v", err)
		}
		t.Cleanup(func() { _, _ = h.tdb.Exec(`DELETE FROM intakes WHERE mailbox_id = ?`, channel.ID) })

		_, err := h.service.PrepareEnable(h.ctx, h.data.UserID, channel.ID)
		configErr := h.configError(t, err)
		if configErr.Kind != ChannelConfigForbidden {
			t.Fatalf("error kind = %v, want ChannelConfigForbidden", configErr.Kind)
		}
	})
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

// A saved credential must survive a config update that echoes the secret back
// blank, while a new value rotates it and JSON null is the explicit clear.
func TestUpdate_SecretFieldBlankKeepsRotatesAndClears(t *testing.T) {
	tests := []struct {
		name        string
		incoming    map[string]interface{}
		wantPresent bool
		wantValue   interface{}
		wantEncrypt []string
	}{
		{
			name:        "blank keeps stored ciphertext",
			incoming:    map[string]interface{}{"smtp_password": ""},
			wantPresent: true,
			wantValue:   "enc:v1:existing-ciphertext",
		},
		{
			name:        "new value rotates ciphertext",
			incoming:    map[string]interface{}{"smtp_password": "new-password"},
			wantPresent: true,
			wantValue:   "enc:v1:new-password",
			wantEncrypt: []string{"new-password"},
		},
		{
			name:        "null clears stored ciphertext",
			incoming:    map[string]interface{}{"smtp_password": nil},
			wantPresent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newChannelPrepareHarness(t, true)
			channel := h.createChannel(t, "smtp", "outbound")
			h.setConfig(t, channel.ID, `{"smtp_host":"mail.example.com","smtp_password":"enc:v1:existing-ciphertext"}`)

			var encryptedValues []string
			h.service.SetSecretEncryptor(func(secret string) (string, error) {
				encryptedValues = append(encryptedValues, secret)
				return "enc:v1:" + secret, nil
			})

			updated, err := h.service.Update(h.ctx, h.data.UserID, channel.ID, tt.incoming)
			if err != nil {
				t.Fatalf("Update() error = %v, want nil", err)
			}
			if !updated {
				t.Fatal("Update() = false, want true")
			}
			if fmt.Sprint(encryptedValues) != fmt.Sprint(tt.wantEncrypt) {
				t.Fatalf("encryptor calls = %v, want %v", encryptedValues, tt.wantEncrypt)
			}

			storedJSON, err := h.channels.GetConfig(h.ctx, channel.ID)
			if err != nil {
				t.Fatalf("GetConfig() error = %v", err)
			}
			var stored map[string]interface{}
			if err := json.Unmarshal([]byte(storedJSON), &stored); err != nil {
				t.Fatalf("decode stored config: %v", err)
			}
			got, present := stored["smtp_password"]
			if present != tt.wantPresent {
				t.Fatalf("smtp_password present = %v, want %v (config %s)", present, tt.wantPresent, storedJSON)
			}
			if tt.wantPresent && got != tt.wantValue {
				t.Fatalf("smtp_password = %#v, want %#v", got, tt.wantValue)
			}
		})
	}
}
