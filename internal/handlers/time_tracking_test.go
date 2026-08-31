//go:build test

package handlers

import (
	"net/http"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// newTestTimeWorklogHandler builds a TimeWorklogHandler with the real
// PermissionService + TimePermissionService so handler paths that consult
// project / system-admin permissions don't deref a nil service pointer.
func newTestTimeWorklogHandler(t *testing.T, tdb *testutils.TestDB) *TimeWorklogHandler {
	t.Helper()
	permService, _, _ := createTestServices(t, *tdb)
	timePerm := services.NewTimePermissionService(tdb.GetDatabase(), permService)
	return NewTimeWorklogHandler(tdb.GetDatabase(), permService, timePerm)
}

// createTestCustomer creates a customer organisation through its repository.
func createTestCustomer(t *testing.T, tdb *testutils.TestDB) int {
	t.Helper()
	return newServiceSetup(t, tdb).CreateCustomer("Test Customer")
}

// createTestTimeProject creates a time project through its repository.
func createTestTimeProject(t *testing.T, tdb *testutils.TestDB, name string, customerID int) int {
	t.Helper()
	return newServiceSetup(t, tdb).CreateTimeProject(name, customerID)
}

func TestTimeWorklogHandler_Create_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)

	// Create the customer through the setup helper.
	customerID := createTestCustomer(t, tdb)

	// Create the project through the setup helper.
	_ = createTestTimeProject(t, tdb, "Test Project", customerID)

	// Create work item for linking
	permService, actTracker, notifService := createTestServices(t, *tdb)
	itemHandler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)
	item := models.Item{
		WorkspaceID: data.WorkspaceID,
		Title:       "Test Work Item",
		Description: "Test description",
		StatusID:    &data.StatusID,
		PriorityID:  &data.PriorityID,
	}
	createReq := testutils.CreateJSONRequest(t, "POST", "/api/items", item)
	createRR := testutils.ExecuteAuthenticatedRequest(t, itemHandler.Create, createReq, nil)
	var createdItem models.Item
	createRR.AssertJSONResponse(&createdItem)

	handler := newTestTimeWorklogHandler(t, tdb)

	worklogReq := WorklogRequest{
		ProjectID:     1,
		ItemID:        &createdItem.ID,
		Description:   "Working on test feature",
		Date:          "2024-01-15",
		StartTime:     "09:00",
		EndTime:       "11:30",
		DurationInput: "2h30m",
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/worklogs", worklogReq)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

	rr.AssertStatusCode(http.StatusCreated).
		AssertContentType("application/json")

	var response models.Worklog
	rr.AssertJSONResponse(&response)

	if response.ID == 0 {
		t.Error("Expected created worklog to have an ID")
	}
	if response.DurationMins != 150 {
		t.Errorf("Expected duration 150 minutes (2h30m), got %d", response.DurationMins)
	}
	if response.Description != worklogReq.Description {
		t.Errorf("Expected description %s, got %s", worklogReq.Description, response.Description)
	}
	if response.ItemID == nil || *response.ItemID != createdItem.ID {
		t.Errorf("Expected item ID %d, got %v", createdItem.ID, response.ItemID)
	}
}

func TestTimeWorklogHandler_Create_DurationOnly(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)

	// Create test data below the HTTP layer.
	customerID := createTestCustomer(t, tdb)
	_ = createTestTimeProject(t, tdb, "Test Project", customerID)

	handler := newTestTimeWorklogHandler(t, tdb)

	worklogReq := WorklogRequest{
		ProjectID:     1,
		Description:   "Ad-hoc work",
		Date:          "2024-01-15",
		DurationInput: "4h",
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/worklogs", worklogReq)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

	rr.AssertStatusCode(http.StatusCreated)

	var response models.Worklog
	rr.AssertJSONResponse(&response)

	if response.DurationMins != 240 {
		t.Errorf("Expected duration 240 minutes (4h), got %d", response.DurationMins)
	}
	// The handler calculates start/end times even for duration-only entries
	if response.StartTime == 0 || response.EndTime == 0 {
		t.Error("Expected calculated start/end times for duration entry")
	}
}

func TestTimeWorklogHandler_ParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected int // minutes
		hasError bool
	}{
		{"1h", 60, false},
		{"30m", 30, false},
		{"2h30m", 150, false},
		{"1d", 480, false}, // 1 day = 8 hours = 480 minutes
		{"0.5h", 30, false},
		{"1h15m", 75, false},
		{"invalid", 0, true},
		{"", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			duration, err := ParseDuration(tt.input)

			if tt.hasError {
				if err == nil {
					t.Errorf("Expected error for input %s, but got none", tt.input)
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error for input %s: %v", tt.input, err)
				return
			}

			actualMinutes := int(duration.Minutes())
			if actualMinutes != tt.expected {
				t.Errorf("For input %s, expected %d minutes, got %d", tt.input, tt.expected, actualMinutes)
			}
		})
	}
}

func TestTimeWorklogHandler_GetAll_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Set up test data below the HTTP layer.
	customerID := createTestCustomer(t, tdb)
	_ = createTestTimeProject(t, tdb, "Test Project", customerID)

	// Create worklogs directly in database for testing filters
	testWorklogs := []struct {
		date        int64 // Unix timestamp
		description string
		duration    int
	}{
		{1705276800, "Morning work", 120},   // 2024-01-15
		{1705363200, "Afternoon work", 180}, // 2024-01-16
		{1705449600, "Evening work", 90},    // 2024-01-17
	}

	for _, wl := range testWorklogs {
		startTime := wl.date + 9*3600                // 09:00 on that day
		endTime := startTime + int64(wl.duration*60) // Add duration
		createdAt := time.Now().Unix()

		if _, err := tdb.Exec(`
			INSERT INTO time_worklogs (project_id, customer_id, description, date, start_time, end_time, duration_minutes, created_at, updated_at)
			VALUES (1, 1, ?, ?, ?, ?, ?, ?, ?)
		`, wl.description, wl.date, startTime, endTime, wl.duration, createdAt, createdAt); err != nil {
			t.Fatalf("Failed to create test worklog: %v", err)
		}
	}

	handler := newTestTimeWorklogHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/worklogs", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var worklogs []models.Worklog
	rr.AssertJSONResponse(&worklogs)

	if len(worklogs) != 3 {
		t.Errorf("Expected 3 worklogs, got %d", len(worklogs))
	}

	// Check that customer and project names are populated
	for _, wl := range worklogs {
		if wl.CustomerName != "Test Customer" {
			t.Errorf("Expected customer name 'Test Customer', got %s", wl.CustomerName)
		}
		if wl.ProjectName != "Test Project" {
			t.Errorf("Expected project name 'Test Project', got %s", wl.ProjectName)
		}
	}
}

func TestTimeWorklogHandler_GetAll_WithFilters(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Set up test data with two projects below the HTTP layer.
	customerID := createTestCustomer(t, tdb)
	_ = createTestTimeProject(t, tdb, "Project A", customerID)
	_ = createTestTimeProject(t, tdb, "Project B", customerID)

	// Create worklogs for different projects and dates
	testWorklogs := []struct {
		projectID   int
		date        int64
		description string
		duration    int
	}{
		{1, 1705276800, "Work on Project A", 120},  // 2024-01-15
		{2, 1705276800, "Work on Project B", 180},  // 2024-01-15
		{1, 1705363200, "More Project A work", 90}, // 2024-01-16
	}

	for _, wl := range testWorklogs {
		startTime := wl.date + 9*3600                // 09:00 on that day
		endTime := startTime + int64(wl.duration*60) // Add duration
		createdAt := time.Now().Unix()

		if _, err := tdb.Exec(`
			INSERT INTO time_worklogs (project_id, customer_id, description, date, start_time, end_time, duration_minutes, created_at, updated_at)
			VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?)
		`, wl.projectID, wl.description, wl.date, startTime, endTime, wl.duration, createdAt, createdAt); err != nil {
			t.Fatalf("Failed to create test worklog: %v", err)
		}
	}

	handler := newTestTimeWorklogHandler(t, tdb)

	// Test project filter
	req := testutils.CreateJSONRequest(t, "GET", "/api/worklogs?project_id=1", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	var projectWorklogs []models.Worklog
	rr.AssertJSONResponse(&projectWorklogs)

	if len(projectWorklogs) != 2 {
		t.Errorf("Expected 2 worklogs for project 1, got %d", len(projectWorklogs))
	}

	// Test date range filter
	req2 := testutils.CreateJSONRequest(t, "GET", "/api/worklogs?date_from=2024-01-15&date_to=2024-01-15", nil)
	rr2 := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req2, nil)

	rr2.AssertStatusCode(http.StatusOK)

	var dateWorklogs []models.Worklog
	rr2.AssertJSONResponse(&dateWorklogs)

	if len(dateWorklogs) != 2 {
		t.Errorf("Expected 2 worklogs for date 2024-01-15, got %d", len(dateWorklogs))
	}
}

func TestTimeWorklogHandler_Get_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Set up test data below the HTTP layer.
	customerID := createTestCustomer(t, tdb)
	_ = createTestTimeProject(t, tdb, "Test Project", customerID)

	// Create test worklog
	baseDate := int64(1705276800)  // 2024-01-15
	startTime := baseDate + 9*3600 // 09:00
	endTime := baseDate + 11*3600  // 11:00
	createdAt := time.Now().Unix()

	var worklogID int
	if err := tdb.QueryRow(`
		INSERT INTO time_worklogs (project_id, customer_id, description, date, start_time, end_time, duration_minutes, created_at, updated_at)
		VALUES (1, 1, 'Test worklog', ?, ?, ?, 120, ?, ?) RETURNING id
	`, baseDate, startTime, endTime, createdAt, createdAt).Scan(&worklogID); err != nil {
		t.Fatalf("Failed to create test worklog: %v", err)
	}

	handler := newTestTimeWorklogHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/worklogs/"+testutils.IntToString(int(worklogID)), nil)
	req.SetPathValue("id", testutils.IntToString(int(worklogID)))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Get, req, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var worklog models.Worklog
	rr.AssertJSONResponse(&worklog)

	if worklog.ID != int(worklogID) {
		t.Errorf("Expected worklog ID %d, got %d", worklogID, worklog.ID)
	}
	if worklog.Description != "Test worklog" {
		t.Errorf("Expected description 'Test worklog', got %s", worklog.Description)
	}
	if worklog.DurationMins != 120 {
		t.Errorf("Expected duration 120 minutes, got %d", worklog.DurationMins)
	}
}

func TestTimeWorklogHandler_ValidationErrors(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Create the test customer and project below the HTTP layer.
	customerID := createTestCustomer(t, tdb)
	_ = createTestTimeProject(t, tdb, "Test Project", customerID)

	handler := newTestTimeWorklogHandler(t, tdb)

	tests := []struct {
		name        string
		request     WorklogRequest
		expectedErr string
	}{
		{
			name:        "Missing project ID",
			request:     WorklogRequest{Description: "Test", Date: "2024-01-15", DurationInput: "1h"},
			expectedErr: "project not found",
		},
		{
			name:        "Invalid date format",
			request:     WorklogRequest{ProjectID: 1, Description: "Test", Date: "invalid-date", DurationInput: "1h"},
			expectedErr: "invalid date format",
		},
		{
			name:        "Invalid duration",
			request:     WorklogRequest{ProjectID: 1, Description: "Test", Date: "2024-01-15", DurationInput: "invalid"},
			expectedErr: "invalid duration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := testutils.CreateJSONRequest(t, "POST", "/api/worklogs", tt.request)
			rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

			testutils.AssertValidationError(t, rr, tt.expectedErr)
		})
	}
}

func TestTimeWorklogHandler_Update_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Create the test customer below the HTTP layer.
	customerID := createTestCustomer(t, tdb)

	// Create the time project below the HTTP layer.
	_ = createTestTimeProject(t, tdb, "Test Project", customerID)

	// Create test worklog
	baseDate := int64(1705276800)  // 2024-01-15
	startTime := baseDate + 9*3600 // 09:00
	endTime := baseDate + 11*3600  // 11:00
	createdAt := time.Now().Unix()

	var worklogID int
	if err := tdb.QueryRow(`
		INSERT INTO time_worklogs (project_id, customer_id, description, date, start_time, end_time, duration_minutes, created_at, updated_at)
		VALUES (1, 1, 'Original description', ?, ?, ?, 120, ?, ?) RETURNING id
	`, baseDate, startTime, endTime, createdAt, createdAt).Scan(&worklogID); err != nil {
		t.Fatalf("Failed to create test worklog: %v", err)
	}

	handler := newTestTimeWorklogHandler(t, tdb)

	updateReq := WorklogRequest{
		ProjectID:     1,
		Description:   "Updated description",
		Date:          "2024-01-15",
		DurationInput: "3h",
	}

	req := testutils.CreateJSONRequest(t, "PUT", "/api/worklogs/"+testutils.IntToString(int(worklogID)), updateReq)
	req.SetPathValue("id", testutils.IntToString(int(worklogID)))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var response models.Worklog
	rr.AssertJSONResponse(&response)

	if response.ID != int(worklogID) {
		t.Errorf("Expected worklog ID %d, got %d", worklogID, response.ID)
	}
	if response.ProjectID != updateReq.ProjectID {
		t.Errorf("Expected project ID %d, got %d", updateReq.ProjectID, response.ProjectID)
	}
	if response.CustomerID != 1 {
		t.Errorf("Expected customer ID 1, got %d", response.CustomerID)
	}
	if response.Description != updateReq.Description {
		t.Errorf("Expected description %q, got %q", updateReq.Description, response.Description)
	}
	if response.DurationMins != 180 {
		t.Errorf("Expected duration 180 minutes, got %d", response.DurationMins)
	}
	if response.Date != baseDate {
		t.Errorf("Expected date %d, got %d", baseDate, response.Date)
	}
	if diff := response.EndTime - response.StartTime; diff != int64(response.DurationMins*60) {
		t.Errorf("Expected start/end difference %d seconds, got %d", response.DurationMins*60, diff)
	}
	if response.ItemID != nil {
		t.Errorf("Expected ItemID to remain nil, got %v", response.ItemID)
	}
}

func TestTimeWorklogHandler_Update_InvalidID(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := newTestTimeWorklogHandler(t, tdb)

	updateReq := WorklogRequest{
		ProjectID:     1,
		Description:   "Test",
		Date:          "2024-01-15",
		DurationInput: "1h",
	}

	req := testutils.CreateJSONRequest(t, "PUT", "/api/worklogs/invalid", updateReq)
	req.SetPathValue("id", "invalid")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
}

func TestTimeWorklogHandler_Delete_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Create the test customer below the HTTP layer.
	customerID := createTestCustomer(t, tdb)

	// Create the time project below the HTTP layer.
	_ = createTestTimeProject(t, tdb, "Test Project", customerID)

	// Create test worklog
	baseDate := int64(1705276800)  // 2024-01-15
	startTime := baseDate + 9*3600 // 09:00
	endTime := baseDate + 11*3600  // 11:00
	createdAt := time.Now().Unix()

	var worklogID int
	if err := tdb.QueryRow(`
		INSERT INTO time_worklogs (project_id, customer_id, description, date, start_time, end_time, duration_minutes, created_at, updated_at)
		VALUES (1, 1, 'Test worklog', ?, ?, ?, 120, ?, ?) RETURNING id
	`, baseDate, startTime, endTime, createdAt, createdAt).Scan(&worklogID); err != nil {
		t.Fatalf("Failed to create test worklog: %v", err)
	}

	handler := newTestTimeWorklogHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/worklogs/"+testutils.IntToString(int(worklogID)), nil)
	req.SetPathValue("id", testutils.IntToString(int(worklogID)))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, req, nil)

	rr.AssertStatusCode(http.StatusNoContent)

	// Verify worklog was deleted from database
	var count int
	if err := tdb.QueryRow("SELECT COUNT(*) FROM time_worklogs WHERE id = ?", worklogID).Scan(&count); err != nil {
		t.Fatalf("Failed to verify worklog deletion: %v", err)
	}
	if count != 0 {
		t.Error("Worklog was not deleted from database")
	}
}

func TestTimeWorklogHandler_Delete_InvalidID(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := newTestTimeWorklogHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/worklogs/invalid", nil)
	req.SetPathValue("id", "invalid")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
}

func TestTimeWorklogHandler_Delete_NotFound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := newTestTimeWorklogHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/worklogs/99999", nil)
	req.SetPathValue("id", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, req, nil)

	// CanEditWorklog cannot distinguish "doesn't exist" from "no permission"
	// (both return false), and the handler refuses to leak existence info, so
	// an unknown id now responds 403 rather than the previous idempotent 204.
	rr.AssertStatusCode(http.StatusForbidden)
}
