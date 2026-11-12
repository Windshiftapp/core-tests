package services

import (
	"path/filepath"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/repository"
	"windshift/internal/testutils"
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

func TestStopTimerWithoutRealCustomerCreatesWorklog(t *testing.T) {
	fixture := newTimerServiceFixture(t, false, false)
	fixture.service.workspaceAccess = func(int, int) (bool, error) { return true, nil }

	result, err := fixture.service.StopTimerByID(fixture.userID, fixture.timerID)
	if err != nil {
		t.Fatalf("StopTimerByID: %v", err)
	}
	if !result.WorklogCreated {
		t.Fatal("timer on a no-customer project did not create a worklog")
	}

	var timerCount, worklogCount int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM active_timers WHERE id = ?`, fixture.timerID).Scan(&timerCount); err != nil {
		t.Fatalf("count active timer: %v", err)
	}
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM time_worklogs WHERE user_id = ?`, fixture.userID).Scan(&worklogCount); err != nil {
		t.Fatalf("count worklogs: %v", err)
	}
	if timerCount != 0 || worklogCount != 1 {
		t.Fatalf("after stop timer_count=%d worklog_count=%d, want 0/1", timerCount, worklogCount)
	}

	// The worklog derives the built-in no-customer placeholder from the project.
	var builtinKey string
	if err := fixture.db.QueryRow(`
		SELECT COALESCE(c.builtin_key, '')
		FROM time_worklogs w
		JOIN time_projects p ON w.project_id = p.id
		JOIN customer_organisations c ON p.customer_id = c.id
		WHERE w.user_id = ?
	`, fixture.userID).Scan(&builtinKey); err != nil {
		t.Fatalf("read derived customer: %v", err)
	}
	if builtinKey != "no_customer" {
		t.Fatalf("worklog customer builtin_key = %q, want no_customer", builtinKey)
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
		id := testutils.InsertID(t, db, query, args...)
		return int(id)
	}

	userID := insert(`INSERT INTO users (email, username, first_name, last_name) VALUES ('service-timer@example.com', 'service-timer', 'Timer', 'Service')`)
	workspaceID := insert(`INSERT INTO workspaces (name, key, description, active) VALUES ('Restricted timer workspace', 'RTW', '', true)`)

	var customerID interface{}
	if withCustomer {
		customerID = insert(`INSERT INTO customer_organisations (name) VALUES ('Timer customer')`)
	} else {
		var noCustomerID int
		if err := db.QueryRow(`SELECT id FROM customer_organisations WHERE builtin_key = 'no_customer'`).Scan(&noCustomerID); err != nil {
			t.Fatalf("load no-customer organisation: %v", err)
		}
		customerID = noCustomerID
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
