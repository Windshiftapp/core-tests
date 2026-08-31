//go:build test

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

// newItemHandlerWithSeed constructs an ItemHandler + seeds the default test
// data. Returns the handler and the TestDataSet so callers can reference IDs.
func newItemHandlerWithSeed(t *testing.T, tdb *testutils.TestDB) (*ItemHandler, testutils.TestDataSet) {
	t.Helper()
	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)
	return handler, data
}

// seedScheduleableItem creates an item through the production path in the
// default workspace and returns its ID. The workspace_item_number is
// production-assigned, so repeated seeds never collide. An empty description
// is stored exactly as production stores it for description-less items.
func seedScheduleableItem(t *testing.T, tdb *testutils.TestDB, data testutils.TestDataSet, _ int) int {
	t.Helper()
	f := factory.NewTestFactory(tdb.GetDatabase())
	itemID, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: data.WorkspaceID,
		Title:       "Schedulable",
		StatusID:    &data.StatusID,
		PriorityID:  &data.PriorityID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("seed item: %v", err)
	}
	return itemID
}

// --- ScheduleItem -----------------------------------------------------------

func TestItemHandler_ScheduleItem_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler, data := newItemHandlerWithSeed(t, tdb)
	itemID := seedScheduleableItem(t, tdb, data, 1)

	body := ScheduleCalendarRequest{
		UserID:          data.UserID,
		WorkspaceID:     data.WorkspaceID,
		ScheduledDate:   "2026-04-20",
		ScheduledTime:   "09:00",
		DurationMinutes: 45,
		Notes:           "Morning focus block",
	}
	req := testutils.CreateJSONRequest(t, "POST", fmt.Sprintf("/api/items/%d/schedule", itemID), body)
	req.SetPathValue("id", testutils.IntToString(itemID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.ScheduleItem, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	// Verify the DB row picked up the calendar entry.
	var stored string
	if err := tdb.QueryRow("SELECT calendar_data FROM items WHERE id = ?", itemID).Scan(&stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	var entries []models.CalendarScheduleEntry
	if err := json.Unmarshal([]byte(stored), &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("Expected 1 calendar entry, got %d", len(entries))
	}
	if entries[0].ScheduledDate != "2026-04-20" || entries[0].Notes != "Morning focus block" {
		t.Errorf("Stored entry not as expected: %+v", entries[0])
	}
}

func TestItemHandler_ScheduleItem_ReplacesExistingForSameUser(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler, data := newItemHandlerWithSeed(t, tdb)
	itemID := seedScheduleableItem(t, tdb, data, 1)

	// Pre-populate the item with a schedule for user 1 on a different date.
	existing := []models.CalendarScheduleEntry{{
		UserID:        data.UserID,
		WorkspaceID:   data.WorkspaceID,
		ScheduledDate: "2025-01-01",
		CreatedAt:     "2025-01-01T00:00:00Z",
	}}
	blob, _ := json.Marshal(existing)
	if _, err := tdb.Exec("UPDATE items SET calendar_data = ? WHERE id = ?", string(blob), itemID); err != nil {
		t.Fatalf("seed existing schedule: %v", err)
	}

	body := ScheduleCalendarRequest{
		UserID:        data.UserID,
		WorkspaceID:   data.WorkspaceID,
		ScheduledDate: "2026-05-01",
	}
	req := testutils.CreateJSONRequest(t, "POST", fmt.Sprintf("/api/items/%d/schedule", itemID), body)
	req.SetPathValue("id", testutils.IntToString(itemID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.ScheduleItem, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	var stored string
	if err := tdb.QueryRow("SELECT calendar_data FROM items WHERE id = ?", itemID).Scan(&stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	var entries []models.CalendarScheduleEntry
	if err := json.Unmarshal([]byte(stored), &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("Expected old entry to be replaced (1 entry), got %d", len(entries))
	}
	if entries[0].ScheduledDate != "2026-05-01" {
		t.Errorf("Expected latest date 2026-05-01, got %q", entries[0].ScheduledDate)
	}
}

func TestItemHandler_ScheduleItem_NonExistentItem(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler, data := newItemHandlerWithSeed(t, tdb)

	body := ScheduleCalendarRequest{
		UserID:        data.UserID,
		WorkspaceID:   data.WorkspaceID,
		ScheduledDate: "2026-04-20",
	}
	req := testutils.CreateJSONRequest(t, "POST", "/api/items/99999/schedule", body)
	req.SetPathValue("id", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.ScheduleItem, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}

func TestItemHandler_ScheduleItem_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler, data := newItemHandlerWithSeed(t, tdb)
	itemID := seedScheduleableItem(t, tdb, data, 1)

	body := ScheduleCalendarRequest{UserID: data.UserID, WorkspaceID: data.WorkspaceID, ScheduledDate: "2026-04-20"}
	req := testutils.CreateJSONRequest(t, "POST", fmt.Sprintf("/api/items/%d/schedule", itemID), body)
	req.SetPathValue("id", testutils.IntToString(itemID))
	rr := testutils.ExecuteRequest(t, handler.ScheduleItem, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

// --- UnscheduleItem ---------------------------------------------------------

func TestItemHandler_UnscheduleItem_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler, data := newItemHandlerWithSeed(t, tdb)
	itemID := seedScheduleableItem(t, tdb, data, 1)

	// Seed a schedule to unschedule.
	existing := []models.CalendarScheduleEntry{{
		UserID:        data.UserID,
		WorkspaceID:   data.WorkspaceID,
		ScheduledDate: "2026-04-20",
		CreatedAt:     "2026-04-19T00:00:00Z",
	}}
	blob, _ := json.Marshal(existing)
	if _, err := tdb.Exec("UPDATE items SET calendar_data = ? WHERE id = ?", string(blob), itemID); err != nil {
		t.Fatalf("seed schedule: %v", err)
	}

	req := testutils.CreateJSONRequest(t, "DELETE",
		fmt.Sprintf("/api/items/%d/schedule?user_id=%d", itemID, data.UserID), nil)
	req.SetPathValue("id", testutils.IntToString(itemID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UnscheduleItem, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	var stored string
	if err := tdb.QueryRow("SELECT calendar_data FROM items WHERE id = ?", itemID).Scan(&stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	// The handler writes "[]" (empty JSON array) once the only entry is removed.
	if stored != "[]" {
		t.Errorf("Expected empty calendar_data after unschedule, got %q", stored)
	}
}

func TestItemHandler_UnscheduleItem_MissingUserIDParam(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler, data := newItemHandlerWithSeed(t, tdb)
	itemID := seedScheduleableItem(t, tdb, data, 1)

	req := testutils.CreateJSONRequest(t, "DELETE", fmt.Sprintf("/api/items/%d/schedule", itemID), nil)
	req.SetPathValue("id", testutils.IntToString(itemID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UnscheduleItem, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
}

func TestItemHandler_UnscheduleItem_InvalidUserIDParam(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler, data := newItemHandlerWithSeed(t, tdb)
	itemID := seedScheduleableItem(t, tdb, data, 1)

	req := testutils.CreateJSONRequest(t, "DELETE",
		fmt.Sprintf("/api/items/%d/schedule?user_id=not-an-int", itemID), nil)
	req.SetPathValue("id", testutils.IntToString(itemID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UnscheduleItem, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
}

func TestItemHandler_UnscheduleItem_ForbiddenForDifferentUser(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler, data := newItemHandlerWithSeed(t, tdb)
	itemID := seedScheduleableItem(t, tdb, data, 1)

	// user_id=2 in the query, but the authenticated user is id=1 — the handler
	// must refuse to let one user modify another user's schedule.
	req := testutils.CreateJSONRequest(t, "DELETE", fmt.Sprintf("/api/items/%d/schedule?user_id=2", itemID), nil)
	req.SetPathValue("id", testutils.IntToString(itemID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UnscheduleItem, req, nil)

	rr.AssertStatusCode(http.StatusForbidden)
}

func TestItemHandler_UnscheduleItem_NoScheduleFound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler, data := newItemHandlerWithSeed(t, tdb)
	itemID := seedScheduleableItem(t, tdb, data, 1)
	// No calendar_data seeded — handler should 404 because there's nothing to remove.

	req := testutils.CreateJSONRequest(t, "DELETE",
		fmt.Sprintf("/api/items/%d/schedule?user_id=%d", itemID, data.UserID), nil)
	req.SetPathValue("id", testutils.IntToString(itemID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UnscheduleItem, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}

// --- GetScheduledItems ------------------------------------------------------

func TestItemHandler_GetScheduledItems_ReturnsAuthedUsersSchedules(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler, data := newItemHandlerWithSeed(t, tdb)
	itemID := seedScheduleableItem(t, tdb, data, 1)

	entries := []models.CalendarScheduleEntry{{
		UserID:        data.UserID,
		WorkspaceID:   data.WorkspaceID,
		ScheduledDate: "2026-04-20",
		ScheduledTime: "10:00",
		CreatedAt:     "2026-04-19T00:00:00Z",
	}}
	blob, _ := json.Marshal(entries)
	if _, err := tdb.Exec("UPDATE items SET calendar_data = ? WHERE id = ?", string(blob), itemID); err != nil {
		t.Fatalf("seed schedule: %v", err)
	}

	req := testutils.CreateJSONRequest(t, "GET", "/api/items/scheduled", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetScheduledItems, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	var result map[string][]map[string]interface{}
	rr.AssertJSONResponse(&result)
	entriesForDate, ok := result["2026-04-20"]
	if !ok || len(entriesForDate) != 1 {
		t.Fatalf("Expected one scheduled item on 2026-04-20, got %+v", result)
	}
}

func TestItemHandler_GetScheduledItems_HonoursDateRange(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler, data := newItemHandlerWithSeed(t, tdb)
	itemID := seedScheduleableItem(t, tdb, data, 1)

	entries := []models.CalendarScheduleEntry{
		{UserID: data.UserID, WorkspaceID: data.WorkspaceID, ScheduledDate: "2026-01-10", CreatedAt: "x"},
		{UserID: data.UserID, WorkspaceID: data.WorkspaceID, ScheduledDate: "2026-04-20", CreatedAt: "x"},
	}
	blob, _ := json.Marshal(entries)
	if _, err := tdb.Exec("UPDATE items SET calendar_data = ? WHERE id = ?", string(blob), itemID); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := testutils.CreateJSONRequest(t, "GET", "/api/items/scheduled?start_date=2026-04-01&end_date=2026-04-30", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetScheduledItems, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	var result map[string][]map[string]interface{}
	rr.AssertJSONResponse(&result)

	if _, earlyIncluded := result["2026-01-10"]; earlyIncluded {
		t.Error("Schedule outside the date range should have been filtered out")
	}
	if _, lateIncluded := result["2026-04-20"]; !lateIncluded {
		t.Error("Schedule inside the date range was missing from the result")
	}
}

func TestItemHandler_GetScheduledItems_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler, _ := newItemHandlerWithSeed(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/items/scheduled", nil)
	rr := testutils.ExecuteRequest(t, handler.GetScheduledItems, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}
