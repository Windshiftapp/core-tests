//go:build test

package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// createBrandedThemeRequest builds a POST /themes request carrying the brand
// layout and full-logo fields. Empty values are sent as-is so tests can cover
// the "omitted"/empty contract.
func createBrandedThemeRequest(t *testing.T, brandLayout, logoFullURL, logoFullURLDark string) *http.Request {
	t.Helper()
	payload := map[string]any{
		"name":                       "Acme",
		"description":                "Company theme",
		"nav_background_color_light": "#ffffff",
		"nav_text_color_light":       "#374151",
		"nav_background_color_dark":  "#1f2937",
		"nav_text_color_dark":        "#f3f4f6",
		"logo_url":                   "",
		"logo_url_dark":              "",
		"logo_full_url":              logoFullURL,
		"logo_full_url_dark":         logoFullURLDark,
	}
	if brandLayout != "" {
		payload["brand_layout"] = brandLayout
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return httptest.NewRequest(http.MethodPost, "/api/themes", bytes.NewReader(body))
}

func storedThemeColumn(t *testing.T, tdb *testutils.TestDB, themeID int, column string) string {
	t.Helper()
	var stored string
	if err := tdb.Database.QueryRow(`SELECT COALESCE(`+column+`, '') FROM themes WHERE id = ?`, themeID).Scan(&stored); err != nil {
		t.Fatalf("read stored %s: %v", column, err)
	}
	return stored
}

func TestCreateTheme_PersistsBrandFields(t *testing.T) {
	handler, tdb := newThemeLogoHandler(t)

	rec := httptest.NewRecorder()
	handler.CreateTheme(rec, createBrandedThemeRequest(
		t, models.BrandLayoutFullLogo, "/api/portal-assets/11", "/api/portal-assets/12",
	))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	var created models.Theme
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created theme: %v", err)
	}
	if created.BrandLayout != models.BrandLayoutFullLogo {
		t.Fatalf("created brand_layout = %q, want %q", created.BrandLayout, models.BrandLayoutFullLogo)
	}
	if created.LogoFullURL != "/api/portal-assets/11" || created.LogoFullURLDark != "/api/portal-assets/12" {
		t.Fatalf("created full logos = %q / %q", created.LogoFullURL, created.LogoFullURLDark)
	}
	for column, want := range map[string]string{
		"brand_layout":        models.BrandLayoutFullLogo,
		"logo_full_url":       "/api/portal-assets/11",
		"logo_full_url_dark":  "/api/portal-assets/12",
	} {
		if stored := storedThemeColumn(t, tdb, created.ID, column); stored != want {
			t.Fatalf("stored %s = %q, want %q", column, stored, want)
		}
	}
}

func TestCreateTheme_DefaultsBrandLayoutToMarkText(t *testing.T) {
	handler, tdb := newThemeLogoHandler(t)

	rec := httptest.NewRecorder()
	handler.CreateTheme(rec, createBrandedThemeRequest(t, "", "", ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	var created models.Theme
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created theme: %v", err)
	}
	if created.BrandLayout != models.BrandLayoutMarkText {
		t.Fatalf("created brand_layout = %q, want %q", created.BrandLayout, models.BrandLayoutMarkText)
	}
	if stored := storedThemeColumn(t, tdb, created.ID, "brand_layout"); stored != models.BrandLayoutMarkText {
		t.Fatalf("stored brand_layout = %q, want %q", stored, models.BrandLayoutMarkText)
	}
}

func TestCreateTheme_RejectsInvalidBrandLayout(t *testing.T) {
	handler, _ := newThemeLogoHandler(t)

	rec := httptest.NewRecorder()
	handler.CreateTheme(rec, createBrandedThemeRequest(t, "sideways", "", ""))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Brand layout") {
		t.Fatalf("rejection body %q does not name the brand layout field", rec.Body.String())
	}
}

func TestCreateTheme_RejectsInvalidFullLogoURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{name: "javascript scheme", url: "javascript:alert(1)"},
		{name: "protocol-relative", url: "//evil.example.com/logo.png"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, _ := newThemeLogoHandler(t)

			rec := httptest.NewRecorder()
			handler.CreateTheme(rec, createBrandedThemeRequest(t, models.BrandLayoutFullLogo, tc.url, ""))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "Full logo URL") {
				t.Fatalf("rejection body %q does not name the full logo URL field", rec.Body.String())
			}
		})
	}
}
