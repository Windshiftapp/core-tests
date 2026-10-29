//go:build test

package database

import (
	"fmt"
	"strings"
	"testing"
)

// TestMigrationTransactionalEmailCopyRewritesShippedCopy verifies the in-place
// rewrite of the shipped transactional email copy: wrong expiry claims and the
// contradictory do-not-reply footer are corrected, admin edits elsewhere
// survive, and unrelated templates are left alone.
func TestMigrationTransactionalEmailCopyRewritesShippedCopy(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/template-copy.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 2, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`CREATE TABLE schema_migrations (
		version TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE notification_templates (
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
	)`); err != nil {
		t.Fatalf("create notification_templates: %v", err)
	}

	// Shipped rows carrying the old copy, one admin-edited row whose custom
	// heading must survive, and an unrelated template that happens to contain
	// the same phrase (must not be touched).
	fixtures := []struct {
		name, content, text string
	}{
		{"magic_link", `<h1>CUSTOM HEADING</h1><p>The link is valid for 15 minutes.</p>`, `The link is valid for 15 minutes:`},
		{"approval_requested", `<p>The link is valid for 15 minutes.</p>`, `The link is valid for 15 minutes:`},
		{"email_verification", `<p>This link expires in 24 hours.</p>`, `This link expires in 24 hours.`},
		{"invitation", `<p>This invitation expires in 7 days.</p>`, `This invitation expires in 7 days.`},
		{"portal_reply", `<p>To continue the conversation, simply reply to this email.</p><div>This is an automated email — please do not reply.</div>`, `To continue the conversation, simply reply to this email.`},
		{"unrelated_template", `<p>The link is valid for 15 minutes.</p>`, `The link is valid for 15 minutes:`},
	}
	for _, f := range fixtures {
		if _, err := db.Exec(
			`INSERT INTO notification_templates (name, content, text_body) VALUES (?, ?, ?)`,
			f.name, f.content, f.text,
		); err != nil {
			t.Fatalf("seed %s: %v", f.name, err)
		}
	}

	var migration *Migration
	for i := range Catalog {
		if Catalog[i].Version == "20261012_transactional_email_copy" {
			migration = &Catalog[i]
			break
		}
	}
	if migration == nil {
		t.Fatal("transactional email copy migration missing from catalog")
	}
	if err := applyMigration(db, driverSQLite, *migration); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	read := func(name string) (content, text string) {
		t.Helper()
		if err := db.QueryRow(
			`SELECT content, text_body FROM notification_templates WHERE name = ?`, name,
		).Scan(&content, &text); err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return content, text
	}

	for _, name := range []string{"magic_link", "approval_requested"} {
		content, text := read(name)
		if !strings.Contains(content, "valid for {{.ExpiresIn}}.") {
			t.Errorf("%s content not rewritten: %q", name, content)
		}
		if !strings.Contains(text, "valid for {{.ExpiresIn}}:") {
			t.Errorf("%s text not rewritten: %q", name, text)
		}
		if strings.Contains(content, "15 minutes") || strings.Contains(text, "15 minutes") {
			t.Errorf("%s still claims 15 minutes", name)
		}
	}
	for _, name := range []string{"email_verification", "invitation"} {
		content, text := read(name)
		if !strings.Contains(content, "{{.ExpiresIn}}") || !strings.Contains(text, "{{.ExpiresIn}}") {
			t.Errorf("%s not rewritten: content=%q text=%q", name, content, text)
		}
	}

	portalContent, _ := read("portal_reply")
	if strings.Contains(portalContent, "please do not reply") {
		t.Errorf("portal reply footer still tells the customer not to reply: %q", portalContent)
	}
	if !strings.Contains(portalContent, "open request with us") {
		t.Errorf("portal reply footer not replaced: %q", portalContent)
	}
	if !strings.Contains(portalContent, "simply reply to this email") {
		t.Errorf("portal reply instruction was lost: %q", portalContent)
	}

	magicContent, _ := read("magic_link")
	if !strings.Contains(magicContent, "CUSTOM HEADING") {
		t.Errorf("admin heading was clobbered: %q", magicContent)
	}

	unrelatedContent, unrelatedText := read("unrelated_template")
	if unrelatedContent != `<p>The link is valid for 15 minutes.</p>` || unrelatedText != `The link is valid for 15 minutes:` {
		t.Errorf("unrelated template was modified: content=%q text=%q", unrelatedContent, unrelatedText)
	}

	// Re-running the body is a no-op: the phrases are already gone.
	if _, err := db.Exec(transactionalEmailCopyMigration); err != nil {
		t.Fatalf("re-run migration body: %v", err)
	}
	againContent, _ := read("magic_link")
	if againContent != magicContent {
		t.Errorf("re-run changed content: before=%q after=%q", magicContent, againContent)
	}
}
