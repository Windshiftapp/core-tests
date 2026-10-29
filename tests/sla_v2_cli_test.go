//go:build test

package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/models"
	"windshift/internal/wscli"
)

func seedSLACalendarAndMetric(t *testing.T, ts *TestServer, workspaceID int) (calendarID, metricID int) {
	t.Helper()
	calendarPayload := map[string]any{
		"name":     "CLI hours",
		"timezone": "UTC",
		"weekly_intervals": map[string]any{
			"monday": []map[string]string{{"start": "00:00", "end": "24:00"}},
		},
	}
	resp := MakeAuthRequest(t, ts, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/calendars", workspaceID), calendarPayload)
	AssertStatusCode(t, resp, http.StatusCreated)
	var calendar models.WorkingCalendar
	DecodeJSON(t, resp, &calendar)
	_ = resp.Body.Close()

	metricPayload := map[string]any{
		"name":           "CLI first response",
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
	resp = MakeAuthRequest(t, ts, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/metrics", workspaceID), metricPayload)
	AssertStatusCode(t, resp, http.StatusCreated)
	var metric models.SLAMetric
	DecodeJSON(t, resp, &metric)
	_ = resp.Body.Close()
	return calendar.ID, metric.ID
}

func TestSLAReadSurfaceOnV2BearerAPI(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	bearer := server.BearerToken
	world := SeedWorld(t, server)
	itemID := CreateTestItem(t, server, world.Alpha.ID, "SLA v2 item")

	resp := MakeBearerRequestWithToken(t, server, bearer, http.MethodGet, fmt.Sprintf("/rest/api/v2/items/%d/sla", itemID), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var states []models.ItemSLA
	DecodeJSON(t, resp, &states)
	_ = resp.Body.Close()
	if len(states) != 0 {
		t.Fatalf("v2 item SLA states = %#v, want none", states)
	}

	resp = MakeBearerRequestWithToken(t, server, bearer, http.MethodGet, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/report", world.Alpha.ID), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var report models.SLAReport
	DecodeJSON(t, resp, &report)
	_ = resp.Body.Close()
}

func TestWSCLISLAItemAndReport(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	world := SeedWorld(t, ts)
	_, metricID := seedSLACalendarAndMetric(t, ts, world.Alpha.ID)
	itemID := CreateTestItem(t, ts, world.Alpha.ID, "CLI SLA item")

	out, stderr, code := runWS(t, ts, "sla", "item", fmt.Sprintf("%d", itemID), "-o", "json")
	requireZero(t, code, stderr)
	var states []models.ItemSLA
	if err := json.Unmarshal(out, &states); err != nil {
		t.Fatalf("decode ws sla item: %v\nraw=%s", err, string(out))
	}
	if len(states) != 0 {
		t.Fatalf("ws sla item states = %#v, want none", states)
	}

	out, stderr, code = runWS(t, ts, "sla", "report", world.Alpha.Key, "-o", "json")
	requireZero(t, code, stderr)
	var report models.SLAReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("decode ws sla report: %v\nraw=%s", err, string(out))
	}
	if len(report.Metrics) != 1 || report.Metrics[0].MetricID != metricID {
		t.Fatalf("ws sla report = %#v", report.Metrics)
	}
}

func seedSLAMetricOnCreate(t *testing.T, ts *TestServer, workspaceID int) int {
	t.Helper()
	calendarPayload := map[string]any{
		"name":     "At-risk hours",
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
	resp := MakeAuthRequest(t, ts, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/calendars", workspaceID), calendarPayload)
	AssertStatusCode(t, resp, http.StatusCreated)
	var calendar models.WorkingCalendar
	DecodeJSON(t, resp, &calendar)
	_ = resp.Body.Close()

	metricPayload := map[string]any{
		"name":           "At-risk first response",
		"display_format": "time",
		"is_active":      true,
		"conditions": []map[string]any{{
			"phase": "start", "position": 0, "condition_type": "created",
			"config": map[string]any{},
		}},
		"goals": []map[string]any{{
			"position": 0, "ql_query": "1 = 1", "import_status": "native",
			"targets": []map[string]any{{
				"position": 0, "target_ms": 3_600_000, "calendar_id": calendar.ID,
			}},
		}},
	}
	resp = MakeAuthRequest(t, ts, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/metrics", workspaceID), metricPayload)
	AssertStatusCode(t, resp, http.StatusCreated)
	var metric models.SLAMetric
	DecodeJSON(t, resp, &metric)
	_ = resp.Body.Close()
	return metric.ID
}

func TestWSCLISLAAtRiskAndBreached(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	world := SeedWorld(t, ts)
	seedSLAMetricOnCreate(t, ts, world.Alpha.ID)

	// Creating the item records a created fact, so inline evaluation opens a
	// cycle with a one-hour deadline.
	itemID := CreateTestItem(t, ts, world.Alpha.ID, "At-risk item")

	listIDs := func(args ...string) map[int]bool {
		t.Helper()
		out, stderr, code := runWS(t, ts, args...)
		requireZero(t, code, stderr)
		var page wscli.PaginatedResponse[wscli.Item]
		if err := json.Unmarshal(out, &page); err != nil {
			t.Fatalf("decode ws %v: %v\nraw=%s", args, err, string(out))
		}
		ids := map[int]bool{}
		for _, item := range page.Data {
			ids[item.ID] = true
		}
		return ids
	}

	atRisk := listIDs("sla", "at-risk", "-w", world.Alpha.Key, "--within", "2d", "-o", "json")
	if !atRisk[itemID] {
		t.Fatalf("at-risk item missing from %v", atRisk)
	}

	// A zero-minute window only matches deadlines at or before now, and the
	// fresh cycle's deadline is an hour away.
	none := listIDs("sla", "at-risk", "-w", world.Alpha.Key, "--within", "0m", "-o", "json")
	if none[itemID] {
		t.Fatalf("item appeared in a zero-minute window: %v", none)
	}

	breached := listIDs("sla", "breached", "-w", world.Alpha.Key, "-o", "json")
	if breached[itemID] {
		t.Fatalf("unbreached item appeared in breached list: %v", breached)
	}
}
