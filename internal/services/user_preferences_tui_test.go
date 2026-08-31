package services

import (
	"fmt"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

func openTUIPrefsTestDB(t *testing.T) (database.Database, *UserPreferencesService) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s/tuiprefs.db?mode=memory&cache=shared", t.TempDir())
	db, err := database.NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("init db: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO users(id, email, username, first_name, last_name) VALUES (1, 'u@example.com', 'u', 'U', 'Ser')`,
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	svc := NewUserPreferencesService(
		repository.NewUserPreferencesRepository(db),
		repository.NewThemeRepository(db),
	)
	return db, svc
}

func floatp(v float64) *float64 { return &v }
func intp(v int) *int           { return &v }

func TestTUIPreferencesRoundTrip(t *testing.T) {
	_, svc := openTUIPrefsTestDB(t)

	// Unset → zero value, no error.
	got, err := svc.GetTUI(1)
	if err != nil {
		t.Fatalf("GetTUI on empty prefs: %v", err)
	}
	if got.Theme != "" || got.SplitRatio != nil || got.LastWorkspaceID != nil {
		t.Fatalf("expected zero prefs, got %+v", got)
	}

	if err := svc.UpdateTUI(1, models.UserTUIPreferences{
		Theme:           "void",
		SplitRatio:      floatp(0.42),
		LastWorkspaceID: intp(7),
	}); err != nil {
		t.Fatalf("UpdateTUI: %v", err)
	}

	got, err = svc.GetTUI(1)
	if err != nil {
		t.Fatalf("GetTUI: %v", err)
	}
	if got.Theme != "void" || got.SplitRatio == nil || *got.SplitRatio != 0.42 ||
		got.LastWorkspaceID == nil || *got.LastWorkspaceID != 7 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestTUIPreferencesDoNotClobberDashboardLayout(t *testing.T) {
	_, svc := openTUIPrefsTestDB(t)

	layout := models.UserDashboardLayout{
		Sections: []models.UserDashboardSection{{ID: "s1"}},
		Widgets:  []models.UserDashboardWidget{},
	}
	if err := svc.UpdateDashboardLayout(1, layout); err != nil {
		t.Fatalf("UpdateDashboardLayout: %v", err)
	}

	if err := svc.UpdateTUI(1, models.UserTUIPreferences{Theme: "onyx"}); err != nil {
		t.Fatalf("UpdateTUI: %v", err)
	}

	gotLayout, err := svc.GetDashboardLayout(1)
	if err != nil {
		t.Fatalf("GetDashboardLayout: %v", err)
	}
	if len(gotLayout.Sections) != 1 || gotLayout.Sections[0].ID != "s1" {
		t.Fatalf("dashboard layout clobbered by TUI update: %+v", gotLayout)
	}

	gotTUI, err := svc.GetTUI(1)
	if err != nil || gotTUI.Theme != "onyx" {
		t.Fatalf("TUI prefs lost: %+v err=%v", gotTUI, err)
	}
}

func TestTUIPreferencesNormalization(t *testing.T) {
	_, svc := openTUIPrefsTestDB(t)

	long := make([]byte, 100)
	for i := range long {
		long[i] = 'x'
	}
	if err := svc.UpdateTUI(1, models.UserTUIPreferences{
		Theme:           string(long),
		SplitRatio:      floatp(3.5),
		LastWorkspaceID: intp(-4),
	}); err != nil {
		t.Fatalf("UpdateTUI: %v", err)
	}

	got, err := svc.GetTUI(1)
	if err != nil {
		t.Fatalf("GetTUI: %v", err)
	}
	if len(got.Theme) != 64 {
		t.Fatalf("theme not truncated: len=%d", len(got.Theme))
	}
	if got.SplitRatio == nil || *got.SplitRatio != 0.9 {
		t.Fatalf("split ratio not clamped: %+v", got.SplitRatio)
	}
	if got.LastWorkspaceID != nil {
		t.Fatalf("non-positive workspace id not dropped: %v", *got.LastWorkspaceID)
	}
}
