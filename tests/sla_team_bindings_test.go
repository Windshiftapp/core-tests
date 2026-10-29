//go:build test

package tests

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/models"
)

// TestSLATeamBindingWorkspaceLifecycle exercises the internal /api binding
// surface: both-sides consent, duplicate handling, joined names, workspace
// scoping, and the delete refusal while an SLA goal target references the
// team's calendar.
func TestSLATeamBindingWorkspaceLifecycle(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Binding Workspace", shortKey("SLB"))
	otherWorkspaceID, _ := CreateTestWorkspace(t, server, "SLA Binding Other", shortKey("SLO"))

	// `both` holds workspace admin and team admin, so only they can consent.
	bothID, bothName, bothPass := CreateTestUserWithCredentials(t, server, "sla_binding_both", "sla_binding_both@test.com")
	AssignWorkspaceRole(t, server, bothID, workspaceID, "Administrator")
	bothCookie, _ := CreateAuthCredentialsForUser(t, server, bothName, bothPass)

	// `wsOnly` is a workspace admin with no team authority.
	wsOnlyID, wsOnlyName, wsOnlyPass := CreateTestUserWithCredentials(t, server, "sla_binding_ws", "sla_binding_ws@test.com")
	AssignWorkspaceRole(t, server, wsOnlyID, workspaceID, "Administrator")
	wsOnlyCookie, _ := CreateAuthCredentialsForUser(t, server, wsOnlyName, wsOnlyPass)

	teamResp := MakeAuthRequest(t, server, http.MethodPost, "/teams", map[string]any{"name": "SLA Binding Team"})
	AssertStatusCode(t, teamResp, http.StatusCreated)
	var team map[string]any
	DecodeJSON(t, teamResp, &team)
	_ = teamResp.Body.Close()
	teamID := int(team["id"].(float64))

	addResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/teams/%d/members", teamID), map[string]any{"user_ids": []int{bothID}, "role": "admin"})
	AssertStatusCode(t, addResp, http.StatusOK)
	_ = addResp.Body.Close()

	// Workspace admin without team admin cannot create the binding.
	denied := MakeAuthRequestWithToken(t, server, wsOnlyCookie, http.MethodPost, fmt.Sprintf("/workspaces/%d/team-bindings", workspaceID), map[string]any{"team_id": teamID})
	AssertStatusCode(t, denied, http.StatusForbidden)
	_ = denied.Body.Close()

	// Both sides consented: create succeeds and returns joined names.
	create := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodPost, fmt.Sprintf("/workspaces/%d/team-bindings", workspaceID), map[string]any{"team_id": teamID})
	AssertStatusCode(t, create, http.StatusCreated)
	var binding models.TeamWorkspaceBinding
	DecodeJSON(t, create, &binding)
	_ = create.Body.Close()
	if binding.ID == 0 || binding.TeamID != teamID || binding.WorkspaceID != workspaceID {
		t.Fatalf("created binding = %#v", binding)
	}
	if binding.TeamName != "SLA Binding Team" || binding.WorkspaceName != "SLA Binding Workspace" {
		t.Fatalf("binding names = team %q workspace %q", binding.TeamName, binding.WorkspaceName)
	}

	// The same pair cannot be bound twice.
	dup := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodPost, fmt.Sprintf("/workspaces/%d/team-bindings", workspaceID), map[string]any{"team_id": teamID})
	AssertStatusCode(t, dup, http.StatusConflict)
	_ = dup.Body.Close()

	// Workspace-side list.
	list := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodGet, fmt.Sprintf("/workspaces/%d/team-bindings", workspaceID), nil)
	AssertStatusCode(t, list, http.StatusOK)
	var bindings []models.TeamWorkspaceBinding
	DecodeJSON(t, list, &bindings)
	_ = list.Body.Close()
	if len(bindings) != 1 || bindings[0].ID != binding.ID {
		t.Fatalf("workspace bindings = %#v", bindings)
	}

	// Team-side list.
	teamList := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodGet, fmt.Sprintf("/teams/%d/workspace-bindings", teamID), nil)
	AssertStatusCode(t, teamList, http.StatusOK)
	var teamBindings []models.TeamWorkspaceBinding
	DecodeJSON(t, teamList, &teamBindings)
	_ = teamList.Body.Close()
	if len(teamBindings) != 1 || teamBindings[0].ID != binding.ID {
		t.Fatalf("team bindings = %#v", teamBindings)
	}

	// A binding cannot be deleted through another workspace's route.
	crossDelete := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/workspaces/%d/team-bindings/%d", otherWorkspaceID, binding.ID), nil)
	AssertStatusCode(t, crossDelete, http.StatusNotFound)
	_ = crossDelete.Body.Close()

	// The binding authorizes the team calendar for an SLA goal target.
	calResp := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodPost, fmt.Sprintf("/teams/%d/working-calendars", teamID), map[string]any{"name": "Team hours", "timezone": "UTC"})
	AssertStatusCode(t, calResp, http.StatusCreated)
	var calendar models.WorkingCalendar
	DecodeJSON(t, calResp, &calendar)
	_ = calResp.Body.Close()

	metricPayload := map[string]any{
		"name":           "Bound calendar metric",
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
	metricResp := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/metrics", workspaceID), metricPayload)
	AssertStatusCode(t, metricResp, http.StatusCreated)
	var metric models.SLAMetric
	DecodeJSON(t, metricResp, &metric)
	_ = metricResp.Body.Close()

	inUse := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodDelete, fmt.Sprintf("/workspaces/%d/team-bindings/%d", workspaceID, binding.ID), nil)
	AssertStatusCode(t, inUse, http.StatusConflict)
	_ = inUse.Body.Close()

	// Removing the referencing metric releases the binding.
	delMetric := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodDelete, fmt.Sprintf("/workspaces/%d/sla/metrics/%d", workspaceID, metric.ID), nil)
	AssertStatusCode(t, delMetric, http.StatusOK)
	_ = delMetric.Body.Close()

	del := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodDelete, fmt.Sprintf("/workspaces/%d/team-bindings/%d", workspaceID, binding.ID), nil)
	AssertStatusCode(t, del, http.StatusOK)
	_ = del.Body.Close()

	after := MakeAuthRequestWithToken(t, server, bothCookie, http.MethodGet, fmt.Sprintf("/workspaces/%d/team-bindings", workspaceID), nil)
	AssertStatusCode(t, after, http.StatusOK)
	var remaining []models.TeamWorkspaceBinding
	DecodeJSON(t, after, &remaining)
	_ = after.Body.Close()
	if len(remaining) != 0 {
		t.Fatalf("bindings after delete = %#v", remaining)
	}
}

// TestSLATeamBindingAllowsSystemAdmin verifies a global administrator can bind
// a team without holding team admin or workspace admin, since global admin
// already confers authority over both sides.
func TestSLATeamBindingAllowsSystemAdmin(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "System Admin Binding", shortKey("SAB"))

	adminID, adminName, adminPass := CreateTestUserWithCredentials(t, server, "sla_binding_sysadmin", "sla_binding_sysadmin@test.com")
	GrantGlobalPermission(t, server, adminID, "system.admin")
	adminCookie, _ := CreateAuthCredentialsForUser(t, server, adminName, adminPass)

	teamResp := MakeAuthRequest(t, server, http.MethodPost, "/teams", map[string]any{"name": "System Admin Team"})
	AssertStatusCode(t, teamResp, http.StatusCreated)
	var team map[string]any
	DecodeJSON(t, teamResp, &team)
	_ = teamResp.Body.Close()
	teamID := int(team["id"].(float64))

	// The system admin is deliberately not a team member or workspace member.
	create := MakeAuthRequestWithToken(t, server, adminCookie, http.MethodPost, fmt.Sprintf("/workspaces/%d/team-bindings", workspaceID), map[string]any{"team_id": teamID})
	AssertStatusCode(t, create, http.StatusCreated)
	var binding models.TeamWorkspaceBinding
	DecodeJSON(t, create, &binding)
	_ = create.Body.Close()
	if binding.ID == 0 || binding.TeamID != teamID || binding.WorkspaceID != workspaceID {
		t.Fatalf("system-admin binding = %#v", binding)
	}

	// The same global authority can remove the binding again.
	del := MakeAuthRequestWithToken(t, server, adminCookie, http.MethodDelete, fmt.Sprintf("/workspaces/%d/team-bindings/%d", workspaceID, binding.ID), nil)
	AssertStatusCode(t, del, http.StatusOK)
	_ = del.Body.Close()
}

// TestTeamDeleteRefusesWhenCalendarReferenced asserts that deleting a team
// whose calendar backs a workspace SLA returns a clean conflict instead of a
// raw foreign-key failure.
func TestTeamDeleteRefusesWhenCalendarReferenced(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Team Delete Workspace", shortKey("STD"))

	setupID, setupName, setupPass := CreateTestUserWithCredentials(t, server, "sla_team_delete", "sla_team_delete@test.com")
	AssignWorkspaceRole(t, server, setupID, workspaceID, "Administrator")
	setupCookie, _ := CreateAuthCredentialsForUser(t, server, setupName, setupPass)

	teamResp := MakeAuthRequest(t, server, http.MethodPost, "/teams", map[string]any{"name": "SLA Team Delete"})
	AssertStatusCode(t, teamResp, http.StatusCreated)
	var team map[string]any
	DecodeJSON(t, teamResp, &team)
	_ = teamResp.Body.Close()
	teamID := int(team["id"].(float64))

	addResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/teams/%d/members", teamID), map[string]any{"user_ids": []int{setupID}, "role": "admin"})
	AssertStatusCode(t, addResp, http.StatusOK)
	_ = addResp.Body.Close()

	bindingResp := MakeAuthRequestWithToken(t, server, setupCookie, http.MethodPost, fmt.Sprintf("/workspaces/%d/team-bindings", workspaceID), map[string]any{"team_id": teamID})
	AssertStatusCode(t, bindingResp, http.StatusCreated)
	_ = bindingResp.Body.Close()

	calResp := MakeAuthRequestWithToken(t, server, setupCookie, http.MethodPost, fmt.Sprintf("/teams/%d/working-calendars", teamID), map[string]any{"name": "Team hours", "timezone": "UTC"})
	AssertStatusCode(t, calResp, http.StatusCreated)
	var calendar models.WorkingCalendar
	DecodeJSON(t, calResp, &calendar)
	_ = calResp.Body.Close()

	metricPayload := map[string]any{
		"name":           "Referenced",
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
	metricResp := MakeAuthRequestWithToken(t, server, setupCookie, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/metrics", workspaceID), metricPayload)
	AssertStatusCode(t, metricResp, http.StatusCreated)
	var metric models.SLAMetric
	DecodeJSON(t, metricResp, &metric)
	_ = metricResp.Body.Close()

	conflict := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/teams/%d", teamID), nil)
	AssertStatusCode(t, conflict, http.StatusConflict)
	_ = conflict.Body.Close()

	// A team with no referenced calendars still deletes cleanly.
	deleteMetric := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/workspaces/%d/sla/metrics/%d", workspaceID, metric.ID), nil)
	AssertStatusCode(t, deleteMetric, http.StatusOK)
	_ = deleteMetric.Body.Close()

	deleteTeam := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/teams/%d", teamID), nil)
	AssertStatusCode(t, deleteTeam, http.StatusNoContent)
	_ = deleteTeam.Body.Close()
}

// TestV2SLATeamBindingCRUD covers the workspace-scoped v2 surface and the
// admin denial.
func TestV2SLATeamBindingCRUD(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "V2 Binding Workspace", shortKey("VBW"))

	bothID, bothName, bothPass := CreateTestUserWithCredentials(t, server, "v2_binding_both", "v2_binding_both@test.com")
	AssignWorkspaceRole(t, server, bothID, workspaceID, "Administrator")
	_, bearer := CreateAuthCredentialsForUser(t, server, bothName, bothPass)

	teamResp := MakeAuthRequest(t, server, http.MethodPost, "/teams", map[string]any{"name": "V2 Binding Team"})
	AssertStatusCode(t, teamResp, http.StatusCreated)
	var team map[string]any
	DecodeJSON(t, teamResp, &team)
	_ = teamResp.Body.Close()
	teamID := int(team["id"].(float64))

	addResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/teams/%d/members", teamID), map[string]any{"user_ids": []int{bothID}, "role": "admin"})
	AssertStatusCode(t, addResp, http.StatusOK)
	_ = addResp.Body.Close()

	create := MakeBearerRequestWithToken(t, server, bearer, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/team-bindings", workspaceID), map[string]any{"team_id": teamID})
	AssertStatusCode(t, create, http.StatusCreated)
	var binding models.TeamWorkspaceBinding
	DecodeJSON(t, create, &binding)
	_ = create.Body.Close()
	if binding.ID == 0 || binding.TeamID != teamID || binding.WorkspaceID != workspaceID {
		t.Fatalf("v2 binding = %#v", binding)
	}

	list := MakeBearerRequestWithToken(t, server, bearer, http.MethodGet, fmt.Sprintf("/rest/api/v2/workspaces/%d/team-bindings", workspaceID), nil)
	AssertStatusCode(t, list, http.StatusOK)
	var bindings []models.TeamWorkspaceBinding
	DecodeJSON(t, list, &bindings)
	_ = list.Body.Close()
	if len(bindings) != 1 || bindings[0].TeamName != "V2 Binding Team" {
		t.Fatalf("v2 bindings = %#v", bindings)
	}

	// A viewer cannot list bindings.
	viewerID, viewerName, viewerPass := CreateTestUserWithCredentials(t, server, "v2_binding_viewer", "v2_binding_viewer@test.com")
	AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")
	_, viewerBearer := CreateAuthCredentialsForUser(t, server, viewerName, viewerPass)
	denied := MakeBearerRequestWithToken(t, server, viewerBearer, http.MethodGet, fmt.Sprintf("/rest/api/v2/workspaces/%d/team-bindings", workspaceID), nil)
	if denied.StatusCode != http.StatusForbidden && denied.StatusCode != http.StatusNotFound {
		t.Fatalf("viewer binding list = %d, want denied (403/404)", denied.StatusCode)
	}
	_ = denied.Body.Close()

	del := MakeBearerRequestWithToken(t, server, bearer, http.MethodDelete, fmt.Sprintf("/rest/api/v2/workspaces/%d/team-bindings/%d", workspaceID, binding.ID), nil)
	AssertStatusCode(t, del, http.StatusNoContent)
	_ = del.Body.Close()
}
