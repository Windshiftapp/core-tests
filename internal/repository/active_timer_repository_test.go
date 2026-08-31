package repository

import (
	"errors"
	"path/filepath"
	"testing"

	"windshift/internal/database"
)

func TestFinalizeTimerRollsBackWorklogWhenTimerDeleteFails(t *testing.T) {
	fixture := newActiveTimerFixture(t)

	if _, err := fixture.db.ExecWrite(`
		CREATE TRIGGER fail_active_timer_delete
		BEFORE DELETE ON active_timers
		BEGIN
			SELECT RAISE(ABORT, 'forced timer delete failure');
		END
	`); err != nil {
		t.Fatalf("create delete trigger: %v", err)
	}

	err := fixture.repo.FinalizeTimer(fixture.timerID, fixture.worklog)
	if err == nil {
		t.Fatal("FinalizeTimer succeeded despite forced timer delete failure")
	}

	assertRowCount(t, fixture.db, "SELECT COUNT(*) FROM time_worklogs", 0)
	assertRowCount(t, fixture.db, "SELECT COUNT(*) FROM active_timers", 1)
}

func TestFinalizeTimerCreatesWorklogAndRemovesTimer(t *testing.T) {
	fixture := newActiveTimerFixture(t)

	if err := fixture.repo.FinalizeTimer(fixture.timerID, fixture.worklog); err != nil {
		t.Fatalf("FinalizeTimer: %v", err)
	}

	assertRowCount(t, fixture.db, "SELECT COUNT(*) FROM time_worklogs", 1)
	assertRowCount(t, fixture.db, "SELECT COUNT(*) FROM active_timers", 0)
}

func TestFinalizeTimerRollsBackWorklogWhenTimerNoLongerExists(t *testing.T) {
	fixture := newActiveTimerFixture(t)
	if err := fixture.repo.DeleteTimer(fixture.timerID); err != nil {
		t.Fatalf("DeleteTimer: %v", err)
	}

	err := fixture.repo.FinalizeTimer(fixture.timerID, fixture.worklog)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("FinalizeTimer error = %v, want ErrNotFound", err)
	}

	assertRowCount(t, fixture.db, "SELECT COUNT(*) FROM time_worklogs", 0)
	assertRowCount(t, fixture.db, "SELECT COUNT(*) FROM active_timers", 0)
}

type activeTimerFixture struct {
	db      database.Database
	repo    *ActiveTimerRepository
	timerID int
	worklog CreateWorklogInput
}

func newActiveTimerFixture(t *testing.T) activeTimerFixture {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "active-timer.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	userID := insertTestRow(t, db, `
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('timer@example.com', 'timer-test', 'Timer', 'Test')
	`)
	workspaceID := insertTestRow(t, db, `
		INSERT INTO workspaces (name, key, description)
		VALUES ('Timer workspace', 'TMR', '')
	`)
	customerID := insertTestRow(t, db, `
		INSERT INTO customer_organisations (name)
		VALUES ('Timer customer')
	`)
	projectID := insertTestRow(t, db, `
		INSERT INTO time_projects (customer_id, name, status)
		VALUES (?, 'Timer project', 'Active')
	`, customerID)
	timerID := insertTestRow(t, db, `
		INSERT INTO active_timers (
			workspace_id, project_id, user_id, description, start_time_utc, created_at
		) VALUES (?, ?, ?, 'Investigate timer', 100, 100)
	`, workspaceID, projectID, userID)

	return activeTimerFixture{
		db:      db,
		repo:    NewActiveTimerRepository(db),
		timerID: timerID,
		worklog: CreateWorklogInput{
			ProjectID:       projectID,
			CustomerID:      customerID,
			UserID:          userID,
			Description:     "Investigate timer",
			DateUnix:        0,
			StartTimeUnix:   100,
			EndTimeUnix:     220,
			DurationMinutes: 2,
			NowUnix:         220,
		},
	}
}

func insertTestRow(t *testing.T, db database.Database, query string, args ...interface{}) int {
	t.Helper()
	result, err := db.ExecWrite(query, args...)
	if err != nil {
		t.Fatalf("insert test row: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	return int(id)
}

func assertRowCount(t *testing.T, db database.Database, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if got != want {
		t.Fatalf("row count = %d, want %d", got, want)
	}
}
