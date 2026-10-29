//go:build test

package tests

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/models"
)

func TestSLAConfigurationAPI(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminToken := CreateBearerToken(t, server)
	server.BearerToken = adminToken

	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Config Workspace", shortKey("SCW"))
	itemID := CreateTestItem(t, server, workspaceID, "SLA item")

	// A workspace admin can create a calendar.
	calendarPayload := map[string]any{
		"name":     "Always open",
		"timezone": "UTC",
		"weekly_intervals": map[string]any{
			"monday":    []map[string]string{{"start": "00:00", "end": "24:00"}},
			"tuesday":   []map[string]string{{"start": "00:00", "end": "24:00"}},
			"wednesday": []map[string]string{{"start": "00:00", "end": "24:00"}},
			"thursday":  []map[string]string{{"start": "00:00", "end": "24:00"}},
			"friday":    []map[string]string{{"start": "00:00", "end": "24:00"}},
			"saturday":  []map[string]string{{"start": "00:00", "end": "24:00"}},
			"sunday":    []map[string]string{{"start": "00:00", "end": "24:00"}},
		},
	}
	resp := MakeAuthRequestWithToken(t, server, adminToken, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/calendars", workspaceID), calendarPayload)
	AssertStatusCode(t, resp, http.StatusCreated)
	var calendar models.WorkingCalendar
	DecodeJSON(t, resp, &calendar)
	_ = resp.Body.Close()
	if calendar.ID == 0 {
		t.Fatal("created calendar has no id")
	}

	// A metric with a start condition that can never match: no cycle is opened,
	// which keeps the item SLA read deterministic.
	metricPayload := map[string]any{
		"name":           "First response",
		"display_format": "time",
		"is_active":      true,
		"conditions": []map[string]any{{
			"phase": "start", "position": 0, "condition_type": "status_entered",
			"config": map[string]any{"status_ids": []int{999_999}},
		}},
		"goals": []map[string]any{{
			"position": 0, "ql_query": "1 = 1", "import_status": "native",
			"targets": []map[string]any{{
				"position": 0, "target_ms": 3_600_000, "calendar_id": calendar.ID,
			}},
		}},
	}
	resp = MakeAuthRequestWithToken(t, server, adminToken, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/metrics", workspaceID), metricPayload)
	AssertStatusCode(t, resp, http.StatusCreated)
	var metric models.SLAMetric
	DecodeJSON(t, resp, &metric)
	_ = resp.Body.Close()
	if metric.ID == 0 || len(metric.Goals) != 1 || len(metric.Goals[0].Targets) != 1 {
		t.Fatalf("created metric = %#v", metric)
	}

	// Listing returns the metric with its configuration intact.
	resp = MakeAuthRequestWithToken(t, server, adminToken, http.MethodGet, fmt.Sprintf("/workspaces/%d/sla/metrics", workspaceID), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var metrics []models.SLAMetric
	DecodeJSON(t, resp, &metrics)
	_ = resp.Body.Close()
	if len(metrics) != 1 || metrics[0].ID != metric.ID || len(metrics[0].Conditions) != 1 {
		t.Fatalf("listed metrics = %#v", metrics)
	}

	// The compliance report lists the metric with no completed cycles.
	resp = MakeAuthRequestWithToken(t, server, adminToken, http.MethodGet, fmt.Sprintf("/workspaces/%d/sla/report", workspaceID), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var report models.SLAReport
	DecodeJSON(t, resp, &report)
	_ = resp.Body.Close()
	if len(report.Metrics) != 1 || report.Metrics[0].MetricID != metric.ID || report.Metrics[0].Completed != 0 {
		t.Fatalf("report = %#v", report)
	}

	// The item SLA read succeeds and is empty because no cycle can start.
	resp = MakeAuthRequestWithToken(t, server, adminToken, http.MethodGet, fmt.Sprintf("/items/%d/sla", itemID), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var states []models.ItemSLA
	DecodeJSON(t, resp, &states)
	_ = resp.Body.Close()
	if len(states) != 0 {
		t.Fatalf("item SLA states = %#v, want none", states)
	}

	// Deleting the metric frees the calendar, which can then be deleted.
	resp = MakeAuthRequestWithToken(t, server, adminToken, http.MethodDelete, fmt.Sprintf("/workspaces/%d/sla/metrics/%d", workspaceID, metric.ID), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	resp = MakeAuthRequestWithToken(t, server, adminToken, http.MethodDelete, fmt.Sprintf("/workspaces/%d/sla/calendars/%d", workspaceID, calendar.ID), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

// TestSLAMetricRejectsMalformedNativeGoalQuery verifies WI-1574: the service
// parses every native goal query before persisting, so a direct API caller
// cannot save a metric that would disable workspace-wide SLA evaluation, and
// the previously valid configuration stays usable.
func TestSLAMetricRejectsMalformedNativeGoalQuery(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminToken := CreateBearerToken(t, server)
	server.BearerToken = adminToken

	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Query Validation", shortKey("SQV"))

	calendarPayload := map[string]any{
		"name":     "Always open",
		"timezone": "UTC",
		"weekly_intervals": map[string]any{
			"monday": []map[string]string{{"start": "00:00", "end": "24:00"}},
		},
	}
	resp := MakeAuthRequestWithToken(t, server, adminToken, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/calendars", workspaceID), calendarPayload)
	AssertStatusCode(t, resp, http.StatusCreated)
	var calendar models.WorkingCalendar
	DecodeJSON(t, resp, &calendar)
	_ = resp.Body.Close()

	metricPayload := func(name, query string) map[string]any {
		return map[string]any{
			"name":           name,
			"display_format": "time",
			"is_active":      true,
			"conditions":     []map[string]any{},
			"goals": []map[string]any{{
				"position": 0, "ql_query": query, "import_status": "native",
				"targets": []map[string]any{{
					"position": 0, "target_ms": 3_600_000, "calendar_id": calendar.ID,
				}},
			}},
		}
	}

	resp = MakeAuthRequestWithToken(t, server, adminToken, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/metrics", workspaceID), metricPayload("Valid metric", "priority = 1"))
	AssertStatusCode(t, resp, http.StatusCreated)
	var valid models.SLAMetric
	DecodeJSON(t, resp, &valid)
	_ = resp.Body.Close()

	for _, bad := range []string{"priority = ", "(priority = 1"} {
		resp = MakeAuthRequestWithToken(t, server, adminToken, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/metrics", workspaceID), metricPayload("Malformed "+bad, bad))
		if resp.StatusCode < 400 || resp.StatusCode >= 500 {
			t.Fatalf("create with query %q = %d, want a 4xx validation error", bad, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	// Updating the valid metric with a malformed query is rejected too, and the
	// stored query is unchanged.
	update := metricPayload("Valid metric", "priority = 1")
	update["goals"].([]map[string]any)[0]["ql_query"] = "priority = "
	resp = MakeAuthRequestWithToken(t, server, adminToken, http.MethodPut, fmt.Sprintf("/workspaces/%d/sla/metrics/%d", workspaceID, valid.ID), update)
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("update with malformed query = %d, want a 4xx validation error", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = MakeAuthRequestWithToken(t, server, adminToken, http.MethodGet, fmt.Sprintf("/workspaces/%d/sla/metrics", workspaceID), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var metrics []models.SLAMetric
	DecodeJSON(t, resp, &metrics)
	_ = resp.Body.Close()
	if len(metrics) != 1 || metrics[0].ID != valid.ID {
		t.Fatalf("metrics after rejected writes = %#v, want only the valid metric", metrics)
	}
	if len(metrics[0].Goals) != 1 || metrics[0].Goals[0].QLQuery != "priority = 1" {
		t.Fatalf("valid metric query changed: %#v", metrics[0].Goals)
	}

	// The workspace SLA report still compiles the remaining metric.
	resp = MakeAuthRequestWithToken(t, server, adminToken, http.MethodGet, fmt.Sprintf("/workspaces/%d/sla/report", workspaceID), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

func TestSLAConfigurationDeniedForViewer(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminToken := CreateBearerToken(t, server)
	server.BearerToken = adminToken

	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Denial Workspace", shortKey("SDW"))
	viewerID, viewerUsername, viewerPassword := CreateTestUserWithCredentials(t, server, "sla_viewer", "sla_viewer@test.com")
	AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")
	viewerToken := CreateBearerTokenForUser(t, server, viewerUsername, viewerPassword)

	resp := MakeAuthRequestWithToken(t, server, viewerToken, http.MethodGet, fmt.Sprintf("/workspaces/%d/sla/metrics", workspaceID), nil)
	AssertStatusCode(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}
