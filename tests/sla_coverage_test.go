//go:build test

package tests

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/models"
)

// TestSLACoverageBlocks exercises the coverage signal end to end: the item SLA
// state, the compliance report, and the config-time preview compare the SLA's
// scheduling calendar against the bound team's service hours.
func TestSLACoverageBlocks(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Coverage Workspace", shortKey("SCW"))

	bothID, bothName, bothPass := CreateTestUserWithCredentials(t, server, "sla_coverage_both", "sla_coverage_both@test.com")
	AssignWorkspaceRole(t, server, bothID, workspaceID, "Administrator")
	bothCookie, _ := CreateAuthCredentialsForUser(t, server, bothName, bothPass)

	teamResp := MakeAuthRequest(t, server, http.MethodPost, "/teams", map[string]any{"name": "Coverage Team"})
	AssertStatusCode(t, teamResp, http.StatusCreated)
	var team map[string]any
	DecodeJSON(t, teamResp, &team)
	_ = teamResp.Body.Close()
	teamID := int(team["id"].(float64))
	addResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/teams/%d/members", teamID), map[string]any{"user_ids": []int{bothID}, "role": "admin"})
	AssertStatusCode(t, addResp, http.StatusOK)
	_ = addResp.Body.Close()

	// Bind the team so its service hours are the reference.
	bind := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodPost, fmt.Sprintf("/workspaces/%d/team-bindings", workspaceID), map[string]any{"team_id": teamID})
	AssertStatusCode(t, bind, http.StatusCreated)
	_ = bind.Body.Close()

	// The team works mornings only; the SLA counts 24/7.
	teamCalResp := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodPost, fmt.Sprintf("/teams/%d/working-calendars", teamID), map[string]any{
		"name":     "Morning hours",
		"timezone": "UTC",
		"weekly_intervals": map[string]any{
			"monday":    []map[string]string{{"start": "09:00", "end": "17:00"}},
			"tuesday":   []map[string]string{{"start": "09:00", "end": "17:00"}},
			"wednesday": []map[string]string{{"start": "09:00", "end": "17:00"}},
			"thursday":  []map[string]string{{"start": "09:00", "end": "17:00"}},
			"friday":    []map[string]string{{"start": "09:00", "end": "17:00"}},
		},
	})
	AssertStatusCode(t, teamCalResp, http.StatusCreated)
	_ = teamCalResp.Body.Close()

	wsCalResp := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/calendars", workspaceID), map[string]any{"name": "Always open", "timezone": "UTC"})
	AssertStatusCode(t, wsCalResp, http.StatusCreated)
	var wsCalendar models.WorkingCalendar
	DecodeJSON(t, wsCalResp, &wsCalendar)
	_ = wsCalResp.Body.Close()

	metricPayload := map[string]any{
		"name":           "Coverage metric",
		"display_format": "time",
		"is_active":      true,
		"conditions": []map[string]any{{
			"phase": "start", "position": 0, "condition_type": "created",
			"config": map[string]any{},
		}},
		"goals": []map[string]any{{
			"position": 0, "ql_query": "1 = 1", "import_status": "native",
			"targets": []map[string]any{{
				"position": 0, "target_ms": 3_600_000, "calendar_id": wsCalendar.ID,
			}},
		}},
	}
	metricResp := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/metrics", workspaceID), metricPayload)
	AssertStatusCode(t, metricResp, http.StatusCreated)
	var metric models.SLAMetric
	DecodeJSON(t, metricResp, &metric)
	_ = metricResp.Body.Close()

	itemID := CreateTestItem(t, server, workspaceID, "Coverage item")

	// Item SLA state carries the coverage block.
	itemResp := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodGet, fmt.Sprintf("/items/%d/sla", itemID), nil)
	AssertStatusCode(t, itemResp, http.StatusOK)
	var states []models.ItemSLA
	DecodeJSON(t, itemResp, &states)
	_ = itemResp.Body.Close()
	if len(states) != 1 || states[0].Ongoing == nil || states[0].Ongoing.Coverage == nil {
		t.Fatalf("item SLA coverage = %#v", states)
	}
	coverage := states[0].Ongoing.Coverage
	if coverage.Reference != "team_service_hours" || coverage.Discrepancy != "sla_wider" {
		t.Fatalf("item coverage = %#v", coverage)
	}

	// The report aggregates coverage from stored snapshots.
	reportResp := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodGet, fmt.Sprintf("/workspaces/%d/sla/report", workspaceID), nil)
	AssertStatusCode(t, reportResp, http.StatusOK)
	var report models.SLAReport
	DecodeJSON(t, reportResp, &report)
	_ = reportResp.Body.Close()
	if report.Coverage == nil || report.Coverage.Reference != "team_service_hours" || !report.Coverage.CurrentStateReference {
		t.Fatalf("report coverage = %#v", report.Coverage)
	}

	// The preview compares a candidate scheduling calendar with the reference.
	previewResp := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodGet, fmt.Sprintf("/workspaces/%d/sla/coverage-preview?calendar_id=%d", workspaceID, wsCalendar.ID), nil)
	AssertStatusCode(t, previewResp, http.StatusOK)
	var preview models.SLACoverage
	DecodeJSON(t, previewResp, &preview)
	_ = previewResp.Body.Close()
	if preview.Reference != "team_service_hours" || preview.Discrepancy != "sla_wider" || preview.SLAOutsideTeamWeeklyMs == 0 {
		t.Fatalf("preview coverage = %#v", preview)
	}

	// An unknown calendar is a client error, not a silent empty block.
	missing := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodGet, fmt.Sprintf("/workspaces/%d/sla/coverage-preview?calendar_id=%d", workspaceID, 999_999), nil)
	AssertStatusCode(t, missing, http.StatusNotFound)
	_ = missing.Body.Close()
}
