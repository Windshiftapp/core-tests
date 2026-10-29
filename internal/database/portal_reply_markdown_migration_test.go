//go:build test

package database

import (
	"fmt"
	"strings"
	"testing"
)

// TestMigrationPortalReplyMarkdownRendersHTML verifies the migration that
// switches the portal reply email from literal Markdown to rendered HTML and
// injects the matching styles, while leaving admin edits and unrelated
// templates intact.
func TestMigrationPortalReplyMarkdownRendersHTML(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/portal-reply-md.db?mode=memory&cache=shared", t.TempDir())
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

	oldPortalReply := `<head><title>{{.Subject}}</title></head><body>CUSTOM BODY <div style="white-space:pre-wrap;">{{.Content}}</div></body>`
	if _, err := db.Exec(
		`INSERT INTO notification_templates (name, content, text_body) VALUES ('portal_reply', ?, '{{.Content}}')`,
		oldPortalReply,
	); err != nil {
		t.Fatalf("seed portal_reply: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO notification_templates (name, content) VALUES ('magic_link', '<head><title>{{.Subject}}</title></head><body>hi</body>')`,
	); err != nil {
		t.Fatalf("seed magic_link: %v", err)
	}

	var migration *Migration
	for i := range Catalog {
		if Catalog[i].Version == "20261013_portal_reply_markdown" {
			migration = &Catalog[i]
			break
		}
	}
	if migration == nil {
		t.Fatal("portal reply markdown migration missing from catalog")
	}
	if err := applyMigration(db, driverSQLite, *migration); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	var portalContent, portalText string
	if err := db.QueryRow(`SELECT content, text_body FROM notification_templates WHERE name = 'portal_reply'`).
		Scan(&portalContent, &portalText); err != nil {
		t.Fatalf("read portal_reply: %v", err)
	}
	if !strings.Contains(portalContent, "{{markdown .Content}}") {
		t.Errorf("portal reply content does not render Markdown: %q", portalContent)
	}
	if strings.Contains(portalContent, "{{.Content}}") {
		t.Errorf("portal reply content still uses raw Markdown: %q", portalContent)
	}
	if strings.Contains(portalContent, "white-space:pre-wrap") {
		t.Errorf("portal reply content still pre-wraps HTML: %q", portalContent)
	}
	if !strings.Contains(portalContent, ".ws-md table") {
		t.Errorf("portal reply content is missing the Markdown styles: %q", portalContent)
	}
	if !strings.Contains(portalContent, "CUSTOM BODY") {
		t.Errorf("admin body edit was lost: %q", portalContent)
	}
	// The plain-text part keeps the raw Markdown.
	if portalText != "{{.Content}}" {
		t.Errorf("portal reply text_body changed: %q", portalText)
	}

	var magicContent string
	if err := db.QueryRow(`SELECT content FROM notification_templates WHERE name = 'magic_link'`).Scan(&magicContent); err != nil {
		t.Fatalf("read magic_link: %v", err)
	}
	if !strings.Contains(magicContent, ".ws-md table") {
		t.Errorf("magic_link did not receive the shared styles: %q", magicContent)
	}

	// Idempotent: the body already uses {{markdown .Content}} and the styles.
	if _, err := db.Exec(portalReplyMarkdownMigration); err != nil {
		t.Fatalf("re-run migration body: %v", err)
	}
	var again string
	if err := db.QueryRow(`SELECT content FROM notification_templates WHERE name = 'portal_reply'`).Scan(&again); err != nil {
		t.Fatalf("read portal_reply again: %v", err)
	}
	if again != portalContent {
		t.Errorf("re-run changed content: before=%q after=%q", portalContent, again)
	}
}
