//go:build test

package tests

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/models"
)

// TestSLACalendarImpactPreview covers the team and workspace impact endpoints:
// bound workspaces, referencing goal targets, ongoing cycle counts, workspace
// scoping, and the not-accessible case.
func TestSLACalendarImpactPreview(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)

	alphaID, _ := CreateTestWorkspace(t, server, "Impact Alpha", shortKey("IMA"))
	betaID, _ := CreateTestWorkspace(t, server, "Impact Beta", shortKey("IMB"))

	bothID, bothName, bothPass := CreateTestUserWithCredentials(t, server, "sla_impact_both", "sla_impact_both@test.com")
	AssignWorkspaceRole(t, server, bothID, alphaID, "Administrator")
	AssignWorkspaceRole(t, server, bothID, betaID, "Administrator")
	bothCookie, bothBearer := CreateAuthCredentialsForUser(t, server, bothName, bothPass)

	teamResp := MakeAuthRequest(t, server, http.MethodPost, "/teams", map[string]any{"name": "Impact Team"})
	AssertStatusCode(t, teamResp, http.StatusCreated)
	var team map[string]any
	DecodeJSON(t, teamResp, &team)
	_ = teamResp.Body.Close()
	teamID := int(team["id"].(float64))
	addResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/teams/%d/members", teamID), map[string]any{"user_ids": []int{bothID}, "role": "admin"})
	AssertStatusCode(t, addResp, http.StatusOK)
	_ = addResp.Body.Close()

	// Team calendar, bound to both workspaces.
	calResp := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodPost, fmt.Sprintf("/teams/%d/working-calendars", teamID), map[string]any{"name": "Impact hours", "timezone": "UTC"})
	AssertStatusCode(t, calResp, http.StatusCreated)
	var teamCalendar models.WorkingCalendar
	DecodeJSON(t, calResp, &teamCalendar)
	_ = calResp.Body.Close()

	for _, workspaceID := range []int{alphaID, betaID} {
		bind := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodPost, fmt.Sprintf("/workspaces/%d/team-bindings", workspaceID), map[string]any{"team_id": teamID})
		AssertStatusCode(t, bind, http.StatusCreated)
		_ = bind.Body.Close()
	}

	// A created-start metric in Alpha uses the team calendar and opens a cycle.
	metricPayload := map[string]any{
		"name":           "Impact response",
		"display_format": "time",
		"is_active":      true,
		"conditions": []map[string]any{{
			"phase": "start", "position": 0, "condition_type": "created",
			"config": map[string]any{},
		}},
		"goals": []map[string]any{{
			"position": 0, "ql_query": "1 = 1", "import_status": "native",
			"targets": []map[string]any{{
				"position": 0, "target_ms": 3_600_000, "calendar_id": teamCalendar.ID,
			}},
		}},
	}
	metricResp := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/metrics", alphaID), metricPayload)
	AssertStatusCode(t, metricResp, http.StatusCreated)
	_ = metricResp.Body.Close()
	CreateTestItem(t, server, alphaID, "Impact item")

	// Team-scoped preview lists both bound workspaces; only Alpha references it.
	teamImpactResp := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodGet, fmt.Sprintf("/teams/%d/working-calendars/%d/impact", teamID, teamCalendar.ID), nil)
	AssertStatusCode(t, teamImpactResp, http.StatusOK)
	var teamImpact models.SLACalendarImpact
	DecodeJSON(t, teamImpactResp, &teamImpact)
	_ = teamImpactResp.Body.Close()
	if teamImpact.CalendarID != teamCalendar.ID || len(teamImpact.Workspaces) != 2 {
		t.Fatalf("team impact = %#v", teamImpact)
	}
	byWorkspace := map[int]models.SLACalendarImpactWorkspace{}
	for _, workspace := range teamImpact.Workspaces {
		byWorkspace[workspace.WorkspaceID] = workspace
	}
	alpha := byWorkspace[alphaID]
	if len(alpha.GoalTargets) != 1 || alpha.GoalTargets[0].MetricID == 0 || alpha.OngoingCycles != 1 {
		t.Fatalf("alpha impact = %#v", alpha)
	}
	beta := byWorkspace[betaID]
	if len(beta.GoalTargets) != 0 || beta.OngoingCycles != 0 {
		t.Fatalf("beta impact = %#v", beta)
	}

	// Workspace-scoped preview hides the other bound workspace.
	wsImpactResp := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodGet, fmt.Sprintf("/workspaces/%d/sla/calendars/%d/impact", alphaID, teamCalendar.ID), nil)
	AssertStatusCode(t, wsImpactResp, http.StatusOK)
	var wsImpact models.SLACalendarImpact
	DecodeJSON(t, wsImpactResp, &wsImpact)
	_ = wsImpactResp.Body.Close()
	if len(wsImpact.Workspaces) != 1 || wsImpact.Workspaces[0].WorkspaceID != alphaID {
		t.Fatalf("workspace impact = %#v", wsImpact)
	}

	// The v2 workspace preview returns the same single-workspace shape.
	v2ImpactResp := MakeBearerRequestWithToken(t, server, bothBearer, http.MethodGet, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/calendars/%d/impact", alphaID, teamCalendar.ID), nil)
	AssertStatusCode(t, v2ImpactResp, http.StatusOK)
	var v2Impact models.SLACalendarImpact
	DecodeJSON(t, v2ImpactResp, &v2Impact)
	_ = v2ImpactResp.Body.Close()
	if len(v2Impact.Workspaces) != 1 || v2Impact.Workspaces[0].WorkspaceID != alphaID {
		t.Fatalf("v2 impact = %#v", v2Impact)
	}

	// A workspace-owned calendar in Alpha is invisible to Beta: 404.
	ownedResp := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/calendars", alphaID), map[string]any{"name": "Alpha only", "timezone": "UTC"})
	AssertStatusCode(t, ownedResp, http.StatusCreated)
	var ownedCalendar models.WorkingCalendar
	DecodeJSON(t, ownedResp, &ownedCalendar)
	_ = ownedResp.Body.Close()

	cross := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodGet, fmt.Sprintf("/workspaces/%d/sla/calendars/%d/impact", betaID, ownedCalendar.ID), nil)
	AssertStatusCode(t, cross, http.StatusNotFound)
	_ = cross.Body.Close()

	// Sanity: the owning workspace still sees it.
	ownImpact := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodGet, fmt.Sprintf("/workspaces/%d/sla/calendars/%d/impact", alphaID, ownedCalendar.ID), nil)
	AssertStatusCode(t, ownImpact, http.StatusOK)
	_ = ownImpact.Body.Close()
}
