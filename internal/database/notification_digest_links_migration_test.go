//go:build test

package database

import (
	"fmt"
	"strings"
	"testing"
)

// TestMigrationNotificationDigestLinksAddsActionURL verifies the migration that
// makes the digest template render the action URL the sender now provides,
// while leaving admin edits and unrelated templates intact.
func TestMigrationNotificationDigestLinksAddsActionURL(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/digest-links.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 2, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, statement := range []string{
		`CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			checksum TEXT NOT NULL,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE notification_templates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			subject TEXT,
			content TEXT,
			text_body TEXT,
			description TEXT,
			is_system BOOLEAN DEFAULT FALSE,
			is_active BOOLEAN DEFAULT TRUE,
			template_type TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("fixture schema: %v", err)
		}
	}

	oldContent := `<div style="font-weight:600;font-size:14px;color:#0f172a;margin-bottom:4px;">{{.Title}}</div>` + "\n" +
		`<div style="font-size:13px;color:#374151;line-height:1.5;">{{.Message}}</div>` + "\n" +
		`<div style="font-size:12px;color:#9ca3af;margin-top:8px;">{{.FormattedTime}}</div>` + "\n</td>"
	oldText := "{{range .Notifications}}* {{.Title}}\n  {{.Message}}\n  {{.FormattedTime}}\n\n{{end}}\n"
	if _, err := db.Exec(
		`INSERT INTO notification_templates (name, content, text_body) VALUES ('notification_batch', ?, ?)`,
		oldContent, oldText,
	); err != nil {
		t.Fatalf("seed notification_batch: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO notification_templates (name, content) VALUES ('unrelated', '<div>{{.Title}}</div>')`,
	); err != nil {
		t.Fatalf("seed unrelated: %v", err)
	}

	var migration *Migration
	for i := range Catalog {
		if Catalog[i].Version == "20261014_notification_digest_links" {
			migration = &Catalog[i]
			break
		}
	}
	if migration == nil {
		t.Fatal("notification digest links migration missing from catalog")
	}
	if err := applyMigration(db, driverSQLite, *migration); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	var content, textBody string
	if err := db.QueryRow(`SELECT content, text_body FROM notification_templates WHERE name = 'notification_batch'`).
		Scan(&content, &textBody); err != nil {
		t.Fatalf("read notification_batch: %v", err)
	}
	for _, want := range []string{"{{if .ActionURL}}", `href="{{.ActionURL}}"`, "View in Windshift"} {
		if !strings.Contains(content, want) {
			t.Errorf("digest content missing %q: %q", want, content)
		}
	}
	for _, want := range []string{"{{if .ActionURL}}", "View: {{.ActionURL}}"} {
		if !strings.Contains(textBody, want) {
			t.Errorf("digest text missing %q: %q", want, textBody)
		}
	}

	var unrelated string
	if err := db.QueryRow(`SELECT content FROM notification_templates WHERE name = 'unrelated'`).Scan(&unrelated); err != nil {
		t.Fatalf("read unrelated: %v", err)
	}
	if unrelated != "<div>{{.Title}}</div>" {
		t.Errorf("unrelated template was modified: %q", unrelated)
	}

	// Idempotent: the replacement phrases are gone, so a re-run is a no-op.
	if _, err := db.Exec(notificationDigestLinksMigration); err != nil {
		t.Fatalf("re-run migration body: %v", err)
	}
	var again string
	if err := db.QueryRow(`SELECT content FROM notification_templates WHERE name = 'notification_batch'`).Scan(&again); err != nil {
		t.Fatalf("read notification_batch again: %v", err)
	}
	if again != content {
		t.Errorf("re-run changed content: before=%q after=%q", content, again)
	}
}
