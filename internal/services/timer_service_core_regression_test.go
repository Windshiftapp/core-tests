package services

import (
	"path/filepath"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/repository"
)

type timerServiceFixture struct {
	db          database.Database
	service     *TimerService
	repo        *repository.ActiveTimerRepository
	userID      int
	workspaceID int
	projectID   int
	timerID     int
}

func TestGetActiveForUserRedactsMetadataAfterWorkspaceRevocation(t *testing.T) {
	fixture := newTimerServiceFixture(t, true, true)
	fixture.service.workspaceAccess = func(int, int) (bool, error) { return false, nil }

	timer, err := fixture.service.GetActiveForUser(fixture.userID)
	if err != nil {
		t.Fatalf("GetActiveForUser: %v", err)
	}
	if timer.WorkspaceID != 0 || timer.ItemID != nil || timer.ItemTitle != nil || timer.WorkspaceName != nil || timer.WorkspaceKey != nil || timer.WorkspaceItemNumber != nil {
		t.Fatalf("restricted timer retained item/workspace metadata: %+v", timer)
	}
	if timer.ProjectName == nil || *timer.ProjectName == "" {
		t.Fatal("unrelated project metadata was unexpectedly removed")
	}
}

func TestStopTimerWithoutProjectCustomerCancelsInsteadOfWedging(t *testing.T) {
	fixture := newTimerServiceFixture(t, false, false)
	fixture.service.workspaceAccess = func(int, int) (bool, error) { return true, nil }

	result, err := fixture.service.StopTimerByID(fixture.userID, fixture.timerID)
	if err != nil {
		t.Fatalf("StopTimerByID: %v", err)
	}
	if result.WorklogCreated {
		t.Fatal("customerless timer unexpectedly created a worklog")
	}

	var timerCount, worklogCount int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM active_timers WHERE id = ?`, fixture.timerID).Scan(&timerCount); err != nil {
		t.Fatalf("count active timer: %v", err)
	}
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM time_worklogs WHERE user_id = ?`, fixture.userID).Scan(&worklogCount); err != nil {
		t.Fatalf("count worklogs: %v", err)
	}
	if timerCount != 0 || worklogCount != 0 {
		t.Fatalf("after recovery timer_count=%d worklog_count=%d, want 0/0", timerCount, worklogCount)
	}
}

func TestStopTimerAttributesWorklogToUserLocalStartDate(t *testing.T) {
	fixture := newTimerServiceFixture(t, true, false)
	if _, err := fixture.db.ExecWrite("UPDATE users SET timezone = 'America/Los_Angeles' WHERE id = ?", fixture.userID); err != nil {
		t.Fatalf("update timezone: %v", err)
	}
	start := time.Date(2026, time.July, 14, 0, 30, 0, 0, time.UTC)
	if _, err := fixture.db.ExecWrite("UPDATE active_timers SET start_time_utc = ? WHERE id = ?", start.Unix(), fixture.timerID); err != nil {
		t.Fatalf("update timer start: %v", err)
	}

	if _, err := fixture.service.StopTimerByID(fixture.userID, fixture.timerID); err != nil {
		t.Fatalf("StopTimerByID: %v", err)
	}
	var dateUnix int64
	if err := fixture.db.QueryRow("SELECT date FROM time_worklogs WHERE user_id = ?", fixture.userID).Scan(&dateUnix); err != nil {
		t.Fatalf("read worklog date: %v", err)
	}
	if got := time.Unix(dateUnix, 0).UTC().Format(time.DateOnly); got != "2026-07-13" {
		t.Fatalf("worklog date = %s, want user-local timer start date 2026-07-13", got)
	}
}

func newTimerServiceFixture(t *testing.T, withCustomer, withItem bool) timerServiceFixture {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "timer-service.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	insert := func(query string, args ...interface{}) int {
		t.Helper()
		result, err := db.ExecWrite(query, args...)
		if err != nil {
			t.Fatalf("insert fixture row: %v", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId: %v", err)
		}
		return int(id)
	}

	userID := insert(`INSERT INTO users (email, username, first_name, last_name) VALUES ('service-timer@example.com', 'service-timer', 'Timer', 'Service')`)
	workspaceID := insert(`INSERT INTO workspaces (name, key, description, active) VALUES ('Restricted timer workspace', 'RTW', '', true)`)

	var customerID interface{}
	if withCustomer {
		customerID = insert(`INSERT INTO customer_organisations (name) VALUES ('Timer customer')`)
	}
	projectID := insert(`INSERT INTO time_projects (customer_id, name, status) VALUES (?, 'Timer project', 'Active')`, customerID)

	var itemID interface{}
	if withItem {
		createdItemID, err := CreateItem(db, ItemCreationParams{WorkspaceID: workspaceID, Title: "Restricted item"})
		if err != nil {
			t.Fatalf("create item: %v", err)
		}
		itemID = int(createdItemID)
	}
	timerID := insert(`
		INSERT INTO active_timers (workspace_id, item_id, project_id, user_id, description, start_time_utc, created_at)
		VALUES (?, ?, ?, ?, 'Running timer', ?, ?)
	`, workspaceID, itemID, projectID, userID, time.Now().Add(-2*time.Minute).Unix(), time.Now().Add(-2*time.Minute).Unix())

	repo := repository.NewActiveTimerRepository(db)
	return timerServiceFixture{
		db:          db,
		repo:        repo,
		service:     NewTimerService(repo, repository.NewItemRepository(db), nil, nil),
		userID:      userID,
		workspaceID: workspaceID,
		projectID:   projectID,
		timerID:     timerID,
	}
}
