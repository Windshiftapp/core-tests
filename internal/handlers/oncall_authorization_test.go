//go:build test

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func TestGetScheduleDeniesRosterAccessToUnrelatedUser(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	setup := newServiceSetup(t, tdb)
	memberID := setup.CreateUser("schedule-member@example.test", "schedule-member", "Schedule", "Member")
	outsiderID := setup.CreateUser("schedule-outsider@example.test", "schedule-outsider", "Schedule", "Outsider")
	teamID := testutils.InsertID(t, tdb.GetDatabase(), `INSERT INTO teams (name, description, created_by) VALUES (?, '', ?)`, "Private Roster", memberID)
	if _, err := tdb.ExecWrite(`INSERT INTO team_members (team_id, user_id, role) VALUES (?, ?, 'member')`, teamID, memberID); err != nil {
		t.Fatalf("add team member: %v", err)
	}
	scheduleID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO on_call_schedules (team_id, name, description, timezone, created_by)
		VALUES (?, 'Private Schedule', '', 'UTC', ?)
	`, teamID, memberID)
	layerID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO on_call_schedule_layers
			(schedule_id, name, priority, rotation_type, rotation_interval_days, handoff_time, start_date)
		VALUES (?, 'Primary', 1, 'daily', 1, '00:00', '2026-01-01')
	`, scheduleID)
	if _, err := tdb.ExecWrite(`INSERT INTO on_call_schedule_layer_members (layer_id, user_id, position) VALUES (?, ?, 1)`, layerID, memberID); err != nil {
		t.Fatalf("add schedule member: %v", err)
	}

	handler, permissionService := newOnCallAuthorizationHandler(t, tdb)
	t.Cleanup(func() { _ = permissionService.Close() })
	requestFor := func(userID int) *http.Request {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/on-call/schedules/%d", scheduleID), nil)
		req.SetPathValue("id", fmt.Sprintf("%d", scheduleID))
		return testutils.WithAuthContext(req, testutils.TestUserWithID(userID))
	}

	memberRecorder := testutils.ExecuteRequest(t, handler.GetSchedule, requestFor(memberID))
	memberRecorder.AssertStatusCode(http.StatusOK)
	if !strings.Contains(memberRecorder.Body.String(), "schedule-member@example.test") {
		t.Fatalf("authorized schedule response omitted roster member: %s", memberRecorder.Body.String())
	}

	outsiderRecorder := testutils.ExecuteRequest(t, handler.GetSchedule, requestFor(outsiderID))
	outsiderRecorder.AssertStatusCode(http.StatusForbidden)
	if strings.Contains(outsiderRecorder.Body.String(), "schedule-member@example.test") {
		t.Fatalf("forbidden response leaked roster member: %s", outsiderRecorder.Body.String())
	}
}

func TestListIncidentsFiltersUnauthorizedTeamsAndItemWorkspaces(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	setup := newServiceSetup(t, tdb)
	viewerID := setup.CreateUser("incident-viewer@example.test", "incident-viewer", "Incident", "Viewer")
	otherID := setup.CreateUser("incident-other@example.test", "incident-other", "Incident", "Other")

	visibleTeamID := testutils.InsertID(t, tdb.GetDatabase(), `INSERT INTO teams (name, description, created_by) VALUES (?, '', ?)`, "Visible Incident Team", viewerID)
	hiddenTeamID := testutils.InsertID(t, tdb.GetDatabase(), `INSERT INTO teams (name, description, created_by) VALUES (?, '', ?)`, "Hidden Incident Team", otherID)
	if _, err := tdb.ExecWrite(`INSERT INTO team_members (team_id, user_id, role) VALUES (?, ?, 'member')`, visibleTeamID, viewerID); err != nil {
		t.Fatalf("add visible team member: %v", err)
	}

	visibleWorkspaceID := setup.CreateWorkspace("Visible Incident Workspace", "VIW")
	hiddenWorkspaceID := setup.CreateWorkspace("Hidden Incident Workspace", "HIW")
	grantViewerRoleForAuthorizationTest(t, tdb, viewerID, visibleWorkspaceID)
	grantViewerRoleForAuthorizationTest(t, tdb, otherID, hiddenWorkspaceID)

	visibleItemID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO items (workspace_id, workspace_item_number, title, description, creator_id, frac_index)
		VALUES (?, 1, 'Visible incident item', '', ?, ?)
	`, visibleWorkspaceID, viewerID, testutils.NextTestFracIndex())
	hiddenItemID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO items (workspace_id, workspace_item_number, title, description, creator_id, frac_index)
		VALUES (?, 1, 'Restricted incident item', '', ?, ?)
	`, hiddenWorkspaceID, otherID, testutils.NextTestFracIndex())

	visiblePolicyID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO on_call_escalation_policies (team_id, name, description, repeat_count, created_by)
		VALUES (?, 'Visible policy', '', 0, ?)
	`, visibleTeamID, viewerID)
	hiddenPolicyID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO on_call_escalation_policies (team_id, name, description, repeat_count, created_by)
		VALUES (?, 'Hidden policy', '', 0, ?)
	`, hiddenTeamID, otherID)

	// Incidents currently have no production create API, so this handler-level
	// authorization fixture inserts the legacy rows the endpoint can expose.
	if _, err := tdb.ExecWrite(`INSERT INTO on_call_incidents (escalation_policy_id, item_id) VALUES (?, ?)`, visiblePolicyID, visibleItemID); err != nil {
		t.Fatalf("insert visible incident: %v", err)
	}
	if _, err := tdb.ExecWrite(`INSERT INTO on_call_incidents (escalation_policy_id, item_id) VALUES (?, ?)`, hiddenPolicyID, visibleItemID); err != nil {
		t.Fatalf("insert hidden-team incident: %v", err)
	}
	if _, err := tdb.ExecWrite(`INSERT INTO on_call_incidents (escalation_policy_id, item_id) VALUES (?, ?)`, visiblePolicyID, hiddenItemID); err != nil {
		t.Fatalf("insert hidden-item incident: %v", err)
	}

	handler, permissionService := newOnCallAuthorizationHandler(t, tdb)
	t.Cleanup(func() { _ = permissionService.Close() })
	req := testutils.WithAuthContext(
		httptest.NewRequest(http.MethodGet, "/api/on-call/incidents", nil),
		testutils.TestUserWithID(viewerID),
	)
	recorder := testutils.ExecuteRequest(t, handler.ListIncidents, req)
	recorder.AssertStatusCode(http.StatusOK)

	var incidents []models.OnCallIncident
	if err := json.NewDecoder(recorder.Body).Decode(&incidents); err != nil {
		t.Fatalf("decode incidents: %v", err)
	}
	if len(incidents) != 1 || incidents[0].EscalationPolicyID != visiblePolicyID || incidents[0].ItemTitle != "Visible incident item" {
		t.Fatalf("incidents = %+v, want only the authorized team and item", incidents)
	}
}

func newOnCallAuthorizationHandler(t *testing.T, tdb *testutils.TestDB) (*OnCallHandler, *services.PermissionService) {
	t.Helper()
	permissionService, err := services.NewPermissionService(tdb.GetDatabase(), services.PermissionCacheConfig{
		TTL: 0, MaxCacheSize: 8, WarmupOnStartup: false, PreWarmActive: false, BatchSize: 10,
	})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	repo := repository.NewOnCallRepository(tdb.GetDatabase())
	return NewOnCallHandler(
		repo,
		repository.NewTeamRepository(tdb.GetDatabase()),
		services.NewOnCallService(tdb.GetDatabase(), repo, repository.NewLeaveRepository(tdb.GetDatabase())),
		permissionService,
		logger.NewAuditor(tdb.GetDatabase()),
	), permissionService
}

func grantViewerRoleForAuthorizationTest(t *testing.T, tdb *testutils.TestDB, userID, workspaceID int) {
	t.Helper()
	var roleID int
	if err := tdb.QueryRow(`SELECT id FROM workspace_roles WHERE name = 'Viewer'`).Scan(&roleID); err != nil {
		t.Fatalf("find Viewer role: %v", err)
	}
	if _, err := tdb.ExecWrite(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_by)
		VALUES (?, ?, ?, 1)
	`, userID, workspaceID, roleID); err != nil {
		t.Fatalf("grant Viewer role: %v", err)
	}
}
