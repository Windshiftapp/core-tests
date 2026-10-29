//go:build test

package v2

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/models"
	"windshift/internal/services"
)

type preferencesFixture struct {
	fakeCatalogs
	last services.UserPreferencesPatch
}

func (f *preferencesFixture) GetSnapshot(int) (services.UserPreferencesSnapshot, error) {
	return services.UserPreferencesSnapshot{
		ColorMode:    "dark",
		ViewState:    map[string]map[string]any{"collection-5": {"board": map[string]any{"sort_mode": "bubble"}}},
		SavedFilters: map[string]any{"collection-5": []any{map[string]any{"id": "a"}}},
		DueDateRemindersEnabled: true,
		DueDateReminderDays:     7,
	}, nil
}
func (f *preferencesFixture) UpdateSnapshot(_ int, patch services.UserPreferencesPatch) (services.UserPreferencesSnapshot, error) {
	f.last = patch
	return services.UserPreferencesSnapshot{
		ColorMode: "dark",
		TUI: models.UserTUIPreferences{
			Theme: patch.TUITheme, SplitRatio: patch.TUISplitRatio, LastWorkspaceID: patch.LastWorkspaceID,
		},
	}, nil
}

func TestPreferencesExposeViewStateAndSavedFilters(t *testing.T) {
	fixture := &preferencesFixture{}
	mux, _ := newTestMuxWithCatalog(t, true, fixture)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/users/me/preferences", nil)
	request.Header.Set("X-Test-Session", "valid")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body)
	}
	var body struct {
		Data struct {
			ViewState    map[string]map[string]any `json:"view_state"`
			SavedFilters map[string]any            `json:"saved_filters"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	board, _ := body.Data.ViewState["collection-5"]["board"].(map[string]any)
	if board["sort_mode"] != "bubble" {
		t.Fatalf("view_state = %#v", body.Data.ViewState)
	}
	filters, _ := body.Data.SavedFilters["collection-5"].([]any)
	if len(filters) != 1 {
		t.Fatalf("saved_filters = %#v", body.Data.SavedFilters)
	}
}

func TestPreferencePatchCarriesViewStateAndSavedFilters(t *testing.T) {
	fixture := &preferencesFixture{}
	mux, _ := newTestMuxWithCatalog(t, true, fixture)
	body := `{"view_state":{"collection-5":{"board":{"sort_mode":"bubble"}}},` +
		`"saved_filters":{"collection-5":[{"id":"a","name":"Mine","ql":"id > 0"}]}}`
	request := httptest.NewRequest(http.MethodPatch, "/api/v2/users/me/preferences", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/merge-patch+json")
	request.Header.Set("X-Test-Session", "valid")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body)
	}
	if !fixture.last.ViewStateSet || !fixture.last.SavedFiltersSet {
		t.Fatalf("patch flags = %#v", fixture.last)
	}
	board, _ := fixture.last.ViewState["collection-5"]["board"].(map[string]any)
	if board["sort_mode"] != "bubble" {
		t.Fatalf("view state = %#v", fixture.last.ViewState["collection-5"]["board"])
	}
	filters, _ := fixture.last.SavedFilters["collection-5"].([]any)
	if len(filters) != 1 {
		t.Fatalf("saved filters = %#v", fixture.last.SavedFilters["collection-5"])
	}
	filter, _ := filters[0].(map[string]any)
	if filter["name"] != "Mine" || filter["ql"] != "id > 0" {
		t.Fatalf("saved filter = %#v", filter)
	}
}

func TestPreferencesCarryDueDateReminders(t *testing.T) {
	fixture := &preferencesFixture{}
	mux, _ := newTestMuxWithCatalog(t, true, fixture)

	getRequest := httptest.NewRequest(http.MethodGet, "/api/v2/users/me/preferences", nil)
	getRequest.Header.Set("X-Test-Session", "valid")
	getResponse := httptest.NewRecorder()
	mux.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get status = %d; body=%s", getResponse.Code, getResponse.Body)
	}
	var body struct {
		Data struct {
			Enabled bool `json:"due_date_reminders_enabled"`
			Days    int  `json:"due_date_reminder_days"`
		} `json:"data"`
	}
	if err := json.Unmarshal(getResponse.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Data.Enabled || body.Data.Days != 7 {
		t.Fatalf("reminder prefs = %#v", body.Data)
	}

	patchRequest := httptest.NewRequest(http.MethodPatch, "/api/v2/users/me/preferences",
		bytes.NewBufferString(`{"due_date_reminders_enabled":true,"due_date_reminder_days":5}`))
	patchRequest.Header.Set("Content-Type", "application/merge-patch+json")
	patchRequest.Header.Set("X-Test-Session", "valid")
	patchResponse := httptest.NewRecorder()
	mux.ServeHTTP(patchResponse, patchRequest)
	if patchResponse.Code != http.StatusOK {
		t.Fatalf("patch status = %d; body=%s", patchResponse.Code, patchResponse.Body)
	}
	if !fixture.last.DueDateRemindersEnabledSet || !fixture.last.DueDateRemindersEnabled {
		t.Fatalf("enabled patch = %#v", fixture.last)
	}
	if !fixture.last.DueDateReminderDaysSet || fixture.last.DueDateReminderDays != 5 {
		t.Fatalf("days patch = %#v", fixture.last)
	}
}

func TestPreferencePatchPreservesOmittedNestedFields(t *testing.T) {
	fixture := &preferencesFixture{}
	mux, _ := newTestMuxWithCatalog(t, true, fixture)
	request := httptest.NewRequest(http.MethodPatch, "/api/v2/users/me/preferences", bytes.NewBufferString(`{"tui":{"theme":"dracula"}}`))
	request.Header.Set("Content-Type", "application/merge-patch+json")
	request.Header.Set("X-Test-Session", "valid")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body)
	}
	if !fixture.last.TUIThemeSet || fixture.last.TUITheme != "dracula" {
		t.Fatalf("theme patch = %#v", fixture.last)
	}
	if fixture.last.TUISplitRatioSet || fixture.last.LastWorkspaceIDSet {
		t.Fatalf("omitted nested fields became present: %#v", fixture.last)
	}
}
