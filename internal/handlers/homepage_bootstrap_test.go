package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"windshift/internal/contextkeys"
	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
)

func TestHomepageRequiresAuthentication(t *testing.T) {
	handler := NewHomepageHandler(nil, nil, nil, nil, nil, nil, nil)
	recorder := httptest.NewRecorder()

	handler.GetHomepage(recorder, httptest.NewRequest(http.MethodGet, "/api/homepage", nil))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestHomepageIncludesDashboardLayoutSnapshot(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "homepage-bootstrap.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	var userID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('homepage@example.test', 'homepage-test', 'Home', 'Page')
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	preferencesService := services.NewUserPreferencesService(
		repository.NewUserPreferencesRepository(db),
		repository.NewThemeRepository(db),
	)
	layout := models.UserDashboardLayout{
		Sections: []models.UserDashboardSection{{
			ID:           "focus",
			Title:        "Focus",
			DisplayOrder: 0,
			WidgetIDs:    []string{"assigned"},
		}},
		Widgets: []models.UserDashboardWidget{{
			ID:        "assigned",
			Type:      "assigned-to-me",
			SectionID: "focus",
			Width:     2,
		}},
	}
	if err := preferencesService.UpdateDashboardLayout(userID, layout); err != nil {
		t.Fatalf("UpdateDashboardLayout: %v", err)
	}

	tracker, err := services.NewActivityTracker(db, services.DefaultActivityTrackerConfig())
	if err != nil {
		t.Fatalf("NewActivityTracker: %v", err)
	}
	t.Cleanup(func() { _ = tracker.Close() })

	handler := NewHomepageHandler(
		repository.NewWorkspaceRepository(db),
		repository.NewItemRepository(db),
		services.NewItemCRUDService(db),
		services.NewPlanningService(db),
		tracker,
		nil,
		preferencesService,
	)
	request := httptest.NewRequest(http.MethodGet, "/api/homepage", nil)
	request = request.WithContext(context.WithValue(request.Context(), contextkeys.User, &models.User{ID: userID}))
	recorder := httptest.NewRecorder()

	handler.GetHomepage(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response HomepageData
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Layout.Sections) != 1 || response.Layout.Sections[0].ID != "focus" {
		t.Fatalf("layout sections = %+v, want saved focus section", response.Layout.Sections)
	}
	if len(response.Layout.Widgets) != 1 || response.Layout.Widgets[0].ID != "assigned" {
		t.Fatalf("layout widgets = %+v, want saved assigned widget", response.Layout.Widgets)
	}
	if response.LayoutRevision == "" {
		t.Fatal("layout_revision must be present")
	}

	_, stableRevision, err := preferencesService.GetDashboardLayoutSnapshot(userID)
	if err != nil {
		t.Fatalf("GetDashboardLayoutSnapshot: %v", err)
	}
	if stableRevision != response.LayoutRevision {
		t.Fatalf("layout revision = %q, want stable %q", response.LayoutRevision, stableRevision)
	}
}
