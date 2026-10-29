//go:build test

package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"windshift/internal/models"
)

func slaMetricPayload(name string, calendarID int) map[string]any {
	return map[string]any{
		"name":           name,
		"display_format": "time",
		"position":       0,
		"is_active":      true,
		"conditions": []map[string]any{{
			"phase": "start", "position": 0, "condition_type": "status_entered",
			"config": map[string]any{"status_ids": []int{999_999}},
		}},
		"goals": []map[string]any{{
			"position": 0, "ql_query": "1 = 1", "import_status": "native",
			"targets": []map[string]any{{
				"position": 0, "target_ms": 3_600_000, "calendar_id": calendarID,
			}},
		}},
	}
}

// TestV2SLAMetricCRUD covers metric configuration on the v2 surface, including
// nested conditions/goals/targets, validation, and the viewer denial.
func TestV2SLAMetricCRUD(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	bearer := server.BearerToken
	workspaceID, _ := CreateTestWorkspace(t, server, "V2 Metric Workspace", shortKey("VMW"))

	calResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/calendars", workspaceID), map[string]any{"name": "V2 hours", "timezone": "UTC"})
	AssertStatusCode(t, calResp, http.StatusCreated)
	var calendar models.WorkingCalendar
	DecodeJSON(t, calResp, &calendar)
	_ = calResp.Body.Close()

	create := MakeBearerRequestWithToken(t, server, bearer, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics", workspaceID), slaMetricPayload("V2 first response", calendar.ID))
	AssertStatusCode(t, create, http.StatusCreated)
	var metric models.SLAMetric
	DecodeJSON(t, create, &metric)
	_ = create.Body.Close()
	if metric.ID == 0 || metric.Name != "V2 first response" || len(metric.Conditions) != 1 || len(metric.Goals) != 1 {
		t.Fatalf("created v2 metric = %#v", metric)
	}
	if len(metric.Goals[0].Targets) != 1 || metric.Goals[0].Targets[0].CalendarID != calendar.ID {
		t.Fatalf("created v2 metric targets = %#v", metric.Goals[0].Targets)
	}

	list := MakeBearerRequestWithToken(t, server, bearer, http.MethodGet, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics", workspaceID), nil)
	AssertStatusCode(t, list, http.StatusOK)
	var metrics []models.SLAMetric
	DecodeJSON(t, list, &metrics)
	_ = list.Body.Close()
	if len(metrics) != 1 || metrics[0].ID != metric.ID {
		t.Fatalf("v2 metric list = %#v", metrics)
	}

	get := MakeBearerRequestWithToken(t, server, bearer, http.MethodGet, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics/%d", workspaceID, metric.ID), nil)
	AssertStatusCode(t, get, http.StatusOK)
	var fetched models.SLAMetric
	DecodeJSON(t, get, &fetched)
	_ = get.Body.Close()
	if fetched.ID != metric.ID || fetched.Name != metric.Name {
		t.Fatalf("v2 metric get = %#v", fetched)
	}

	bad := MakeBearerRequestWithToken(t, server, bearer, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics", workspaceID), map[string]any{"name": "Bad", "display_format": "calendar"})
	AssertStatusCode(t, bad, http.StatusBadRequest)
	_ = bad.Body.Close()

	// A metric with no goals has nothing to compute, so it is rejected.
	noGoals := slaMetricPayload("No goals", calendar.ID)
	noGoals["goals"] = []map[string]any{}
	badGoals := MakeBearerRequestWithToken(t, server, bearer, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics", workspaceID), noGoals)
	AssertStatusCode(t, badGoals, http.StatusBadRequest)
	_ = badGoals.Body.Close()

	update := MakeBearerRequestWithToken(t, server, bearer, http.MethodPut, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics/%d", workspaceID, metric.ID), slaMetricPayload("V2 second response", calendar.ID))
	AssertStatusCode(t, update, http.StatusOK)
	var updated models.SLAMetric
	DecodeJSON(t, update, &updated)
	_ = update.Body.Close()
	if updated.Name != "V2 second response" {
		t.Fatalf("updated v2 metric = %#v", updated)
	}

	viewerID, viewerName, viewerPass := CreateTestUserWithCredentials(t, server, "v2_metric_viewer", "v2_metric_viewer@test.com")
	AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")
	_, viewerBearer := CreateAuthCredentialsForUser(t, server, viewerName, viewerPass)
	denied := MakeBearerRequestWithToken(t, server, viewerBearer, http.MethodGet, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics", workspaceID), nil)
	if denied.StatusCode != http.StatusForbidden && denied.StatusCode != http.StatusNotFound {
		t.Fatalf("viewer metric list = %d, want denied (403/404)", denied.StatusCode)
	}
	_ = denied.Body.Close()

	del := MakeBearerRequestWithToken(t, server, bearer, http.MethodDelete, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics/%d", workspaceID, metric.ID), nil)
	AssertStatusCode(t, del, http.StatusNoContent)
	_ = del.Body.Close()

	after := MakeBearerRequestWithToken(t, server, bearer, http.MethodGet, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics", workspaceID), nil)
	AssertStatusCode(t, after, http.StatusOK)
	var remaining []models.SLAMetric
	DecodeJSON(t, after, &remaining)
	_ = after.Body.Close()
	if len(remaining) != 0 {
		t.Fatalf("v2 metrics after delete = %#v", remaining)
	}
}

// TestV2SLAMetricGoalShape pins the redesigned goal contract: a goal may have
// a blank query (match all) and must have exactly one target.
func TestV2SLAMetricGoalShape(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	bearer := server.BearerToken
	workspaceID, _ := CreateTestWorkspace(t, server, "V2 Goal Shape", shortKey("VGS"))

	calResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/calendars", workspaceID), map[string]any{"name": "Shape hours", "timezone": "UTC"})
	AssertStatusCode(t, calResp, http.StatusCreated)
	var calendar models.WorkingCalendar
	DecodeJSON(t, calResp, &calendar)
	_ = calResp.Body.Close()

	// A blank query is accepted and matches every item.
	blank := slaMetricPayload("Blank query", calendar.ID)
	blank["goals"].([]map[string]any)[0]["ql_query"] = ""
	create := MakeBearerRequestWithToken(t, server, bearer, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics", workspaceID), blank)
	AssertStatusCode(t, create, http.StatusCreated)
	_ = create.Body.Close()

	// A goal with more than one target is rejected.
	two := slaMetricPayload("Two targets", calendar.ID)
	goal := two["goals"].([]map[string]any)[0]
	goal["targets"] = append(goal["targets"].([]map[string]any), map[string]any{
		"position": 1, "target_ms": 7_200_000, "calendar_id": calendar.ID,
	})
	bad := MakeBearerRequestWithToken(t, server, bearer, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics", workspaceID), two)
	AssertStatusCode(t, bad, http.StatusBadRequest)
	_ = bad.Body.Close()
}

func TestSLAManualRecalculationRejectsMetricFromAnotherWorkspace(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Recalculation owner", shortKey("SRO"))
	otherWorkspaceID, _ := CreateTestWorkspace(t, server, "Recalculation other", shortKey("SRT"))
	createCalendar := func(workspaceID int, name string) models.WorkingCalendar {
		response := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/calendars", workspaceID), map[string]any{"name": name, "timezone": "UTC"})
		AssertStatusCode(t, response, http.StatusCreated)
		var calendar models.WorkingCalendar
		DecodeJSON(t, response, &calendar)
		_ = response.Body.Close()
		return calendar
	}
	ownerCalendar := createCalendar(workspaceID, "Owner hours")
	otherCalendar := createCalendar(otherWorkspaceID, "Other hours")
	createMetric := func(workspaceID, calendarID int, name string) models.SLAMetric {
		response := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/metrics", workspaceID), slaMetricPayload(name, calendarID))
		AssertStatusCode(t, response, http.StatusCreated)
		var metric models.SLAMetric
		DecodeJSON(t, response, &metric)
		_ = response.Body.Close()
		return metric
	}
	createMetric(workspaceID, ownerCalendar.ID, "Owner metric")
	otherMetric := createMetric(otherWorkspaceID, otherCalendar.ID, "Other metric")

	response := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/recalculations", workspaceID), map[string]any{"metric_id": otherMetric.ID})
	AssertStatusCode(t, response, http.StatusNotFound)
	_ = response.Body.Close()
}

// TestWSCLISLAMetrics drives the metric CLI against the v2 API.
func TestWSCLISLAMetrics(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	world := SeedWorld(t, ts)
	calendarID, metricID := seedSLACalendarAndMetric(t, ts, world.Alpha.ID)

	out, stderr, code := runWS(t, ts, "sla", "metrics", "list", "-w", world.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	var listed []models.SLAMetric
	if err := json.Unmarshal(out, &listed); err != nil {
		t.Fatalf("decode ws sla metrics list: %v\nraw=%s", err, string(out))
	}
	if len(listed) != 1 || listed[0].ID != metricID {
		t.Fatalf("ws sla metrics list = %#v", listed)
	}

	out, stderr, code = runWS(t, ts, "sla", "metrics", "get", fmt.Sprintf("%d", metricID), "-w", world.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	var fetched models.SLAMetric
	if err := json.Unmarshal(out, &fetched); err != nil {
		t.Fatalf("decode ws sla metrics get: %v\nraw=%s", err, string(out))
	}
	if fetched.ID != metricID || fetched.Name != "CLI first response" {
		t.Fatalf("ws sla metrics get = %#v", fetched)
	}

	dir := t.TempDir()
	createPath := filepath.Join(dir, "metric.json")
	createBody, err := json.Marshal(slaMetricPayload("CLI created", calendarID))
	if err != nil {
		t.Fatalf("marshal metric: %v", err)
	}
	if err := os.WriteFile(createPath, createBody, 0o600); err != nil {
		t.Fatalf("write metric file: %v", err)
	}

	out, stderr, code = runWS(t, ts, "sla", "metrics", "create", "--file", createPath, "-w", world.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	var created models.SLAMetric
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatalf("decode ws sla metrics create: %v\nraw=%s", err, string(out))
	}
	if created.ID == 0 || created.Name != "CLI created" {
		t.Fatalf("ws sla metrics create = %#v", created)
	}

	updateBody, err := json.Marshal(slaMetricPayload("CLI updated", calendarID))
	if err != nil {
		t.Fatalf("marshal metric: %v", err)
	}
	if err := os.WriteFile(createPath, updateBody, 0o600); err != nil {
		t.Fatalf("write metric file: %v", err)
	}
	out, stderr, code = runWS(t, ts, "sla", "metrics", "update", fmt.Sprintf("%d", created.ID), "--file", createPath, "-w", world.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	var updated models.SLAMetric
	if err := json.Unmarshal(out, &updated); err != nil {
		t.Fatalf("decode ws sla metrics update: %v\nraw=%s", err, string(out))
	}
	if updated.Name != "CLI updated" {
		t.Fatalf("ws sla metrics update = %#v", updated)
	}

	out, stderr, code = runWS(t, ts, "sla", "metrics", "delete", fmt.Sprintf("%d", created.ID), "-w", world.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	var deleted map[string]bool
	if err := json.Unmarshal(out, &deleted); err != nil {
		t.Fatalf("decode ws sla metrics delete: %v\nraw=%s", err, string(out))
	}
	if !deleted["deleted"] {
		t.Fatalf("ws sla metrics delete = %#v", deleted)
	}
}
