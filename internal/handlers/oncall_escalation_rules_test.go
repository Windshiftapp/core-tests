//go:build test

package handlers

import (
	"net/http"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// TestSetRulesRejectsCrossTeamTargets covers the cross-team IDOR: a team admin
// may manage their own policy, but no escalation rule may point at another
// team's team, on-call schedule, or users.
func TestSetRulesRejectsCrossTeamTargets(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	setup := newServiceSetup(t, tdb)

	adminAID := setup.CreateUser("escalation-admin-a@example.test", "escalation-admin-a", "Admin", "A")
	adminBID := setup.CreateUser("escalation-admin-b@example.test", "escalation-admin-b", "Admin", "B")
	memberAID := setup.CreateUser("escalation-member-a@example.test", "escalation-member-a", "Member", "A")

	teamAID := testutils.InsertID(t, tdb.GetDatabase(), `INSERT INTO teams (name, description, created_by) VALUES (?, '', ?)`, "Escalation Team A", adminAID)
	teamBID := testutils.InsertID(t, tdb.GetDatabase(), `INSERT INTO teams (name, description, created_by) VALUES (?, '', ?)`, "Escalation Team B", adminBID)
	if _, err := tdb.ExecWrite(`INSERT INTO team_members (team_id, user_id, role) VALUES (?, ?, 'admin')`, teamAID, adminAID); err != nil {
		t.Fatalf("add team A admin: %v", err)
	}
	if _, err := tdb.ExecWrite(`INSERT INTO team_members (team_id, user_id, role) VALUES (?, ?, 'admin')`, teamBID, adminBID); err != nil {
		t.Fatalf("add team B admin: %v", err)
	}
	if _, err := tdb.ExecWrite(`INSERT INTO team_members (team_id, user_id, role) VALUES (?, ?, 'member')`, teamAID, memberAID); err != nil {
		t.Fatalf("add team A member: %v", err)
	}

	teamBScheduleID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO on_call_schedules (team_id, name, description, timezone, created_by)
		VALUES (?, 'Team B Schedule', '', 'UTC', ?)
	`, teamBID, adminBID)
	policyAID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO on_call_escalation_policies (team_id, name, description, repeat_count, created_by)
		VALUES (?, 'Team A Policy', '', 0, ?)
	`, teamAID, adminAID)

	handler, permissionService := newOnCallAuthorizationHandler(t, tdb)
	t.Cleanup(func() { _ = permissionService.Close() })

	setRules := func(rules []models.EscalationRuleInput) *testutils.ResponseRecorder {
		t.Helper()
		req := testutils.CreateJSONRequest(t, http.MethodPut,
			"/api/on-call/escalation-policies/1/rules",
			models.SetEscalationRulesRequest{Rules: rules})
		req.SetPathValue("id", testutils.IntToString(policyAID))
		return testutils.ExecuteAuthenticatedRequest(t, handler.SetRules, req, testutils.TestUserWithID(adminAID))
	}

	t.Run("cross_team_team_target", func(t *testing.T) {
		recorder := setRules([]models.EscalationRuleInput{{StepOrder: 1, TargetType: "team", TargetID: teamBID}})
		recorder.AssertStatusCode(http.StatusBadRequest)
		assertEscalationRuleCount(t, tdb, policyAID, 0)
	})

	t.Run("cross_team_schedule_target", func(t *testing.T) {
		recorder := setRules([]models.EscalationRuleInput{{StepOrder: 1, TargetType: "on_call_schedule", TargetID: teamBScheduleID}})
		recorder.AssertStatusCode(http.StatusBadRequest)
		assertEscalationRuleCount(t, tdb, policyAID, 0)
	})

	t.Run("non_member_user_target", func(t *testing.T) {
		recorder := setRules([]models.EscalationRuleInput{{StepOrder: 1, TargetType: "user", TargetID: adminBID}})
		recorder.AssertStatusCode(http.StatusBadRequest)
		assertEscalationRuleCount(t, tdb, policyAID, 0)
	})

	t.Run("unknown_target_type", func(t *testing.T) {
		recorder := setRules([]models.EscalationRuleInput{{StepOrder: 1, TargetType: "everyone", TargetID: teamAID}})
		recorder.AssertStatusCode(http.StatusBadRequest)
		assertEscalationRuleCount(t, tdb, policyAID, 0)
	})

	t.Run("own_team_and_member_targets", func(t *testing.T) {
		recorder := setRules([]models.EscalationRuleInput{
			{StepOrder: 1, TargetType: "team", TargetID: teamAID},
			{StepOrder: 2, TargetType: "user", TargetID: memberAID},
		})
		recorder.AssertStatusCode(http.StatusOK)
		assertEscalationRuleCount(t, tdb, policyAID, 2)
	})
}

// TestGetPolicyRequiresTeamAccess covers the read gate on escalation policies:
// an unrelated user must not read a policy's rules and targets by id.
func TestGetPolicyRequiresTeamAccess(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	setup := newServiceSetup(t, tdb)

	adminID := setup.CreateUser("policy-read-admin@example.test", "policy-read-admin", "Policy", "Admin")
	memberID := setup.CreateUser("policy-read-member@example.test", "policy-read-member", "Policy", "Member")
	outsiderID := setup.CreateUser("policy-read-outsider@example.test", "policy-read-outsider", "Policy", "Outsider")

	teamID := testutils.InsertID(t, tdb.GetDatabase(), `INSERT INTO teams (name, description, created_by) VALUES (?, '', ?)`, "Policy Read Team", adminID)
	if _, err := tdb.ExecWrite(`INSERT INTO team_members (team_id, user_id, role) VALUES (?, ?, 'admin')`, teamID, adminID); err != nil {
		t.Fatalf("add team admin: %v", err)
	}
	if _, err := tdb.ExecWrite(`INSERT INTO team_members (team_id, user_id, role) VALUES (?, ?, 'member')`, teamID, memberID); err != nil {
		t.Fatalf("add team member: %v", err)
	}
	policyID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO on_call_escalation_policies (team_id, name, description, repeat_count, created_by)
		VALUES (?, 'Readable Policy', '', 0, ?)
	`, teamID, adminID)

	handler, permissionService := newOnCallAuthorizationHandler(t, tdb)
	t.Cleanup(func() { _ = permissionService.Close() })
	requestFor := func(userID int) *http.Request {
		t.Helper()
		req := testutils.CreateJSONRequest(t, http.MethodGet, "/api/on-call/escalation-policies/1", nil)
		req.SetPathValue("id", testutils.IntToString(policyID))
		return testutils.WithAuthContext(req, testutils.TestUserWithID(userID))
	}

	memberRecorder := testutils.ExecuteRequest(t, handler.GetPolicy, requestFor(memberID))
	memberRecorder.AssertStatusCode(http.StatusOK)

	outsiderRecorder := testutils.ExecuteRequest(t, handler.GetPolicy, requestFor(outsiderID))
	outsiderRecorder.AssertStatusCode(http.StatusForbidden)
}

func assertEscalationRuleCount(t *testing.T, tdb *testutils.TestDB, policyID, want int) {
	t.Helper()
	var count int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM on_call_escalation_rules WHERE policy_id = ?`, policyID).Scan(&count); err != nil {
		t.Fatalf("count escalation rules: %v", err)
	}
	if count != want {
		t.Fatalf("escalation rule count = %d, want %d", count, want)
	}
}
