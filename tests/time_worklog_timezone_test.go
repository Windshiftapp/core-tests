// time_worklog_timezone_test covers the 0.8.8 worklog time contracts:
// explicit clocks win over a supplied duration (WI-1279), overnight entries
// store one continuous interval that date edits move as a whole (WI-1282),
// description-only edits preserve the stored interval exactly (WI-1280), and
// date-range filters interpret civil dates in the request timezone against
// the stored timestamps (WI-1281).
package tests

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func createDenverWorklog(t *testing.T, server *TestServer, projectID int, date, startClock, endClock string) temporalWorklog {
	t.Helper()
	response := MakeAuthRequest(t, server, http.MethodPost, "/time/worklogs", map[string]any{
		"project_id":  projectID,
		"description": "timezone probe",
		"date":        date,
		"start_time":  startClock,
		"end_time":    endClock,
		"timezone":    "America/Denver",
	})
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusCreated)
	var worklog temporalWorklog
	DecodeJSON(t, response, &worklog)
	return worklog
}

func listWorklogIDsInRange(t *testing.T, server *TestServer, query string) map[int]bool {
	t.Helper()
	response := MakeAuthRequest(t, server, http.MethodGet, "/time/worklogs"+query, nil)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)
	var page struct {
		Items []temporalWorklog `json:"items"`
	}
	DecodeJSON(t, response, &page)
	ids := make(map[int]bool, len(page.Items))
	for _, worklog := range page.Items {
		ids[worklog.ID] = true
	}
	return ids
}

func TestTimeWorklog_ExplicitClocksBeatSuppliedDuration(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	projectID := createTimeProjectFixture(t, server, "TzPrecedence")

	worklog := createDenverWorklog(t, server, projectID, "2026-08-05", "16:00", "18:00")

	denver := mustLoadLocation(t, "America/Denver")
	if worklog.StartTime != time.Date(2026, 8, 5, 16, 0, 0, 0, denver).Unix() ||
		worklog.EndTime != time.Date(2026, 8, 5, 18, 0, 0, 0, denver).Unix() ||
		worklog.DurationMinutes != 120 {
		t.Fatalf("worklog = start %d end %d (%d min), want 16:00-18:00 Denver and 120 minutes",
			worklog.StartTime, worklog.EndTime, worklog.DurationMinutes)
	}

	mismatch := MakeAuthRequest(t, server, http.MethodPost, "/time/worklogs", map[string]any{
		"project_id":  projectID,
		"description": "mismatch probe",
		"date":        "2026-08-05",
		"start_time":  "16:00",
		"end_time":    "18:00",
		"duration":    "3h",
		"timezone":    "America/Denver",
	})
	defer mismatch.Body.Close()
	AssertStatusCode(t, mismatch, http.StatusBadRequest)
}

func TestTimeWorklog_OvernightIntervalSurvivesEditAndReopen(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	projectID := createTimeProjectFixture(t, server, "TzOvernight")

	worklog := createDenverWorklog(t, server, projectID, "2026-08-05", "23:00", "01:00")
	denver := mustLoadLocation(t, "America/Denver")
	assertWorklogInterval(t, worklog,
		time.Date(2026, 8, 5, 23, 0, 0, 0, denver),
		time.Date(2026, 8, 6, 1, 0, 0, 0, denver), 120)

	// Reopen-equivalent read returns the same complete interval.
	reopened := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/time/worklogs/%d", worklog.ID), nil)
	defer reopened.Body.Close()
	AssertStatusCode(t, reopened, http.StatusOK)
	var fetched temporalWorklog
	DecodeJSON(t, reopened, &fetched)
	assertWorklogInterval(t, fetched,
		time.Date(2026, 8, 5, 23, 0, 0, 0, denver),
		time.Date(2026, 8, 6, 1, 0, 0, 0, denver), 120)

	// A metadata-only edit must preserve the stored interval exactly.
	described := patchTemporalWorklog(t, server, worklog.ID, map[string]any{"description": "overnight, renamed"})
	assertWorklogInterval(t, described,
		time.Date(2026, 8, 5, 23, 0, 0, 0, denver),
		time.Date(2026, 8, 6, 1, 0, 0, 0, denver), 120)

	// Moving the selected start date moves both local dates together,
	// preserving the clock times and the day difference. The form pins the
	// entry timezone whenever time fields are resubmitted.
	moved := patchTemporalWorklog(t, server, worklog.ID, map[string]any{
		"date":     "2026-08-10",
		"timezone": "America/Denver",
	})
	assertWorklogInterval(t, moved,
		time.Date(2026, 8, 10, 23, 0, 0, 0, denver),
		time.Date(2026, 8, 11, 1, 0, 0, 0, denver), 120)
}

func TestTimeWorklog_DateRangeFiltersFollowRequestTimezone(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	projectID := createTimeProjectFixture(t, server, "TzRange")

	overnight := createDenverWorklog(t, server, projectID, "2026-08-05", "23:00", "01:00")
	exactMidnightEnd := createDenverWorklog(t, server, projectID, "2026-08-05", "22:00", "00:00")

	tests := []struct {
		name    string
		query   string
		wantHit bool
		worklog temporalWorklog
	}{
		{
			name: "denver range includes overnight start day", query: "?project_id=%d&from=2026-08-05&to=2026-08-05&timezone=America/Denver",
			wantHit: true, worklog: overnight,
		},
		{
			name: "tokyo range excludes denver overnight entry", query: "?project_id=%d&from=2026-08-05&to=2026-08-05&timezone=Asia/Tokyo",
			wantHit: false, worklog: overnight,
		},
		{
			name: "tokyo range includes entry on its tokyo day", query: "?project_id=%d&from=2026-08-06&to=2026-08-06&timezone=Asia/Tokyo",
			wantHit: true, worklog: overnight,
		},
		{
			name: "exact midnight end does not count into next day", query: "?project_id=%d&from=2026-08-06&to=2026-08-06&timezone=America/Denver",
			wantHit: false, worklog: exactMidnightEnd,
		},
		{
			name: "entry ending at midnight is included on its start day", query: "?project_id=%d&from=2026-08-05&to=2026-08-05&timezone=America/Denver",
			wantHit: true, worklog: exactMidnightEnd,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ids := listWorklogIDsInRange(t, server, fmt.Sprintf(test.query, projectID))
			if ids[test.worklog.ID] != test.wantHit {
				t.Fatalf("worklog %d presence = %t, want %t (ids %v)", test.worklog.ID, ids[test.worklog.ID], test.wantHit, ids)
			}
		})
	}

	// The v1 surface shares the timezone-aware interval semantics.
	v1 := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v1/time/worklogs?date_from=2026-08-05&date_to=2026-08-05&timezone=Asia/Tokyo"), nil)
	defer v1.Body.Close()
	AssertStatusCode(t, v1, http.StatusOK)
	var v1Page struct {
		Data []temporalWorklog `json:"data"`
	}
	DecodeJSON(t, v1, &v1Page)
	for _, worklog := range v1Page.Data {
		if worklog.ID == overnight.ID {
			t.Fatal("v1 tokyo-filtered list included the denver overnight worklog")
		}
	}
}

func TestTimeWorklog_DateRangeRejectsInvalidTimezone(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)

	response := MakeAuthRequest(t, server, http.MethodGet, "/time/worklogs?from=2026-08-05&timezone=Not/AZone", nil)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusBadRequest)
}

func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load timezone %s: %v", name, err)
	}
	return location
}
