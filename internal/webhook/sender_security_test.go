package webhook

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"windshift/internal/database"
)

type fixedDecryptor struct {
	ciphertext string
	plaintext  string
}

func (d fixedDecryptor) Encrypt(string) (string, error) { return d.ciphertext, nil }
func (d fixedDecryptor) Decrypt(value string) (string, error) {
	if value != d.ciphertext {
		return "", errors.New("unexpected ciphertext")
	}
	return d.plaintext, nil
}

type failingWebhookPlugin struct{}

func (failingWebhookPlugin) DispatchToPlugin(context.Context, string, string, string, json.RawMessage) error {
	return errors.New("receiver unavailable")
}

func newWebhookSecurityTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "webhooks.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	if err := db.Initialize(); err != nil {
		_ = db.Close()
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertWebhookChannel(t *testing.T, db database.Database, config string) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('hook', 'webhook', 'outbound', 'enabled', ?) RETURNING id
	`, config).Scan(&id); err != nil {
		t.Fatalf("insert webhook channel: %v", err)
	}
	return id
}

func shutdownWebhookSender(t *testing.T, sender *WebhookSender) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := sender.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestSubscriptionIndexDecryptsPersistedSecret(t *testing.T) {
	db := newWebhookSecurityTestDB(t)
	ciphertext := base64.StdEncoding.EncodeToString(make([]byte, 32))
	insertWebhookChannel(t, db, `{"webhook_url":"https://example.com/hook","webhook_secret":"`+ciphertext+`","webhook_scope_type":"all","webhook_auto_trigger":true,"webhook_subscribed_events":["item.created"]}`)

	sender := NewWebhookSender(db, fixedDecryptor{ciphertext: ciphertext, plaintext: "shared-secret"})
	defer shutdownWebhookSender(t, sender)
	index, err := sender.subscriptionIndex(context.Background())
	if err != nil {
		t.Fatalf("subscriptionIndex: %v", err)
	}
	hooks := index.byEvent["item.created"]
	if len(hooks) != 1 || hooks[0].Secret != "shared-secret" {
		t.Fatalf("loaded hooks = %#v, want decrypted secret", hooks)
	}
}

func TestSubscriptionIndexKeepsPluginSpecificEvents(t *testing.T) {
	db := newWebhookSecurityTestDB(t)
	if _, err := db.ExecWrite(`
		INSERT INTO channels
			(name, type, direction, status, config, plugin_name, plugin_webhook_id)
		VALUES
			('approval hook', 'webhook', 'outbound', 'enabled',
			 '{"webhook_scope_type":"all","webhook_auto_trigger":true,"webhook_subscribed_events":["approval.completed"],"webhook_plugin_handler":"onApproval"}',
			 'approval-plugin', 'approval-hook')
	`); err != nil {
		t.Fatalf("insert plugin webhook: %v", err)
	}
	sender := NewWebhookSender(db)
	defer shutdownWebhookSender(t, sender)
	index, err := sender.subscriptionIndex(context.Background())
	if err != nil {
		t.Fatalf("subscriptionIndex: %v", err)
	}
	if hooks := index.byEvent["approval.completed"]; len(hooks) != 1 || hooks[0].PluginName != "approval-plugin" {
		t.Fatalf("plugin approval subscriptions = %#v", hooks)
	}
}

func TestSynchronousWebhookDeliveryReturnsPluginFailure(t *testing.T) {
	db := newWebhookSecurityTestDB(t)
	channelID := insertWebhookChannel(t, db, `{}`)
	sender := NewWebhookSender(db)
	defer shutdownWebhookSender(t, sender)
	sender.SetPluginDispatcher(failingWebhookPlugin{})

	err := sender.sendWebhookPayload(context.Background(), WebhookConfig{
		ChannelID:     channelID,
		PluginName:    "test-plugin",
		PluginHandler: "deliver",
	}, "manual", 42, json.RawMessage(`{"id":42}`))
	if err == nil {
		t.Fatal("plugin delivery failure was reported as success")
	}
}
