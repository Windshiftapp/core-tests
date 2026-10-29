//go:build test

package tests

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/models"
)

func TestV2WorkspaceCalendarCRUD(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	bearer := server.BearerToken
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Calendar Workspace", shortKey("SLC"))

	// Create.
	payload := map[string]any{
		"name":     "Support hours",
		"timezone": "UTC",
		"weekly_intervals": map[string]any{
			"monday":    []map[string]string{{"start": "09:00", "end": "17:00"}},
			"tuesday":   []map[string]string{{"start": "09:00", "end": "17:00"}},
			"wednesday": []map[string]string{{"start": "09:00", "end": "17:00"}},
			"thursday":  []map[string]string{{"start": "09:00", "end": "17:00"}},
			"friday":    []map[string]string{{"start": "09:00", "end": "17:00"}},
		},
		"holidays": []map[string]any{{"date": "2025-01-01", "name": "New Year"}},
	}
	resp := MakeBearerRequestWithToken(t, server, bearer, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/calendars", workspaceID), payload)
	AssertStatusCode(t, resp, http.StatusCreated)
	var calendar models.WorkingCalendar
	DecodeJSON(t, resp, &calendar)
	_ = resp.Body.Close()
	if calendar.ID == 0 || calendar.Name != "Support hours" || calendar.Timezone != "UTC" {
		t.Fatalf("created calendar = %#v", calendar)
	}

	// List.
	resp = MakeBearerRequestWithToken(t, server, bearer, http.MethodGet, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/calendars", workspaceID), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var calendars []models.WorkingCalendar
	DecodeJSON(t, resp, &calendars)
	_ = resp.Body.Close()
	if len(calendars) != 1 || calendars[0].ID != calendar.ID {
		t.Fatalf("listed calendars = %#v", calendars)
	}

	// Available calendars include the owned one.
	resp = MakeBearerRequestWithToken(t, server, bearer, http.MethodGet, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/available-calendars", workspaceID), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	// Validation errors are client errors.
	badPayload := map[string]any{"name": "Bad", "timezone": "Mars/Olympus"}
	resp = MakeBearerRequestWithToken(t, server, bearer, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/calendars", workspaceID), badPayload)
	AssertStatusCode(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()

	// Update replaces the calendar.
	update := map[string]any{"name": "Support hours v2", "timezone": "Europe/Berlin"}
	resp = MakeBearerRequestWithToken(t, server, bearer, http.MethodPut, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/calendars/%d", workspaceID, calendar.ID), update)
	AssertStatusCode(t, resp, http.StatusOK)
	var updated models.WorkingCalendar
	DecodeJSON(t, resp, &updated)
	_ = resp.Body.Close()
	if updated.Name != "Support hours v2" || updated.Timezone != "Europe/Berlin" {
		t.Fatalf("updated calendar = %#v", updated)
	}

	// A calendar referenced by a metric cannot be deleted.
	metricPayload := map[string]any{
		"name":           "Calendar-backed metric",
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
	resp = MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/metrics", workspaceID), metricPayload)
	AssertStatusCode(t, resp, http.StatusCreated)
	var metric models.SLAMetric
	DecodeJSON(t, resp, &metric)
	_ = resp.Body.Close()

	resp = MakeBearerRequestWithToken(t, server, bearer, http.MethodDelete, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/calendars/%d", workspaceID, calendar.ID), nil)
	AssertStatusCode(t, resp, http.StatusConflict)
	_ = resp.Body.Close()

	// Once the metric is gone, the calendar can be deleted.
	resp = MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/workspaces/%d/sla/metrics/%d", workspaceID, metric.ID), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	resp = MakeBearerRequestWithToken(t, server, bearer, http.MethodDelete, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/calendars/%d", workspaceID, calendar.ID), nil)
	AssertStatusCode(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()
}

func TestV2WorkspaceCalendarRequiresAdmin(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Calendar Denial", shortKey("SLD"))
	viewerID, username, password := CreateTestUserWithCredentials(t, server, "sla_cal_viewer", "sla_cal_viewer@test.com")
	AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")
	_, viewerBearer := CreateAuthCredentialsForUser(t, server, username, password)

	payload := map[string]any{"name": "Nope", "timezone": "UTC"}
	resp := MakeBearerRequestWithToken(t, server, viewerBearer, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/calendars", workspaceID), payload)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("viewer calendar create = %d, want denied (403/404)", resp.StatusCode)
	}
}
