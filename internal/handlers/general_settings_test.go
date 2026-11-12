//go:build test

package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// newGeneralSettingsHandler wires the general-settings handler against a
// production-schema test database. The auditor is nil because these tests
// assert HTTP contracts, not audit rows.
func newGeneralSettingsHandler(t *testing.T) (*GeneralSettingsHandler, *testutils.TestDB) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	handler := NewGeneralSettingsHandler(
		repository.NewSystemSettingRepository(tdb.Database),
		repository.NewThemeRepository(tdb.Database),
		nil,
	)
	return handler, tdb
}

func TestGetBranding_DefaultsToProductName(t *testing.T) {
	handler, _ := newGeneralSettingsHandler(t)

	rec := httptest.NewRecorder()
	handler.GetBranding(rec, httptest.NewRequest(http.MethodGet, "/api/branding", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var branding BrandingResponse
	if err := json.NewDecoder(rec.Body).Decode(&branding); err != nil {
		t.Fatalf("decode branding: %v", err)
	}
	if branding.SiteTitle != DefaultSiteTitle {
		t.Fatalf("site_title = %q, want %q", branding.SiteTitle, DefaultSiteTitle)
	}
	if branding.BrandLayout != models.BrandLayoutMarkText {
		t.Fatalf("brand_layout = %q, want %q", branding.BrandLayout, models.BrandLayoutMarkText)
	}
}

func TestUpdateSettings_PersistsSiteTitleAndFeedsBranding(t *testing.T) {
	handler, _ := newGeneralSettingsHandler(t)

	rec := httptest.NewRecorder()
	handler.UpdateSettings(rec, httptest.NewRequest(
		http.MethodPut, "/api/admin/general-settings",
		bytes.NewReader([]byte(`{"site_title":"Acme Tracker"}`)),
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var updated GeneralSettings
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if updated.SiteTitle != "Acme Tracker" {
		t.Fatalf("site_title = %q, want Acme Tracker", updated.SiteTitle)
	}

	brandingRec := httptest.NewRecorder()
	handler.GetBranding(brandingRec, httptest.NewRequest(http.MethodGet, "/api/branding", nil))
	var branding BrandingResponse
	if err := json.NewDecoder(brandingRec.Body).Decode(&branding); err != nil {
		t.Fatalf("decode branding: %v", err)
	}
	if branding.SiteTitle != "Acme Tracker" {
		t.Fatalf("branding site_title = %q, want Acme Tracker", branding.SiteTitle)
	}
}

func TestUpdateSettings_BlankTitleFallsBackToProductName(t *testing.T) {
	handler, _ := newGeneralSettingsHandler(t)

	rec := httptest.NewRecorder()
	handler.UpdateSettings(rec, httptest.NewRequest(
		http.MethodPut, "/api/admin/general-settings",
		bytes.NewReader([]byte(`{"site_title":"   "}`)),
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var updated GeneralSettings
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if updated.SiteTitle != DefaultSiteTitle {
		t.Fatalf("site_title = %q, want %q", updated.SiteTitle, DefaultSiteTitle)
	}
}

func TestGetBranding_ReflectsActiveThemeBranding(t *testing.T) {
	handler, tdb := newGeneralSettingsHandler(t)

	themeRepo := repository.NewThemeRepository(tdb.Database)
	active, err := themeRepo.GetActive()
	if err != nil {
		t.Fatalf("get active theme: %v", err)
	}
	if err := themeRepo.Update(active.ID, models.ThemeUpdateRequest{
		Name:                    active.Name,
		Description:             active.Description,
		NavBackgroundColorLight: active.NavBackgroundColorLight,
		NavTextColorLight:       active.NavTextColorLight,
		NavBackgroundColorDark:  active.NavBackgroundColorDark,
		NavTextColorDark:        active.NavTextColorDark,
		LogoURL:                 "/api/portal-assets/1",
		LogoURLDark:             "/api/portal-assets/2",
		LogoFullURL:             "/api/portal-assets/3",
		LogoFullURLDark:         "/api/portal-assets/4",
		BrandLayout:             models.BrandLayoutFullLogo,
		IsActive:                true,
	}, time.Now()); err != nil {
		t.Fatalf("update active theme: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.GetBranding(rec, httptest.NewRequest(http.MethodGet, "/api/branding", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var branding BrandingResponse
	if err := json.NewDecoder(rec.Body).Decode(&branding); err != nil {
		t.Fatalf("decode branding: %v", err)
	}
	if branding.BrandLayout != models.BrandLayoutFullLogo {
		t.Fatalf("brand_layout = %q, want %q", branding.BrandLayout, models.BrandLayoutFullLogo)
	}
	if branding.LogoURL != "/api/portal-assets/1" || branding.LogoURLDark != "/api/portal-assets/2" {
		t.Fatalf("mark logos = %q / %q", branding.LogoURL, branding.LogoURLDark)
	}
	if branding.LogoFullURL != "/api/portal-assets/3" || branding.LogoFullURLDark != "/api/portal-assets/4" {
		t.Fatalf("full logos = %q / %q", branding.LogoFullURL, branding.LogoFullURLDark)
	}
}
