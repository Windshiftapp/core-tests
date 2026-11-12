//go:build test

package smtp

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/emailutil"
	"windshift/internal/i18n"
)

// TestBuiltInTemplateRendersRecipientLocale pins the end-to-end localization:
// the same template renders German copy, sets lang/dir on the shell, and keeps
// the localized expiry wording.
func TestBuiltInTemplateRendersRecipientLocale(t *testing.T) {
	sender := &NotificationSMTPSender{}
	data := struct {
		FirstName    string
		MagicLinkURL string
		ExpiresIn    string
	}{
		FirstName:    "Alex",
		MagicLinkURL: "https://app.example.com/portal/acme/verify#token=t",
		ExpiresIn:    i18n.HumanizeDuration("de", 30*time.Minute),
	}

	subject, htmlBody, textBody, err := sender.RenderEmailLocale("de", emailutil.TemplateMagicLink, data)
	if err != nil {
		t.Fatalf("RenderEmailLocale: %v", err)
	}
	if want := "Bei Ihrem Portal anmelden"; subject != want {
		t.Fatalf("subject = %q, want %q", subject, want)
	}
	if !strings.Contains(htmlBody, `lang="de"`) || !strings.Contains(htmlBody, `dir="ltr"`) {
		t.Fatalf("HTML shell missing German lang/dir:\n%s", htmlBody)
	}
	if !strings.Contains(textBody, "Der Link ist 30 Minuten gültig.") {
		t.Fatalf("plain-text body is not German with a localized expiry: %q", textBody)
	}
	if strings.Contains(htmlBody, "Sign in to your portal") {
		t.Fatalf("German email still contains English copy:\n%s", htmlBody)
	}
}

func TestArabicTemplateRendersRightToLeft(t *testing.T) {
	sender := &NotificationSMTPSender{}
	_, htmlBody, _, err := sender.RenderEmailLocale(
		"ar",
		emailutil.TemplateMagicLink,
		emailutil.SampleData(emailutil.TemplateMagicLink),
	)
	if err != nil {
		t.Fatalf("RenderEmailLocale: %v", err)
	}
	if !strings.Contains(htmlBody, `lang="ar"`) || !strings.Contains(htmlBody, `dir="rtl"`) {
		t.Fatalf("Arabic HTML shell missing lang=\"ar\" dir=\"rtl\":\n%s", htmlBody)
	}
}

// TestUncustomizedSeedUsesLocalizedDefault covers the production upgrade path:
// rows seeded from the shipped defaults must not shadow the localized default
// until an admin edits them.
func TestUncustomizedSeedUsesLocalizedDefault(t *testing.T) {
	db := newSMTPTestDB(t)
	if err := emailutil.SeedTemplates(db); err != nil {
		t.Fatalf("SeedTemplates: %v", err)
	}

	sender := NewNotificationSMTPSender(db)
	subject, _, _, err := sender.RenderEmailLocale(
		"de",
		emailutil.TemplateMagicLink,
		emailutil.SampleData(emailutil.TemplateMagicLink),
	)
	if err != nil {
		t.Fatalf("RenderEmailLocale: %v", err)
	}
	if want := "Bei Ihrem Portal anmelden"; subject != want {
		t.Fatalf("subject = %q, want the localized default %q", subject, want)
	}
}

// TestCustomizedTemplateWinsOverLocalizedDefault proves an admin edit still
// overrides localization once is_customized is set.
func TestCustomizedTemplateWinsOverLocalizedDefault(t *testing.T) {
	db := newSMTPTestDB(t)
	if err := emailutil.SeedTemplates(db); err != nil {
		t.Fatalf("SeedTemplates: %v", err)
	}
	if _, err := db.ExecWrite(`
		UPDATE notification_templates
		SET subject = 'Custom subject', content = '<p>Custom body</p>', text_body = 'Custom body', is_customized = true
		WHERE name = 'magic_link'
	`); err != nil {
		t.Fatalf("customize template: %v", err)
	}

	sender := NewNotificationSMTPSender(db)
	subject, htmlBody, textBody, err := sender.RenderEmailLocale(
		"de",
		emailutil.TemplateMagicLink,
		emailutil.SampleData(emailutil.TemplateMagicLink),
	)
	if err != nil {
		t.Fatalf("RenderEmailLocale: %v", err)
	}
	if subject != "Custom subject" {
		t.Fatalf("subject = %q, want the admin-edited subject", subject)
	}
	if !strings.Contains(htmlBody, "Custom body") || !strings.Contains(textBody, "Custom body") {
		t.Fatalf("admin edit was not used: html=%q text=%q", htmlBody, textBody)
	}
}

func newSMTPTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "smtp-i18n.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return db
}
