//go:build test

package handlers

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// Covers the completed_since query param on GET /api/items, which caps the
// indefinitely-growing "done" list on personal views: items in a completed
// status are pruned to those finished on/after the cutoff, while everything
// else passes through unaffected.
func TestItemHandler_GetAll_CompletedSinceFilter(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)

	openStatus := data.StatusID

	// A completed status under its own (is_completed = true) category.
	var doneCatID, doneStatus64 int64
	if err := tdb.QueryRow(`
		INSERT INTO status_categories (name, color, is_completed) VALUES ('Done CSF', '#22c55e', true) RETURNING id
	`).Scan(&doneCatID); err != nil {
		t.Fatalf("create done category: %v", err)
	}
	if err := tdb.QueryRow(`
		INSERT INTO statuses (name, category_id) VALUES ('Done CSF', ?) RETURNING id
	`, doneCatID).Scan(&doneStatus64); err != nil {
		t.Fatalf("create done status: %v", err)
	}
	doneStatus := int(doneStatus64)

	createItem := func(title string, statusID int) int {
		t.Helper()
		// Create-time status overrides are validated against the workflow
		// (ValidateCreateStatusOverride) and the Done status above is
		// deliberately outside any workflow — so create on the default status
		// and move via direct UPDATE, matching how the test pins history rows.
		item := models.Item{WorkspaceID: data.WorkspaceID, Title: title}
		req := testutils.CreateJSONRequest(t, "POST", "/api/items", item)
		rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
		rr.AssertStatusCode(http.StatusCreated)
		var created models.Item
		rr.AssertJSONResponse(&created)
		if created.ID == 0 {
			t.Fatalf("created item %q has no ID", title)
		}
		if _, err := tdb.Exec(`UPDATE items SET status_id = ? WHERE id = ?`, statusID, created.ID); err != nil {
			t.Fatalf("set status of %q: %v", title, err)
		}
		return created.ID
	}

	// Pin when the item entered its (done) status — completion time is derived
	// from item_history, so clear any create-time rows first.
	setDoneAt := func(itemID int, at time.Time) {
		t.Helper()
		if _, err := tdb.Exec(`DELETE FROM item_history WHERE item_id = ? AND field_name = 'status_id'`, itemID); err != nil {
			t.Fatalf("clear history: %v", err)
		}
		if _, err := tdb.Exec(`
			INSERT INTO item_history (item_id, user_id, changed_at, field_name, old_value, new_value)
			VALUES (?, ?, ?, 'status_id', ?, ?)
		`, itemID, data.UserID, at, testutils.IntToString(openStatus), testutils.IntToString(doneStatus)); err != nil {
			t.Fatalf("insert history: %v", err)
		}
	}

	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	handler.now = func() time.Time { return now }
	openItem := createItem("Open item CSF", openStatus)
	recentDone := createItem("Recent done CSF", doneStatus)
	oldDone := createItem("Old done CSF", doneStatus)
	setDoneAt(recentDone, now.AddDate(0, 0, -2))
	setDoneAt(oldDone, now.AddDate(0, 0, -30))

	list := func(query string) []models.Item {
		t.Helper()
		req := testutils.CreateJSONRequest(t, "GET",
			"/api/items?workspace_id="+testutils.IntToString(data.WorkspaceID)+query, nil)
		rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)
		rr.AssertStatusCode(http.StatusOK)
		var response models.PaginatedItemsResponse
		rr.AssertJSONResponse(&response)
		return response.Items
	}

	has := func(items []models.Item, id int) bool {
		for _, it := range items {
			if it.ID == id {
				return true
			}
		}
		return false
	}

	t.Run("7-day cutoff keeps open + recent done, drops old done", func(t *testing.T) {
		cutoff := now.AddDate(0, 0, -7).Format("2006-01-02")
		items := list("&completed_since=" + cutoff)
		if !has(items, openItem) {
			t.Error("open item must always pass the completed_since filter")
		}
		if !has(items, recentDone) {
			t.Error("recently-done item should be within the 7-day window")
		}
		if has(items, oldDone) {
			t.Error("long-ago-done item should be excluded by the 7-day window")
		}
	})

	t.Run("absent filter returns the old done item too", func(t *testing.T) {
		items := list("")
		if !has(items, oldDone) {
			t.Error("old done item should appear when completed_since is absent")
		}
	})

	t.Run("activity cutoff keeps unfinished and recently active completed items", func(t *testing.T) {
		oldActivity := now.AddDate(0, 0, -30)
		recentActivity := now.AddDate(0, 0, -2)
		if _, err := tdb.Exec(`UPDATE items SET updated_at = ?, last_active_at = ? WHERE id IN (?, ?)`, oldActivity, oldActivity, openItem, recentDone); err != nil {
			t.Fatalf("set old activity: %v", err)
		}
		if _, err := tdb.Exec(`UPDATE items SET updated_at = ?, last_active_at = ? WHERE id = ?`, recentActivity, recentActivity, oldDone); err != nil {
			t.Fatalf("set recent activity: %v", err)
		}

		items := list("&completed_activity_days=7")
		if !has(items, openItem) {
			t.Error("unfinished item must pass regardless of activity age")
		}
		if has(items, recentDone) {
			t.Error("completed item with old activity should be excluded")
		}
		if !has(items, oldDone) {
			t.Error("completed item with recent activity should be included")
		}
	})
}

func TestItemHandler_GetAll_CompletedActivityDaysUsesServerClock(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)
	serverNow := time.Date(2026, time.August, 26, 15, 30, 0, 0, time.UTC)
	handler.now = func() time.Time { return serverNow }
	cutoff := serverNow.AddDate(0, 0, -7)

	// Exact activity instants and the standalone completed status cannot be
	// created through the item API, so this boundary fixture pins them directly.
	var doneCategoryID, doneStatusID int
	if err := tdb.QueryRow(`
		INSERT INTO status_categories (name, color, is_completed)
		VALUES ('Done retention boundary', '#22c55e', true)
		RETURNING id
	`).Scan(&doneCategoryID); err != nil {
		t.Fatalf("create completed status category: %v", err)
	}
	if err := tdb.QueryRow(`
		INSERT INTO statuses (name, category_id)
		VALUES ('Done retention boundary', ?)
		RETURNING id
	`, doneCategoryID).Scan(&doneStatusID); err != nil {
		t.Fatalf("create completed status: %v", err)
	}

	createItem := func(title string, statusID int, activityAt time.Time) int {
		t.Helper()
		req := testutils.CreateJSONRequest(t, http.MethodPost, "/api/items", models.Item{
			WorkspaceID: data.WorkspaceID,
			Title:       title,
		})
		rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
		rr.AssertStatusCode(http.StatusCreated)
		var created models.Item
		rr.AssertJSONResponse(&created)
		if _, err := tdb.Exec(`
			UPDATE items
			SET status_id = ?, updated_at = ?, last_active_at = ?
			WHERE id = ?
		`, statusID, activityAt, activityAt, created.ID); err != nil {
			t.Fatalf("pin activity for %q: %v", title, err)
		}
		return created.ID
	}

	openBefore := createItem("Open before cutoff", data.StatusID, cutoff.Add(-time.Second))
	doneBefore := createItem("Done before cutoff", doneStatusID, cutoff.Add(-time.Second))
	doneExact := createItem("Done at cutoff", doneStatusID, cutoff)
	doneAfter := createItem("Done after cutoff", doneStatusID, cutoff.Add(time.Second))

	list := func(values url.Values) map[int]bool {
		t.Helper()
		values.Set("workspace_id", testutils.IntToString(data.WorkspaceID))
		req := testutils.CreateJSONRequest(t, http.MethodGet, "/api/items?"+values.Encode(), nil)
		rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)
		rr.AssertStatusCode(http.StatusOK)
		var response models.PaginatedItemsResponse
		rr.AssertJSONResponse(&response)
		ids := make(map[int]bool, len(response.Items))
		for _, item := range response.Items {
			ids[item.ID] = true
		}
		return ids
	}

	tests := []struct {
		name   string
		values url.Values
	}{
		{
			name:   "server derives exact cutoff",
			values: url.Values{"completed_activity_days": {"7"}},
		},
		{
			name: "client timestamp cannot move cutoff",
			values: url.Values{
				"completed_activity_days":  {"7"},
				"completed_activity_since": {"2099-01-01T00:00:00Z"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids := list(tt.values)
			if !ids[openBefore] {
				t.Error("unfinished item before cutoff must remain visible")
			}
			if ids[doneBefore] {
				t.Error("completed item just before cutoff must be hidden")
			}
			if !ids[doneExact] {
				t.Error("completed item at cutoff must remain visible")
			}
			if !ids[doneAfter] {
				t.Error("completed item just after cutoff must remain visible")
			}
		})
	}

	for _, value := range []string{"0", "3651", "seven"} {
		t.Run("rejects invalid day window "+value, func(t *testing.T) {
			req := testutils.CreateJSONRequest(t, http.MethodGet,
				"/api/items?workspace_id="+testutils.IntToString(data.WorkspaceID)+"&completed_activity_days="+value, nil)
			rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)
			rr.AssertStatusCode(http.StatusBadRequest)
			var response struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}
			if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if response.Code != "VALIDATION_FAILED" || response.Error != "completed_activity_days must be between 1 and 3650" {
				t.Fatalf("error = %#v, want completed-activity day validation", response)
			}
		})
	}
}
