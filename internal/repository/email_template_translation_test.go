//go:build test

package repository

import (
	"errors"
	"testing"

	"windshift/internal/testutils"
)

func newEmailTemplateRepo(t *testing.T) *EmailTemplateRepository {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	return NewEmailTemplateRepository(tdb.GetDatabase())
}

// TestEmailTemplateTranslationResolution covers the resolution order:
// exact-locale override, then a customized base row, then the embedded default.
func TestEmailTemplateTranslationResolution(t *testing.T) {
	repo := newEmailTemplateRepo(t)

	if err := repo.SeedDefault("magic_link", "desc", "Sign in", `<p>Sign in</p>`, "Sign in"); err != nil {
		t.Fatalf("SeedDefault: %v", err)
	}
	base, err := repo.GetByName("magic_link")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}

	// No override and an unmodified seed: the embedded default applies.
	tmpl, fromDB, err := repo.ResolveForLocale("magic_link", "de")
	if err != nil {
		t.Fatalf("ResolveForLocale (no override): %v", err)
	}
	if tmpl != nil || fromDB {
		t.Fatalf("ResolveForLocale returned (%v, %v), want embedded default", tmpl, fromDB)
	}

	// Adding a locale override wins.
	if _, err := repo.UpsertTranslation(base.ID, "de", "DE subject", "<p>DE body</p>", "DE text"); err != nil {
		t.Fatalf("UpsertTranslation: %v", err)
	}
	tmpl, fromDB, err = repo.ResolveForLocale("magic_link", "de")
	if err != nil || !fromDB || tmpl == nil {
		t.Fatalf("ResolveForLocale (de) = (%v, %v, %v)", tmpl, fromDB, err)
	}
	if tmpl.Subject != "DE subject" || tmpl.HTMLBody != "<p>DE body</p>" || tmpl.TextBody != "DE text" {
		t.Fatalf("override not applied: %+v", tmpl)
	}

	// Region tags normalize to the shipped locale.
	if tmpl, _, _ := repo.ResolveForLocale("magic_link", "de-AT"); tmpl == nil || tmpl.Subject != "DE subject" {
		t.Fatalf("de-AT did not resolve to the de override: %+v", tmpl)
	}

	// English still has no override and the base is unmodified.
	if tmpl, fromDB, _ := repo.ResolveForLocale("magic_link", "en"); tmpl != nil || fromDB {
		t.Fatalf("en resolved to a DB row, want embedded default")
	}

	// A customized base row is the fallback for locales without an override.
	if _, err := repo.Update(base.ID, "Custom base", "<p>base</p>", "base", "desc", true); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if tmpl, fromDB, _ := repo.ResolveForLocale("magic_link", "en"); !fromDB || tmpl == nil || tmpl.Subject != "Custom base" {
		t.Fatalf("en did not fall back to the customized base: %+v", tmpl)
	}
	if tmpl, _, _ := repo.ResolveForLocale("magic_link", "de"); tmpl == nil || tmpl.Subject != "DE subject" {
		t.Fatalf("de override lost after base update: %+v", tmpl)
	}

	// Removing the override reverts to the customized base.
	if err := repo.DeleteTranslation(base.ID, "de"); err != nil {
		t.Fatalf("DeleteTranslation: %v", err)
	}
	if tmpl, fromDB, _ := repo.ResolveForLocale("magic_link", "de"); !fromDB || tmpl == nil || tmpl.Subject != "Custom base" {
		t.Fatalf("de did not revert to the base row: %+v", tmpl)
	}
	if err := repo.DeleteTranslation(base.ID, "de"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteTranslation(missing) = %v, want ErrNotFound", err)
	}
}

// TestEmailTemplateListIncludesTranslations ensures the admin list carries the
// per-locale overrides so the editor can pre-fill them.
func TestEmailTemplateListIncludesTranslations(t *testing.T) {
	repo := newEmailTemplateRepo(t)
	if err := repo.SeedDefault("magic_link", "desc", "Sign in", "<p>Sign in</p>", "Sign in"); err != nil {
		t.Fatalf("SeedDefault: %v", err)
	}
	base, err := repo.GetByName("magic_link")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if _, err := repo.UpsertTranslation(base.ID, "de", "DE", "<p>DE</p>", "DE"); err != nil {
		t.Fatalf("UpsertTranslation: %v", err)
	}
	if _, err := repo.UpsertTranslation(base.ID, "fr", "FR", "<p>FR</p>", "FR"); err != nil {
		t.Fatalf("UpsertTranslation: %v", err)
	}

	list, err := repo.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, tmpl := range list {
		if tmpl.Name != "magic_link" {
			continue
		}
		if len(tmpl.Translations) != 2 {
			t.Fatalf("translations = %d, want 2", len(tmpl.Translations))
		}
		if tmpl.Translations[0].Locale != "de" || tmpl.Translations[1].Locale != "fr" {
			t.Fatalf("translations not ordered by locale: %+v", tmpl.Translations)
		}
		return
	}
	t.Fatal("magic_link template not found in list")
}
