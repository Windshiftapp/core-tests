//go:build test

package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/testutils"
)

// On-call layer members and overrides decide who gets paged, so they must be
// scoped to the schedule's team (WI-1808). These tests drive the handlers with
// a team admin and a user outside the team.
type onCallMemberScopeFixture struct {
	tdb        *testutils.TestDB
	handler    *OnCallHandler
	teamID     int
	managerID  int
	memberID   int
	outsiderID int
	scheduleID int
	layerID    int
}

func newOnCallMemberScopeFixture(t *testing.T) *onCallMemberScopeFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	setup := newServiceSetup(t, tdb)

	managerID := setup.CreateUser("oncall-manager@example.test", "oncall-manager", "OnCall", "Manager")
	memberID := setup.CreateUser("oncall-member@example.test", "oncall-member", "OnCall", "Member")
	outsiderID := setup.CreateUser("oncall-outsider@example.test", "oncall-outsider", "OnCall", "Outsider")

	teamID := testutils.InsertID(t, tdb.GetDatabase(), `INSERT INTO teams (name, description, created_by) VALUES (?, '', ?)`, "Member Scope Team", managerID)
	if _, err := tdb.ExecWrite(`INSERT INTO team_members (team_id, user_id, role) VALUES (?, ?, 'admin')`, teamID, managerID); err != nil {
		t.Fatalf("add team admin: %v", err)
	}
	if _, err := tdb.ExecWrite(`INSERT INTO team_members (team_id, user_id, role) VALUES (?, ?, 'member')`, teamID, memberID); err != nil {
		t.Fatalf("add team member: %v", err)
	}

	scheduleID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO on_call_schedules (team_id, name, description, timezone, created_by)
		VALUES (?, 'Member Scope Schedule', '', 'UTC', ?)
	`, teamID, managerID)
	layerID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO on_call_schedule_layers
			(schedule_id, name, priority, rotation_type, rotation_interval_days, handoff_time, start_date)
		VALUES (?, 'Primary', 1, 'daily', 1, '00:00', '2026-01-01')
	`, scheduleID)

	handler, permissionService := newOnCallAuthorizationHandler(t, tdb)
	t.Cleanup(func() { _ = permissionService.Close() })

	return &onCallMemberScopeFixture{
		tdb:        tdb,
		handler:    handler,
		teamID:     teamID,
		managerID:  managerID,
		memberID:   memberID,
		outsiderID: outsiderID,
		scheduleID: scheduleID,
		layerID:    layerID,
	}
}

func (f *onCallMemberScopeFixture) setLayerMembersRequest(t *testing.T, memberIDs []int) *http.Request {
	t.Helper()
	req := testutils.CreateJSONRequest(t, http.MethodPut, "/api/on-call/schedules/layers/members", map[string]any{
		"user_ids": memberIDs,
	})
	req.SetPathValue("scheduleId", fmt.Sprint(f.scheduleID))
	req.SetPathValue("layerId", fmt.Sprint(f.layerID))
	return testutils.WithAuthContext(req, testutils.TestUserWithID(f.managerID))
}

func (f *onCallMemberScopeFixture) createOverrideRequest(t *testing.T, userID, overrideUserID int) *http.Request {
	t.Helper()
	req := testutils.CreateJSONRequest(t, http.MethodPost, "/api/on-call/schedules/overrides", map[string]any{
		"user_id":          userID,
		"override_user_id": overrideUserID,
		"start_time":       "2026-02-01T00:00:00Z",
		"end_time":         "2026-02-02T00:00:00Z",
		"reason":           "coverage",
	})
	req.SetPathValue("id", fmt.Sprint(f.scheduleID))
	return testutils.WithAuthContext(req, testutils.TestUserWithID(f.managerID))
}

func TestSetLayerMembersRejectsUserOutsideTeam(t *testing.T) {
	f := newOnCallMemberScopeFixture(t)

	rr := testutils.ExecuteRequest(t, f.handler.SetLayerMembers, f.setLayerMembersRequest(t, []int{f.outsiderID}))
	rr.AssertStatusCode(http.StatusBadRequest)
	rr.AssertBodyContains("must be members of this team")

	var count int
	if err := f.tdb.QueryRow(`SELECT COUNT(*) FROM on_call_schedule_layer_members WHERE layer_id = ?`, f.layerID).Scan(&count); err != nil {
		t.Fatalf("count layer members: %v", err)
	}
	if count != 0 {
		t.Fatalf("layer member count = %d, want 0 after rejected write", count)
	}
}

func TestSetLayerMembersAcceptsTeamMember(t *testing.T) {
	f := newOnCallMemberScopeFixture(t)

	rr := testutils.ExecuteRequest(t, f.handler.SetLayerMembers, f.setLayerMembersRequest(t, []int{f.memberID}))
	rr.AssertStatusCode(http.StatusOK)

	var count int
	if err := f.tdb.QueryRow(`SELECT COUNT(*) FROM on_call_schedule_layer_members WHERE layer_id = ?`, f.layerID).Scan(&count); err != nil {
		t.Fatalf("count layer members: %v", err)
	}
	if count != 1 {
		t.Fatalf("layer member count = %d, want 1", count)
	}
}

func TestListSchedulesDeniesUserOutsideTeam(t *testing.T) {
	f := newOnCallMemberScopeFixture(t)

	listRequest := func(userID int) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/api/teams/"+fmt.Sprint(f.teamID)+"/on-call/schedules", nil)
		req.SetPathValue("id", fmt.Sprint(f.teamID))
		return testutils.WithAuthContext(req, testutils.TestUserWithID(userID))
	}

	outsider := testutils.ExecuteRequest(t, f.handler.ListSchedules, listRequest(f.outsiderID))
	outsider.AssertStatusCode(http.StatusForbidden)
	if strings.Contains(outsider.Body.String(), "Member Scope Schedule") {
		t.Fatalf("forbidden list leaked schedule: %s", outsider.Body.String())
	}

	member := testutils.ExecuteRequest(t, f.handler.ListSchedules, listRequest(f.memberID))
	member.AssertStatusCode(http.StatusOK)
	member.AssertBodyContains("Member Scope Schedule")
}

func TestCreateOverrideRejectsUserOutsideTeam(t *testing.T) {
	f := newOnCallMemberScopeFixture(t)

	rr := testutils.ExecuteRequest(t, f.handler.CreateOverride, f.createOverrideRequest(t, f.memberID, f.outsiderID))
	rr.AssertStatusCode(http.StatusBadRequest)
	rr.AssertBodyContains("not a member of this team")

	var count int
	if err := f.tdb.QueryRow(`SELECT COUNT(*) FROM on_call_schedule_overrides WHERE schedule_id = ?`, f.scheduleID).Scan(&count); err != nil {
		t.Fatalf("count overrides: %v", err)
	}
	if count != 0 {
		t.Fatalf("override count = %d, want 0 after rejected write", count)
	}
}
