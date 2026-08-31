//go:build test

package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/repository"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

type workspaceStatsQueryFailureDB struct {
	database.Database
	queryFragment    string
	queryRowFragment string
}

func (db *workspaceStatsQueryFailureDB) Query(query string, args ...any) (*sql.Rows, error) {
	if db.queryFragment != "" && strings.Contains(query, db.queryFragment) {
		return nil, errors.New("forced workspace stats query failure")
	}
	return db.Database.Query(query, args...)
}

func (db *workspaceStatsQueryFailureDB) QueryRow(query string, args ...any) *sql.Row {
	if db.queryRowFragment != "" && strings.Contains(query, db.queryRowFragment) {
		return db.Database.QueryRow("SELECT missing_column FROM missing_workspace_stats_table")
	}
	return db.Database.QueryRow(query, args...)
}

// --- GetStats ---------------------------------------------------------------

func TestWorkspaceHandler_GetStats_EmptyWorkspace(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces/1/stats", nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetStats, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	var stats WorkspaceStats
	rr.AssertJSONResponse(&stats)

	// TotalCollections always includes the implicit default collection (+1).
	if stats.TotalCollections != 1 {
		t.Errorf("Expected TotalCollections=1 for empty workspace, got %d", stats.TotalCollections)
	}
	if stats.TotalItems != 0 {
		t.Errorf("Expected TotalItems=0, got %d", stats.TotalItems)
	}
	if stats.ItemsByStatusCategory == nil {
		t.Error("Expected non-nil ItemsByStatusCategory map")
	}
	if stats.AssignmentDistribution == nil {
		t.Error("Expected non-nil AssignmentDistribution slice")
	}
	if stats.ProjectStatistics == nil {
		t.Error("Expected non-nil ProjectStatistics slice")
	}
	if stats.PriorityBreakdown == nil {
		t.Error("Expected non-nil PriorityBreakdown map")
	}
	if stats.MilestoneProgress == nil {
		t.Error("Expected non-nil MilestoneProgress slice")
	}
}

func TestWorkspaceHandler_GetStats_CountsItems(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	// Seed 3 items in the default workspace through the production create path.
	f := factory.NewTestFactory(tdb.GetDatabase())
	for i := 0; i < 3; i++ {
		_, err := f.CreateItem(factory.CreateItemOpts{
			WorkspaceID: data.WorkspaceID,
			Title:       "Item",
			StatusID:    &data.StatusID,
			PriorityID:  &data.PriorityID,
			CreatorID:   &data.UserID,
			AssigneeID:  &data.UserID,
		})
		if err != nil {
			t.Fatalf("seed item %d: %v", i, err)
		}
	}

	req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces/1/stats", nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetStats, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	var stats WorkspaceStats
	rr.AssertJSONResponse(&stats)

	if stats.TotalItems != 3 {
		t.Errorf("Expected TotalItems=3, got %d", stats.TotalItems)
	}
	// Assignment distribution should show the one assignee with 3 items.
	if len(stats.AssignmentDistribution) == 0 {
		t.Error("Expected at least one entry in AssignmentDistribution")
	}
	// Priority breakdown should have at least one entry.
	if len(stats.PriorityBreakdown) == 0 {
		t.Error("Expected at least one entry in PriorityBreakdown")
	}
}

func TestWorkspaceHandler_GetStats_ReturnsInternalErrorForAggregationFailure(t *testing.T) {
	tests := []struct {
		name             string
		queryFragment    string
		queryRowFragment string
	}{
		{name: "collection aggregation", queryRowFragment: "FROM collections"},
		{name: "item aggregation", queryFragment: "GROUP BY sc.name"},
		{name: "milestone aggregation", queryFragment: "JOIN item_milestones im"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tdb := testutils.CreateTestDB(t, true)
			t.Cleanup(func() { tdb.Close() })
			tdb.SeedTestData(t)

			permissionService, activityTracker, _ := createTestServices(t, *tdb)
			keyCache := NewWorkspaceKeyCache(repository.NewWorkspaceRepository(tdb.GetDatabase()))
			handler := NewWorkspaceHandler(&workspaceStatsQueryFailureDB{
				Database:         tdb.GetDatabase(),
				queryFragment:    tc.queryFragment,
				queryRowFragment: tc.queryRowFragment,
			}, permissionService, activityTracker, keyCache)

			req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces/1/stats", nil)
			req.SetPathValue("id", "1")
			rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetStats, req, nil)

			rr.AssertStatusCode(http.StatusInternalServerError)
			if !strings.Contains(rr.Body.String(), `"code":"INTERNAL_ERROR"`) {
				t.Fatalf("response = %s, want INTERNAL_ERROR", rr.Body.String())
			}
		})
	}
}

func TestWorkspaceHandler_GetStats_WithValidVQLFilter(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	// Seed two items with different titles so the filter can distinguish them.
	f := factory.NewTestFactory(tdb.GetDatabase())
	for _, title := range []string{"Match", "Other"} {
		if _, err := f.CreateItem(factory.CreateItemOpts{
			WorkspaceID: data.WorkspaceID,
			Title:       title,
			StatusID:    &data.StatusID,
			PriorityID:  &data.PriorityID,
			CreatorID:   &data.UserID,
		}); err != nil {
			t.Fatalf("seed item %s: %v", title, err)
		}
	}

	req := testutils.CreateJSONRequest(t, "GET", `/api/workspaces/1/stats?vql=title = "Match"`, nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetStats, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	var stats WorkspaceStats
	rr.AssertJSONResponse(&stats)

	if stats.TotalItems != 1 {
		t.Errorf("Expected TotalItems=1 after VQL filter, got %d", stats.TotalItems)
	}
}

func TestWorkspaceHandler_GetStats_NameBasedVQLFilterPopulatesMilestoneProgress(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)
	f := factory.NewTestFactory(tdb.GetDatabase())

	matchingItemID, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: data.WorkspaceID,
		Title:       "Matching item",
		StatusID:    &data.StatusID,
		PriorityID:  &data.PriorityID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("seed matching item: %v", err)
	}

	var inProgressStatusID int
	if err := tdb.QueryRow(`SELECT id FROM statuses WHERE name = 'In Progress'`).Scan(&inProgressStatusID); err != nil {
		t.Fatalf("load in-progress status: %v", err)
	}
	if _, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: data.WorkspaceID,
		Title:       "Non-matching item",
		StatusID:    &inProgressStatusID,
		PriorityID:  &data.PriorityID,
		CreatorID:   &data.UserID,
	}); err != nil {
		t.Fatalf("seed non-matching item: %v", err)
	}

	milestoneID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO milestones (name, status, is_global, workspace_id)
		VALUES ('Stats milestone', 'planning', false, ?)
	`, data.WorkspaceID)
	if _, err := tdb.Exec(`
		INSERT INTO item_milestones (item_id, milestone_id)
		VALUES (?, ?)
	`, matchingItemID, milestoneID); err != nil {
		t.Fatalf("attach matching item to milestone: %v", err)
	}

	req := testutils.CreateJSONRequest(t, "GET", `/api/workspaces/1/stats?vql=status = "Open"`, nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetStats, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	var stats WorkspaceStats
	rr.AssertJSONResponse(&stats)
	if stats.TotalItems != 1 {
		t.Fatalf("filtered total items = %d, want 1", stats.TotalItems)
	}
	if len(stats.MilestoneProgress) != 1 {
		t.Fatalf("milestone progress entries = %d, want 1", len(stats.MilestoneProgress))
	}
	if stats.MilestoneProgress[0].MilestoneID != milestoneID || stats.MilestoneProgress[0].TotalItems != 1 {
		t.Fatalf("milestone progress = %+v, want milestone %d with one item", stats.MilestoneProgress[0], milestoneID)
	}
}

func TestWorkspaceHandler_GetStats_NameBasedVQLFiltersCountMatchingItems(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)
	f := factory.NewTestFactory(tdb.GetDatabase())

	var inProgressStatusID, doneStatusID int
	if err := tdb.QueryRow(`SELECT id FROM statuses WHERE name = 'In Progress'`).Scan(&inProgressStatusID); err != nil {
		t.Fatalf("load in-progress status: %v", err)
	}
	if err := tdb.QueryRow(`SELECT id FROM statuses WHERE name = 'Done'`).Scan(&doneStatusID); err != nil {
		t.Fatalf("load done status: %v", err)
	}
	var highPriorityID, lowPriorityID int
	if err := tdb.QueryRow(`SELECT id FROM priorities WHERE name = 'High'`).Scan(&highPriorityID); err != nil {
		t.Fatalf("load high priority: %v", err)
	}
	if err := tdb.QueryRow(`SELECT id FROM priorities WHERE name = 'Low'`).Scan(&lowPriorityID); err != nil {
		t.Fatalf("load low priority: %v", err)
	}
	var taskTypeID, bugTypeID int
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE name = 'Task'`).Scan(&taskTypeID); err != nil {
		t.Fatalf("load task type: %v", err)
	}
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE name = 'Bug'`).Scan(&bugTypeID); err != nil {
		t.Fatalf("load bug type: %v", err)
	}

	iterationID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO iterations (name, description, start_date, end_date, status, is_global, workspace_id)
		VALUES ('Stats iteration', '', '2026-01-01', '2026-12-31', 'active', false, ?)
	`, data.WorkspaceID)
	projectID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO time_projects (name, status, active)
		VALUES ('Stats project', 'Active', true)
	`)

	createdAt := time.Now().UTC()
	createItem := func(title string, statusID, priorityID, itemTypeID int, iterationID, projectID *int) int {
		t.Helper()
		id, err := f.CreateItem(factory.CreateItemOpts{
			WorkspaceID: data.WorkspaceID,
			Title:       title,
			StatusID:    &statusID,
			PriorityID:  &priorityID,
			ItemTypeID:  &itemTypeID,
			CreatorID:   &data.UserID,
			IterationID: iterationID,
			ProjectID:   projectID,
			CreatedAt:   &createdAt,
			UpdatedAt:   &createdAt,
		})
		if err != nil {
			t.Fatalf("seed %s item: %v", title, err)
		}
		return id
	}

	createItem("Open task", data.StatusID, data.PriorityID, taskTypeID, nil, nil)
	createItem("High priority bug", inProgressStatusID, highPriorityID, bugTypeID, nil, nil)
	completedItemID := createItem("Completed task", doneStatusID, lowPriorityID, taskTypeID, nil, nil)
	createItem("Iteration task", inProgressStatusID, data.PriorityID, taskTypeID, &iterationID, nil)
	createItem("Project task", inProgressStatusID, data.PriorityID, taskTypeID, nil, &projectID)
	if _, err := tdb.Exec(`
		INSERT INTO item_history (item_id, user_id, changed_at, field_name, old_value, new_value)
		VALUES (?, ?, ?, 'status_id', ?, ?)
	`, completedItemID, data.UserID, createdAt, data.StatusID, doneStatusID); err != nil {
		t.Fatalf("insert completion history: %v", err)
	}

	cases := []struct {
		name  string
		query string
		want  int
	}{
		{name: "status", query: `status = "Open"`, want: 1},
		{name: "priority", query: `priority = "High"`, want: 1},
		{name: "type", query: `type = "Bug"`, want: 1},
		{name: "iteration", query: `iterationname = "Stats iteration"`, want: 1},
		{name: "project", query: `projectname = "Stats project"`, want: 1},
		{name: "completed at", query: `completed_at IS NOT NULL`, want: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces/1/stats?vql="+tc.query, nil)
			req.SetPathValue("id", "1")
			rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetStats, req, nil)
			rr.AssertStatusCode(http.StatusOK)

			var stats WorkspaceStats
			rr.AssertJSONResponse(&stats)
			if stats.TotalItems != tc.want {
				t.Fatalf("%s filtered total items = %d, want %d", tc.name, stats.TotalItems, tc.want)
			}
		})
	}
}

func TestWorkspaceHandler_GetStats_InvalidVQLQuery(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces/1/stats?vql=INVALID%20SYNTAX!!!", nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetStats, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
	if !contains(rr.Body.String(), "VQL query error") {
		t.Errorf("Expected body to mention VQL query error, got %q", rr.Body.String())
	}
}

func TestWorkspaceHandler_GetStats_InvalidCollectionID(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces/1/stats?collection_id=not-a-number", nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetStats, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
}

func TestWorkspaceHandler_GetStats_NonExistentCollection(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces/1/stats?collection_id=99999", nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetStats, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}

func TestWorkspaceHandler_GetStats_CollectionFromOtherWorkspace(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	// Create a second workspace + a collection scoped to it.
	_, err := tdb.Exec(`
		INSERT INTO workspaces (id, name, key, description, active) VALUES (2, 'Other', 'OTHER', '', TRUE)
	`)
	if err != nil {
		t.Fatalf("seed other workspace: %v", err)
	}
	collID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO collections (name, workspace_id, ql_query, created_by)
		VALUES ('OtherCol', 2, '', 1)
	`)

	req := testutils.CreateJSONRequest(t, "GET",
		"/api/workspaces/1/stats?collection_id="+testutils.IntToString(collID), nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetStats, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
}

func TestWorkspaceHandler_GetStats_CollectionReusesItsQuery(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	// Create a workspace-scoped collection with a VQL query that matches one item.
	collID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO collections (name, workspace_id, ql_query, created_by)
		VALUES ('Matcher', ?, 'title = "Keep"', ?)
	`, data.WorkspaceID, data.UserID)

	// Seed two items; only one should match the collection's query.
	f := factory.NewTestFactory(tdb.GetDatabase())
	for _, title := range []string{"Keep", "Drop"} {
		if _, err := f.CreateItem(factory.CreateItemOpts{
			WorkspaceID: data.WorkspaceID,
			Title:       title,
			StatusID:    &data.StatusID,
			PriorityID:  &data.PriorityID,
			CreatorID:   &data.UserID,
		}); err != nil {
			t.Fatalf("seed %s item: %v", title, err)
		}
	}

	req := testutils.CreateJSONRequest(t, "GET",
		"/api/workspaces/1/stats?collection_id="+testutils.IntToString(collID), nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetStats, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	var stats WorkspaceStats
	rr.AssertJSONResponse(&stats)

	if stats.TotalItems != 1 {
		t.Errorf("Expected TotalItems=1 (filtered by collection query), got %d", stats.TotalItems)
	}
}

func TestWorkspaceHandler_GetStats_WorkspaceKeyResolves(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	// Look up by workspace key ('TEST' is seeded by SeedTestData).
	req := testutils.CreateJSONRequest(t, "GET", "/api/workspaces/TEST/stats", nil)
	req.SetPathValue("id", "TEST")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetStats, req, nil)

	rr.AssertStatusCode(http.StatusOK)
}

// contains is a tiny substring helper to avoid importing strings here.
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
