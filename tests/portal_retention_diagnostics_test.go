package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// TestPortalRetentionSchedulersSurfaceInDiagnostics pins the admin
// diagnostics contract for the WI-1552 retention jobs: both sweepers tick at
// server start, record their runs in scheduler_runs, and appear through
// /admin/diagnostics/scheduler-runs filtered by name. Non-admin callers are
// denied like every other diagnostics route.
func TestPortalRetentionSchedulersSurfaceInDiagnostics(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	for _, schedulerName := range []string{"portal_auth_retention", "kb_events_retention"} {
		resp := MakeAuthRequest(t, server, http.MethodGet,
			fmt.Sprintf("/admin/diagnostics/scheduler-runs?scheduler=%s&limit=10", schedulerName), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var runs []struct {
			SchedulerName string  `json:"scheduler_name"`
			Success       bool    `json:"success"`
			ItemsProcessed *int   `json:"items_processed"`
			DurationMs    *int    `json:"duration_ms"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&runs); err != nil {
			t.Fatalf("decode scheduler runs for %s: %v", schedulerName, err)
		}
		if len(runs) == 0 {
			t.Fatalf("scheduler %s has no recorded runs; the retention sweeper must tick at startup and surface in diagnostics", schedulerName)
		}
		found := false
		for _, run := range runs {
			if run.SchedulerName != schedulerName {
				t.Errorf("filter leak: got scheduler %q when filtering for %q", run.SchedulerName, schedulerName)
				continue
			}
			found = true
			if !run.Success {
				t.Errorf("scheduler %s run not successful: %+v", schedulerName, run)
			}
		}
		if !found {
			t.Fatalf("no runs named %s in response", schedulerName)
		}
	}

	// Diagnostics are admin-only.
	_, _, plainPass := CreateTestUserWithCredentials(t, server, "retentionplain", "retentionplain@test.local")
	plainSession, _ := CreateAuthCredentialsForUser(t, server, "retentionplain", plainPass)
	denied := MakeAuthRequestWithToken(t, server, plainSession, http.MethodGet,
		"/admin/diagnostics/scheduler-runs?scheduler=portal_auth_retention", nil)
	defer denied.Body.Close()
	AssertStatusCode(t, denied, http.StatusForbidden)
}
