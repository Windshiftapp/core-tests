//go:build test

package handlers

import (
	"net/http"
	"strings"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// createLeaveHandler creates a LeaveHandler with test dependencies
func createLeaveHandler(t *testing.T, tdb *testutils.TestDB) *LeaveHandler {
	t.Helper()
	permService, _, _ := createTestServices(t, *tdb)
	leaveRepo := repository.NewLeaveRepository(tdb.GetDatabase())
	userRepo := repository.NewUserRepository(tdb.GetDatabase())
	return NewLeaveHandler(leaveRepo, userRepo, permService)
}

// --- GetForUser ---

func TestLeaveHandler_GetForUser_Self(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createLeaveHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/users/1/leave", nil)
	req.SetPathValue("userId", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetForUser, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	var periods []models.UserLeavePeriod
	rr.AssertJSONResponse(&periods)
	if len(periods) != 0 {
		t.Errorf("Expected empty list, got %d periods", len(periods))
	}
}

func TestLeaveHandler_GetForUser_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createLeaveHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/users/1/leave", nil)
	req.SetPathValue("userId", "1")
	rr := testutils.ExecuteRequest(t, handler.GetForUser, req)
	rr.AssertStatusCode(http.StatusUnauthorized)
}

// --- Create ---

func TestLeaveHandler_Create_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createLeaveHandler(t, tdb)

	body := models.UserLeavePeriodRequest{
		StartDate: "2026-04-01",
		EndDate:   "2026-04-10",
		Reason:    "Vacation",
	}
	req := testutils.CreateJSONRequest(t, "POST", "/api/users/1/leave", body)
	req.SetPathValue("userId", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
	rr.AssertStatusCode(http.StatusCreated)

	var leave models.UserLeavePeriod
	rr.AssertJSONResponse(&leave)
	if !strings.HasPrefix(leave.StartDate, "2026-04-01") {
		t.Errorf("Expected start_date to start with '2026-04-01', got %s", leave.StartDate)
	}
	if leave.Reason != "Vacation" {
		t.Errorf("Expected reason 'Vacation', got %s", leave.Reason)
	}
}

func TestLeaveHandler_Create_WithSubstitute(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createLeaveHandler(t, tdb)

	// Create second user for substitute
	_, err := tdb.GetDatabase().Exec(`
		INSERT INTO users (id, email, username, first_name, last_name, password_hash, is_active)
		VALUES (2, 'sub@example.com', 'substitute', 'Sub', 'User', '$2a$10$hash', true)
	`)
	if err != nil {
		t.Fatalf("Failed to create substitute user: %v", err)
	}

	subID := 2
	body := models.UserLeavePeriodRequest{
		SubstituteUserID: &subID,
		StartDate:        "2026-05-01",
		EndDate:          "2026-05-15",
		Reason:           "Holiday",
	}
	req := testutils.CreateJSONRequest(t, "POST", "/api/users/1/leave", body)
	req.SetPathValue("userId", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
	rr.AssertStatusCode(http.StatusCreated)

	var leave models.UserLeavePeriod
	rr.AssertJSONResponse(&leave)
	if leave.SubstituteUserID == nil || *leave.SubstituteUserID != 2 {
		t.Errorf("Expected substitute_user_id 2, got %v", leave.SubstituteUserID)
	}
}

func TestLeaveHandler_Create_InvalidDates(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createLeaveHandler(t, tdb)

	body := models.UserLeavePeriodRequest{
		StartDate: "2026-04-10",
		EndDate:   "2026-04-01",
		Reason:    "Invalid",
	}
	req := testutils.CreateJSONRequest(t, "POST", "/api/users/1/leave", body)
	req.SetPathValue("userId", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
	rr.AssertStatusCode(http.StatusBadRequest)
}

func TestLeaveHandler_Create_MissingStartDate(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createLeaveHandler(t, tdb)

	body := models.UserLeavePeriodRequest{
		EndDate: "2026-04-10",
		Reason:  "No start",
	}
	req := testutils.CreateJSONRequest(t, "POST", "/api/users/1/leave", body)
	req.SetPathValue("userId", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
	rr.AssertStatusCode(http.StatusBadRequest)
}

func TestLeaveHandler_Create_SelfSubstitute(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createLeaveHandler(t, tdb)

	selfID := 1
	body := models.UserLeavePeriodRequest{
		SubstituteUserID: &selfID,
		StartDate:        "2026-04-01",
		EndDate:          "2026-04-10",
		Reason:           "Self sub",
	}
	req := testutils.CreateJSONRequest(t, "POST", "/api/users/1/leave", body)
	req.SetPathValue("userId", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
	rr.AssertStatusCode(http.StatusBadRequest)
}

func TestLeaveHandler_Create_InvalidSubstitute(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createLeaveHandler(t, tdb)

	badID := 99999
	body := models.UserLeavePeriodRequest{
		SubstituteUserID: &badID,
		StartDate:        "2026-04-01",
		EndDate:          "2026-04-10",
		Reason:           "Bad sub",
	}
	req := testutils.CreateJSONRequest(t, "POST", "/api/users/1/leave", body)
	req.SetPathValue("userId", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
	rr.AssertStatusCode(http.StatusBadRequest)
}

// --- Update ---

func TestLeaveHandler_Create_InactiveSubstituteMatchesUnknownUser(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createLeaveHandler(t, tdb)

	var inactiveUserID int
	if err := tdb.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, password_hash, is_active)
		VALUES ('inactive-substitute@example.com', 'inactive-substitute', 'Inactive', 'Substitute', 'hash', false)
		RETURNING id
	`).Scan(&inactiveUserID); err != nil {
		t.Fatalf("insert inactive substitute: %v", err)
	}

	var firstBody string
	for _, substituteID := range []int{inactiveUserID, 999999} {
		body := models.UserLeavePeriodRequest{
			SubstituteUserID: &substituteID,
			StartDate:        "2026-04-01",
			EndDate:          "2026-04-10",
			Reason:           "Unavailable",
		}
		req := testutils.CreateJSONRequest(t, "POST", "/api/users/1/leave", body)
		req.SetPathValue("userId", "1")
		rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
		rr.AssertStatusCode(http.StatusBadRequest)

		responseBody := rr.Body.String()
		if firstBody == "" {
			firstBody = responseBody
		} else if responseBody != firstBody {
			t.Fatalf("inactive and unknown substitute responses differ: inactive=%q unknown=%q", firstBody, responseBody)
		}
	}
}

func TestLeaveHandler_Update_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createLeaveHandler(t, tdb)

	// Create a leave period first
	createBody := models.UserLeavePeriodRequest{
		StartDate: "2026-04-01",
		EndDate:   "2026-04-10",
		Reason:    "Original",
	}
	createReq := testutils.CreateJSONRequest(t, "POST", "/api/users/1/leave", createBody)
	createReq.SetPathValue("userId", "1")
	createRR := testutils.ExecuteAuthenticatedRequest(t, handler.Create, createReq, nil)
	createRR.AssertStatusCode(http.StatusCreated)
	var created models.UserLeavePeriod
	createRR.AssertJSONResponse(&created)

	// Update the leave period
	updateBody := models.UserLeavePeriodRequest{
		StartDate: "2026-04-05",
		EndDate:   "2026-04-15",
		Reason:    "Updated",
	}
	updateReq := testutils.CreateJSONRequest(t, "PUT", "/api/users/1/leave/"+testutils.IntToString(created.ID), updateBody)
	updateReq.SetPathValue("userId", "1")
	updateReq.SetPathValue("leaveId", testutils.IntToString(created.ID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, updateReq, nil)
	rr.AssertStatusCode(http.StatusOK)

	var updated models.UserLeavePeriod
	rr.AssertJSONResponse(&updated)
	if updated.Reason != "Updated" {
		t.Errorf("Expected reason 'Updated', got %s", updated.Reason)
	}
	if !strings.HasPrefix(updated.StartDate, "2026-04-05") {
		t.Errorf("Expected start_date to start with '2026-04-05', got %s", updated.StartDate)
	}
}

func TestLeaveHandler_Update_NotFound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createLeaveHandler(t, tdb)

	body := models.UserLeavePeriodRequest{
		StartDate: "2026-04-01",
		EndDate:   "2026-04-10",
		Reason:    "Not found",
	}
	req := testutils.CreateJSONRequest(t, "PUT", "/api/users/1/leave/99999", body)
	req.SetPathValue("userId", "1")
	req.SetPathValue("leaveId", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)
	rr.AssertStatusCode(http.StatusNotFound)
}

// --- Delete ---

func TestLeaveHandler_Delete_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createLeaveHandler(t, tdb)

	// Create leave period
	createBody := models.UserLeavePeriodRequest{
		StartDate: "2026-06-01",
		EndDate:   "2026-06-10",
		Reason:    "To delete",
	}
	createReq := testutils.CreateJSONRequest(t, "POST", "/api/users/1/leave", createBody)
	createReq.SetPathValue("userId", "1")
	createRR := testutils.ExecuteAuthenticatedRequest(t, handler.Create, createReq, nil)
	createRR.AssertStatusCode(http.StatusCreated)
	var created models.UserLeavePeriod
	createRR.AssertJSONResponse(&created)

	// Delete it
	req := testutils.CreateJSONRequest(t, "DELETE", "/api/users/1/leave/"+testutils.IntToString(created.ID), nil)
	req.SetPathValue("userId", "1")
	req.SetPathValue("leaveId", testutils.IntToString(created.ID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, req, nil)
	rr.AssertStatusCode(http.StatusNoContent)
}

func TestLeaveHandler_Delete_NotFound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createLeaveHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/users/1/leave/99999", nil)
	req.SetPathValue("userId", "1")
	req.SetPathValue("leaveId", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, req, nil)
	rr.AssertStatusCode(http.StatusNotFound)
}
