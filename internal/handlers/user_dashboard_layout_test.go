package handlers

import (
	"bytes"
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

func newDashboardLayoutHandler(t *testing.T) (*UserPreferencesHandler, int) {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "dashboard-layout.db"))
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
		VALUES ('layout@example.test', 'layout-test', 'Lay', 'Out')
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	handler := NewUserPreferencesHandler(services.NewUserPreferencesService(
		repository.NewUserPreferencesRepository(db),
		repository.NewThemeRepository(db),
	))
	return handler, userID
}

func putLayout(t *testing.T, handler *UserPreferencesHandler, userID int, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/user/dashboard-layout", bytes.NewReader(payload))
	req = req.WithContext(context.WithValue(req.Context(), contextkeys.User, &models.User{ID: userID}))
	rr := httptest.NewRecorder()
	handler.UpdateDashboardLayout(rr, req)
	return rr
}

// Regression for WI-829: the 12-column grid must accept widths up to 12.
// The old 1-3 range check rejected every resize and silently dropped the
// change (the store only logs the failure).
func TestUpdateDashboardLayout_Accepts12ColumnWidth(t *testing.T) {
	handler, userID := newDashboardLayoutHandler(t)

	layout := models.UserDashboardLayout{
		GridColumns: 12,
		Sections: []models.UserDashboardSection{{
			ID:           "s-1",
			Title:        "Work",
			DisplayOrder: 0,
			WidgetIDs:    []string{"w-1"},
		}},
		Widgets: []models.UserDashboardWidget{{
			ID:        "w-1",
			Type:      "assigned-to-me",
			SectionID: "s-1",
			Width:     12,
		}},
	}

	rr := putLayout(t, handler, userID, layout)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var saved models.UserDashboardLayout
	if err := json.Unmarshal(rr.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if saved.Widgets[0].Width != 12 {
		t.Fatalf("persisted width = %d, want 12", saved.Widgets[0].Width)
	}
	if saved.GridColumns != 12 {
		t.Fatalf("persisted grid_columns = %d, want 12", saved.GridColumns)
	}
}

// personal-tasks is a seeded widget type and must be accepted by the
// allow-list (it was missing before this fix).
func TestUpdateDashboardLayout_AcceptsPersonalTasksType(t *testing.T) {
	handler, userID := newDashboardLayoutHandler(t)

	layout := models.UserDashboardLayout{
		Sections: []models.UserDashboardSection{{
			ID:           "s-1",
			Title:        "Work",
			DisplayOrder: 0,
			WidgetIDs:    []string{"w-1"},
		}},
		Widgets: []models.UserDashboardWidget{{
			ID:        "w-1",
			Type:      "personal-tasks",
			SectionID: "s-1",
			Width:     6,
		}},
	}

	rr := putLayout(t, handler, userID, layout)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestUpdateDashboardLayout_AcceptsSavedSearchTypeAndConfig(t *testing.T) {
	handler, userID := newDashboardLayoutHandler(t)

	layout := models.UserDashboardLayout{
		Sections: []models.UserDashboardSection{{
			ID:           "s-1",
			Title:        "Work",
			DisplayOrder: 0,
			WidgetIDs:    []string{"w-1"},
		}},
		Widgets: []models.UserDashboardWidget{{
			ID:        "w-1",
			Type:      "saved-search",
			SectionID: "s-1",
			Width:     6,
			Config:    map[string]any{"collectionId": 42},
		}},
	}

	rr := putLayout(t, handler, userID, layout)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var saved models.UserDashboardLayout
	if err := json.Unmarshal(rr.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if saved.Widgets[0].Type != "saved-search" {
		t.Fatalf("saved widget type = %q, want saved-search", saved.Widgets[0].Type)
	}
	if saved.Widgets[0].Config["collectionId"] != float64(42) {
		t.Fatalf("saved collection id = %v, want 42", saved.Widgets[0].Config["collectionId"])
	}
}

// Width 0 and 13 must still be rejected — the bounds just moved, they
// did not disappear.
func TestUpdateDashboardLayout_RejectsWidthOutOfBounds(t *testing.T) {
	handler, userID := newDashboardLayoutHandler(t)

	for _, width := range []int{0, 13} {
		layout := models.UserDashboardLayout{
			Sections: []models.UserDashboardSection{{
				ID:           "s-1",
				Title:        "Work",
				DisplayOrder: 0,
				WidgetIDs:    []string{"w-1"},
			}},
			Widgets: []models.UserDashboardWidget{{
				ID:        "w-1",
				Type:      "assigned-to-me",
				SectionID: "s-1",
				Width:     width,
			}},
		}

		rr := putLayout(t, handler, userID, layout)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("width %d: status = %d, want 400", width, rr.Code)
		}
	}
}
