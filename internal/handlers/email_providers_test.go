package handlers

import (
	"context"
	"path/filepath"
	"testing"

	"windshift/internal/database"
)

func newEmailProviderHandlerTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "providers.db"))
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

func TestScanEmailProviderAcceptsNullableModeSpecificColumns(t *testing.T) {
	db := newEmailProviderHandlerTestDB(t)
	if _, err := db.ExecWrite(`
		INSERT INTO email_providers
			(name, slug, type, is_enabled, imap_host, imap_port, imap_encryption)
		VALUES ('Generic', 'generic', 'generic', true, 'imap.example.com', 993, 'ssl')
	`); err != nil {
		t.Fatalf("insert provider: %v", err)
	}

	provider, err := scanEmailProvider(db.QueryRow(`
		SELECT id, name, slug, type, is_enabled,
		       oauth_client_id, oauth_scopes, oauth_tenant_id,
		       imap_host, imap_port, imap_encryption,
		       created_at, updated_at
		FROM email_providers WHERE slug = 'generic'
	`))
	if err != nil {
		t.Fatalf("scanEmailProvider: %v", err)
	}
	if provider.OAuthClientID != "" || provider.IMAPHost != "imap.example.com" || provider.IMAPPort != 993 {
		t.Fatalf("provider = %#v", provider)
	}
}

func TestChannelsUsingProviderReadsJSONBindings(t *testing.T) {
	db := newEmailProviderHandlerTestDB(t)
	if _, err := db.ExecWrite(`
		INSERT INTO channels(name, type, direction, config)
		VALUES
			('bound', 'email', 'inbound', '{"email_provider_id":7}'),
			('other', 'email', 'inbound', '{"email_provider_id":8}'),
			('portal', 'portal', 'inbound', '{"email_provider_id":7}')
	`); err != nil {
		t.Fatalf("insert channels: %v", err)
	}

	handler := &EmailProviderHandler{db: db}
	ids, err := handler.channelsUsingProvider(context.Background(), 7)
	if err != nil {
		t.Fatalf("channelsUsingProvider: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("bound channel IDs = %v, want one email channel", ids)
	}
}
