//go:build test

package smtp

import (
	"strings"
	"testing"

	"windshift/internal/emailutil"
	"windshift/internal/repository"
)

// TestLocaleOverrideWinsOverBuiltInDefault covers the sender resolution order:
// a per-locale admin override wins for that locale, while other locales keep
// the localized built-in default.
func TestLocaleOverrideWinsOverBuiltInDefault(t *testing.T) {
	db := newSMTPTestDB(t)
	if err := emailutil.SeedTemplates(db); err != nil {
		t.Fatalf("SeedTemplates: %v", err)
	}

	repo := repository.NewEmailTemplateRepository(db)
	base, err := repo.GetByName(emailutil.TemplateMagicLink)
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if _, err := repo.UpsertTranslation(base.ID, "de", "DE Betreff", "<p>DE Inhalt</p>", "DE Inhalt"); err != nil {
		t.Fatalf("UpsertTranslation: %v", err)
	}

	sender := NewNotificationSMTPSender(db)
	subject, htmlBody, _, err := sender.RenderEmailLocale(
		"de", emailutil.TemplateMagicLink, emailutil.SampleData(emailutil.TemplateMagicLink),
	)
	if err != nil {
		t.Fatalf("RenderEmailLocale(de): %v", err)
	}
	if subject != "DE Betreff" {
		t.Fatalf("subject = %q, want the de override", subject)
	}
	if !strings.Contains(htmlBody, "DE Inhalt") {
		t.Fatalf("de override body not used:\n%s", htmlBody)
	}

	// English has no override and the seed is unmodified: built-in default.
	enSubject, _, _, err := sender.RenderEmailLocale(
		"en", emailutil.TemplateMagicLink, emailutil.SampleData(emailutil.TemplateMagicLink),
	)
	if err != nil {
		t.Fatalf("RenderEmailLocale(en): %v", err)
	}
	if enSubject != "Sign in to your portal" {
		t.Fatalf("en subject = %q, want the built-in default", enSubject)
	}
}

// TestCustomizedBaseWinsOverBuiltInDefaultForUntranslatedLocale proves an
// admin-edited base row is used for locales without their own override.
func TestCustomizedBaseWinsOverBuiltInDefaultForUntranslatedLocale(t *testing.T) {
	db := newSMTPTestDB(t)
	if err := emailutil.SeedTemplates(db); err != nil {
		t.Fatalf("SeedTemplates: %v", err)
	}

	repo := repository.NewEmailTemplateRepository(db)
	base, err := repo.GetByName(emailutil.TemplateMagicLink)
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if _, err := repo.Update(base.ID, "Custom subject", "<p>Custom body</p>", "Custom body", "desc", true); err != nil {
		t.Fatalf("Update: %v", err)
	}

	sender := NewNotificationSMTPSender(db)
	subject, htmlBody, _, err := sender.RenderEmailLocale(
		"fr", emailutil.TemplateMagicLink, emailutil.SampleData(emailutil.TemplateMagicLink),
	)
	if err != nil {
		t.Fatalf("RenderEmailLocale(fr): %v", err)
	}
	if subject != "Custom subject" || !strings.Contains(htmlBody, "Custom body") {
		t.Fatalf("customized base not used for fr: subject=%q html=%q", subject, htmlBody)
	}
}
