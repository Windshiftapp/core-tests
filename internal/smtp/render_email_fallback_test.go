//go:build test

package smtp

import (
	"path/filepath"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/emailutil"
)

// TestRenderEmailFallsBackToDefaultWhenAdminTemplateIsBroken covers the bug
// hunt finding that a broken admin template aborted the whole email family.
// Rendering must fall back to the embedded default instead of failing.
func TestRenderEmailFallsBackToDefaultWhenAdminTemplateIsBroken(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "smtp.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO notification_templates (name, subject, content, text_body, is_active)
		VALUES ('magic_link', 'Sign in', '<p>{{.FirstName</p>', 'body', true)
	`); err != nil {
		t.Fatalf("insert broken template: %v", err)
	}

	sender := NewNotificationSMTPSender(db)
	subject, htmlBody, textBody, err := sender.RenderEmail(
		emailutil.TemplateMagicLink,
		emailutil.SampleData(emailutil.TemplateMagicLink),
	)
	if err != nil {
		t.Fatalf("RenderEmail returned an error instead of falling back: %v", err)
	}
	if subject != "Sign in to your portal" {
		t.Fatalf("subject = %q, want the built-in default", subject)
	}
	if !strings.Contains(textBody, "30 minutes") || !strings.Contains(htmlBody, "30 minutes") {
		t.Fatalf("fallback did not use the built-in default content: html=%q text=%q", htmlBody, textBody)
	}
}
