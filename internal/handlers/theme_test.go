//go:build test

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/models"
	"windshift/internal/objecttranslation"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func TestGetActiveThemeLocalizesResponse(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := NewThemeHandler(
		services.NewThemeService(repository.NewThemeRepository(tdb.Database)),
		nil,
	).WithObjectTranslations(objecttranslation.NewService(tdb.Database))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/themes/active", nil)

	handler.GetActiveTheme(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var theme models.Theme
	if err := json.NewDecoder(recorder.Body).Decode(&theme); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if theme.Name != "Default" || theme.DisplayName != "Default" {
		t.Fatalf("theme names = canonical %q display %q, want both Default", theme.Name, theme.DisplayName)
	}
	const description = "Clean theme with standard navigation colors"
	if theme.Description != description || theme.DisplayDescription != description {
		t.Fatalf("theme descriptions = canonical %q display %q, want %q", theme.Description, theme.DisplayDescription, description)
	}
}
