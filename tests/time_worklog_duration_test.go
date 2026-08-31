// time_worklog_duration_test covers the `duration` shorthand on
// POST /time/worklogs end-to-end (WI-944). The parser behind it used to match
// an unanchored prefix, so a malformed duration was silently recorded as
// whatever leading token happened to parse: "5h30" became 5h, "90ms" became
// 90 minutes. These tests pin that the API now answers 400 instead of writing
// a wrong-but-plausible timesheet entry.
package tests

import (
	"io"
	"net/http"
	"testing"
	"time"
)

// createDurationTimeProject seeds a customer + project and returns the project
// ID the worklog endpoints require.
func createDurationTimeProject(t *testing.T, ts *TestServer, name string) int {
	t.Helper()
	custResp := MakeAuthRequest(t, ts, http.MethodPost, "/customer-organisations",
		map[string]interface{}{"name": "DurCust" + name, "email": name + "@example.com"})
	defer custResp.Body.Close()
	if custResp.StatusCode != http.StatusCreated && custResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(custResp.Body)
		t.Fatalf("create customer: %d %s", custResp.StatusCode, string(b))
	}
	var custGot map[string]interface{}
	DecodeJSON(t, custResp, &custGot)
	customerID := ExtractIDFromResponse(t, custGot)

	projResp := MakeAuthRequest(t, ts, http.MethodPost, "/time/projects",
		map[string]interface{}{"customer_id": customerID, "name": name, "hourly_rate": 100})
	defer projResp.Body.Close()
	if projResp.StatusCode != http.StatusCreated && projResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(projResp.Body)
		t.Fatalf("create project: %d %s", projResp.StatusCode, string(b))
	}
	var projGot map[string]interface{}
	DecodeJSON(t, projResp, &projGot)
	return ExtractIDFromResponse(t, projGot)
}

func TestTimeWorklog_DurationOnlyAnchorsAtSubmittedDateStart(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	projectID := createDurationTimeProject(t, ts, "DurationAnchor")

	status, minutes := postWorklogDuration(t, ts, projectID, "90m")
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("duration-only worklog status = %d", status)
	}
	if minutes != 90 {
		t.Fatalf("duration = %d, want 90", minutes)
	}

	var start, end int64
	if err := ts.DB().QueryRow(`SELECT start_time, end_time FROM time_worklogs WHERE project_id = ? ORDER BY id DESC LIMIT 1`, projectID).Scan(&start, &end); err != nil {
		t.Fatalf("load duration-only worklog: %v", err)
	}
	if got := time.Unix(start, 0).UTC().Format(time.RFC3339); got != "2026-06-04T00:00:00Z" {
		t.Fatalf("start = %s, want submitted date start", got)
	}
	if got := time.Unix(end, 0).UTC().Format(time.RFC3339); got != "2026-06-04T01:30:00Z" {
		t.Fatalf("end = %s, want 90 minutes after date start", got)
	}
}

// postWorklogDuration posts a worklog using the duration shorthand and returns
// the response status plus the recorded duration_minutes (-1 when refused).
func postWorklogDuration(t *testing.T, ts *TestServer, projectID int, duration string) (status int, minutes int64) {
	t.Helper()
	resp := MakeAuthRequest(t, ts, http.MethodPost, "/time/worklogs",
		map[string]interface{}{
			"project_id":  projectID,
			"description": "duration probe " + duration,
			"date":        "2026-06-04",
			"duration":    duration,
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return resp.StatusCode, -1
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	min, _ := got["duration_minutes"].(float64)
	return resp.StatusCode, int64(min)
}

func TestTimeWorklog_DocumentedDurationsRecordExactMinutes(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	projectID := createDurationTimeProject(t, ts, "DurControl")

	cases := []struct {
		duration string
		want     int64
	}{
		{duration: "3h45m", want: 225},
		{duration: "1h", want: 60},
		{duration: "30m", want: 30},
		{duration: "0.5h", want: 30},
		{duration: "1d", want: 480},
	}

	for _, tc := range cases {
		t.Run(tc.duration, func(t *testing.T) {
			status, minutes := postWorklogDuration(t, ts, projectID, tc.duration)
			if status != http.StatusCreated && status != http.StatusOK {
				t.Fatalf("%s rejected: status %d", tc.duration, status)
			}
			if minutes != tc.want {
				t.Fatalf("%s recorded as %d minutes, want %d", tc.duration, minutes, tc.want)
			}
		})
	}
}

func TestTimeWorklog_MalformedDurationIsRejectedNotSilentlyTruncated(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	projectID := createDurationTimeProject(t, ts, "DurMalformed")

	cases := []struct {
		name      string
		duration  string
		wasStored int64 // what the unanchored parser used to record
	}{
		{name: "BareTrailingNumber", duration: "5h30", wasStored: 300},
		{name: "MillisecondTypo", duration: "90ms", wasStored: 90},
		{name: "PluralisedUnit", duration: "30mins", wasStored: 30},
		{name: "UnsupportedSeconds", duration: "2h15m20s", wasStored: 135},
		{name: "TrailingJunk", duration: "5h junk", wasStored: 300},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, minutes := postWorklogDuration(t, ts, projectID, tc.duration)
			if status == http.StatusCreated || status == http.StatusOK {
				t.Fatalf("%q accepted and recorded %d minutes (regression: used to store %d); want 400",
					tc.duration, minutes, tc.wasStored)
			}
			if status != http.StatusBadRequest {
				t.Fatalf("%q produced status %d, want 400", tc.duration, status)
			}
		})
	}
}
