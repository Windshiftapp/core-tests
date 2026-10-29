package tests

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

type temporalWorklog struct {
	ID              int    `json:"id"`
	ProjectID       int    `json:"project_id"`
	ItemID          *int   `json:"item_id"`
	Description     string `json:"description"`
	Date            int64  `json:"date"`
	StartTime       int64  `json:"start_time"`
	EndTime         int64  `json:"end_time"`
	DurationMinutes int    `json:"duration_minutes"`
}

func patchTemporalWorklog(t *testing.T, server *TestServer, worklogID int, patch map[string]any) temporalWorklog {
	t.Helper()
	response := MakeBearerRequest(t, server, http.MethodPatch,
		fmt.Sprintf("/rest/api/v2/time/worklogs/%d", worklogID), patch)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)
	var worklog temporalWorklog
	DecodeJSON(t, response, &worklog)
	return worklog
}

func assertWorklogInterval(t *testing.T, worklog temporalWorklog, start, end time.Time, duration int) {
	t.Helper()
	if worklog.StartTime != start.Unix() || worklog.EndTime != end.Unix() || worklog.DurationMinutes != duration {
		t.Fatalf("worklog interval = %s-%s (%d minutes), want %s-%s (%d minutes)",
			time.Unix(worklog.StartTime, 0).UTC().Format(time.RFC3339),
			time.Unix(worklog.EndTime, 0).UTC().Format(time.RFC3339),
			worklog.DurationMinutes,
			start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339), duration)
	}
}

// TestTimeWorklogPatchClearsItemLinkWithExplicitNull is the WI-1721 contract:
// an explicit null clears the link under merge-PATCH, an omitted key preserves
// it, and the cleared state survives a re-fetch.
func TestTimeWorklogPatchClearsItemLinkWithExplicitNull(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	projectID := createTimeProjectFixture(t, server, "Clear item link")
	workspaceID, _ := CreateTestWorkspace(t, server, "Clear link WS", shortKey("CLNK"))
	itemID := CreateTestItem(t, server, workspaceID, "Linked work item")

	create := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/time/worklogs", map[string]any{
		"project_id":  projectID,
		"item_id":     itemID,
		"description": "linked work",
		"date":        "2026-09-05",
		"duration":    "1h",
	})
	defer create.Body.Close()
	AssertStatusCode(t, create, http.StatusCreated)
	var worklog temporalWorklog
	DecodeJSON(t, create, &worklog)
	if worklog.ItemID == nil || *worklog.ItemID != itemID {
		t.Fatalf("created worklog item = %v, want %d", worklog.ItemID, itemID)
	}

	// A metadata-only edit that omits the link preserves it.
	kept := patchTemporalWorklog(t, server, worklog.ID, map[string]any{"description": "still linked"})
	if kept.ItemID == nil || *kept.ItemID != itemID {
		t.Fatalf("metadata-only patch item = %v, want preserved %d", kept.ItemID, itemID)
	}

	// An explicit null clears the link.
	cleared := patchTemporalWorklog(t, server, worklog.ID, map[string]any{"item_id": nil, "description": "unlinked"})
	if cleared.ItemID != nil {
		t.Fatalf("cleared worklog item = %d, want nil", *cleared.ItemID)
	}

	// Re-fetch proves the unlinked state persisted.
	refetch := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/time/worklogs/%d", worklog.ID), nil)
	defer refetch.Body.Close()
	AssertStatusCode(t, refetch, http.StatusOK)
	var reopened temporalWorklog
	DecodeJSON(t, refetch, &reopened)
	if reopened.ItemID != nil {
		t.Fatalf("reopened worklog item = %d, want nil", *reopened.ItemID)
	}
}

func TestTimeWorklogPatchPreservesOmittedTimestamps(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	regional := MakeAuthRequest(t, server, http.MethodPut, "/users/1/regional-settings", map[string]any{
		"timezone": "Europe/Zurich",
		"language": "en",
	})
	defer regional.Body.Close()
	AssertStatusCode(t, regional, http.StatusOK)

	projectID := createTimeProjectFixture(t, server, "Patch preservation")
	secondProjectID := createTimeProjectFixture(t, server, "Patch preservation second")
	create := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/time/worklogs", map[string]any{
		"project_id":  projectID,
		"description": "before",
		"date":        "2026-09-05",
		"start_time":  "09:15",
		"end_time":    "10:45",
	})
	defer create.Body.Close()
	AssertStatusCode(t, create, http.StatusCreated)
	var worklog temporalWorklog
	DecodeJSON(t, create, &worklog)

	location, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}
	originalStart := time.Date(2026, 9, 5, 9, 15, 0, 0, location)
	originalEnd := time.Date(2026, 9, 5, 10, 45, 0, 0, location)
	assertWorklogInterval(t, worklog, originalStart, originalEnd, 90)

	patches := []struct {
		name  string
		patch map[string]any
	}{
		{name: "description only", patch: map[string]any{"description": "after"}},
		{name: "empty", patch: map[string]any{}},
		{name: "project only", patch: map[string]any{"project_id": secondProjectID}},
		{name: "item only", patch: map[string]any{"item_id": nil}},
		{name: "timezone only", patch: map[string]any{"timezone": "UTC"}},
	}
	for _, test := range patches {
		t.Run(test.name, func(t *testing.T) {
			updated := patchTemporalWorklog(t, server, worklog.ID, test.patch)
			assertWorklogInterval(t, updated, originalStart, originalEnd, 90)
		})
	}

	dateMoved := patchTemporalWorklog(t, server, worklog.ID, map[string]any{"date": "2026-09-06"})
	assertWorklogInterval(t, dateMoved,
		time.Date(2026, 9, 6, 9, 15, 0, 0, location),
		time.Date(2026, 9, 6, 10, 45, 0, 0, location), 90)
	if got, want := time.Unix(dateMoved.Date, 0).UTC().Format(time.DateOnly), "2026-09-06"; got != want {
		t.Fatalf("moved worklog date = %s, want %s", got, want)
	}

	clockMoved := patchTemporalWorklog(t, server, worklog.ID, map[string]any{
		"start_time": "08:00",
		"end_time":   "09:30",
	})
	assertWorklogInterval(t, clockMoved,
		time.Date(2026, 9, 6, 8, 0, 0, 0, location),
		time.Date(2026, 9, 6, 9, 30, 0, 0, location), 90)

	durationMoved := patchTemporalWorklog(t, server, worklog.ID, map[string]any{"duration_minutes": 120})
	assertWorklogInterval(t, durationMoved,
		time.Date(2026, 9, 6, 0, 0, 0, 0, location),
		time.Date(2026, 9, 6, 2, 0, 0, 0, location), 120)
}
