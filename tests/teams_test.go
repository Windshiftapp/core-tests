package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// TestTeamOperations tests the full team lifecycle via HTTP
func TestTeamOperations(t *testing.T) {
	dbType := GetDBType()
	testServer, cleanup := StartTestServer(t, dbType)
	defer cleanup()

	CreateBearerToken(t, testServer)

	var teamID int

	t.Run("CreateTeam", func(t *testing.T) {
		body := map[string]interface{}{
			"name":        "Engineering",
			"description": "Engineering team",
		}
		resp := MakeAuthRequest(t, testServer, http.MethodPost, "/teams", body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)

		var team map[string]interface{}
		DecodeJSON(t, resp, &team)
		teamID = int(team["id"].(float64))

		if team["name"] != "Engineering" {
			t.Errorf("Expected name 'Engineering', got %v", team["name"])
		}
	})

	t.Run("GetTeam", func(t *testing.T) {
		resp := MakeAuthRequest(t, testServer, http.MethodGet, fmt.Sprintf("/teams/%d", teamID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var team map[string]interface{}
		DecodeJSON(t, resp, &team)
		if team["name"] != "Engineering" {
			t.Errorf("Expected name 'Engineering', got %v", team["name"])
		}
	})

	t.Run("ListTeams", func(t *testing.T) {
		resp := MakeAuthRequest(t, testServer, http.MethodGet, "/teams", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var teams []map[string]interface{}
		DecodeJSON(t, resp, &teams)
		if len(teams) < 1 {
			t.Error("Expected at least one team")
		}
	})

	t.Run("UpdateTeam", func(t *testing.T) {
		body := map[string]interface{}{
			"name":        "Engineering v2",
			"description": "Updated description",
			"is_active":   true,
		}
		resp := MakeAuthRequest(t, testServer, http.MethodPut, fmt.Sprintf("/teams/%d", teamID), body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var team map[string]interface{}
		DecodeJSON(t, resp, &team)
		if team["name"] != "Engineering v2" {
			t.Errorf("Expected updated name, got %v", team["name"])
		}
	})

	// Create a second user for member operations. Users default to inactive
	// (POST /users always inserts is_active=false); activate them so the
	// resolved-members query (filters WHERE u.is_active = TRUE) sees them.
	var user2ID int
	t.Run("CreateUserForTeam", func(t *testing.T) {
		body := map[string]interface{}{
			"email":      "teamuser@test.com",
			"username":   "teamuser",
			"first_name": "Team",
			"last_name":  "User",
			"password":   "testpass123",
		}
		resp := MakeAuthRequest(t, testServer, http.MethodPost, "/users", body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)

		var user map[string]interface{}
		DecodeJSON(t, resp, &user)
		user2ID = int(user["id"].(float64))

		activateResp := MakeAuthRequest(t, testServer, http.MethodPost, fmt.Sprintf("/users/%d/activate", user2ID), nil)
		activateResp.Body.Close()
		AssertStatusCode(t, activateResp, http.StatusOK)
	})

	t.Run("AddDirectMembers", func(t *testing.T) {
		body := map[string]interface{}{
			"user_ids": []int{user2ID},
			"role":     "member",
		}
		resp := MakeAuthRequest(t, testServer, http.MethodPost, fmt.Sprintf("/teams/%d/members", teamID), body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("GetResolvedMembers", func(t *testing.T) {
		resp := MakeAuthRequest(t, testServer, http.MethodGet, fmt.Sprintf("/teams/%d/resolved-members", teamID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var members []map[string]interface{}
		DecodeJSON(t, resp, &members)
		if len(members) < 1 {
			t.Error("Expected at least one resolved member")
		}
	})

	// Create a group and map it
	var groupID int
	t.Run("CreateGroupAndMap", func(t *testing.T) {
		groupBody := map[string]interface{}{
			"name":        "QA Group",
			"description": "QA team",
		}
		resp := MakeAuthRequest(t, testServer, http.MethodPost, "/groups", groupBody)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)

		var group map[string]interface{}
		DecodeJSON(t, resp, &group)
		groupID = int(group["id"].(float64))

		// Map group to team
		mapBody := map[string]interface{}{
			"group_ids": []int{groupID},
		}
		resp2 := MakeAuthRequest(t, testServer, http.MethodPost, fmt.Sprintf("/teams/%d/groups", teamID), mapBody)
		defer resp2.Body.Close()
		AssertStatusCode(t, resp2, http.StatusOK)
	})

	t.Run("RemoveGroupMapping", func(t *testing.T) {
		body := map[string]interface{}{
			"group_ids": []int{groupID},
		}
		resp := MakeAuthRequest(t, testServer, http.MethodDelete, fmt.Sprintf("/teams/%d/groups", teamID), body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("UpdateMemberRole", func(t *testing.T) {
		body := map[string]interface{}{
			"role": "admin",
		}
		resp := MakeAuthRequest(t, testServer, http.MethodPut, fmt.Sprintf("/teams/%d/members/%d/role", teamID, user2ID), body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("RemoveDirectMember", func(t *testing.T) {
		body := map[string]interface{}{
			"user_ids": []int{user2ID},
		}
		resp := MakeAuthRequest(t, testServer, http.MethodDelete, fmt.Sprintf("/teams/%d/members", teamID), body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("GetTeamsForUser", func(t *testing.T) {
		resp := MakeAuthRequest(t, testServer, http.MethodGet, "/users/1/teams", nil)
		defer resp.Body.Close()
		// Admin user (ID=1) may or may not be a direct team member, but endpoint should work
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("DeleteTeam", func(t *testing.T) {
		resp := MakeAuthRequest(t, testServer, http.MethodDelete, fmt.Sprintf("/teams/%d", teamID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNoContent)
	})

	t.Run("VerifyTeamDeleted", func(t *testing.T) {
		resp := MakeAuthRequest(t, testServer, http.MethodGet, fmt.Sprintf("/teams/%d", teamID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})
}

// TestLeaveOperations tests leave period CRUD via HTTP
func TestLeaveOperations(t *testing.T) {
	dbType := GetDBType()
	testServer, cleanup := StartTestServer(t, dbType)
	defer cleanup()

	CreateBearerToken(t, testServer)

	// Get admin user ID (the setup creates user as ID 1)
	var userID int

	t.Run("GetCurrentUserID", func(t *testing.T) {
		// /auth/me returns UserResponse with the user nested under "user"
		// (see internal/handlers/auth.go UserResponse) — not flat at the top
		// level. Extract via the nested path.
		resp := MakeAuthRequest(t, testServer, http.MethodGet, "/auth/me", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var me map[string]interface{}
		DecodeJSON(t, resp, &me)
		user, ok := me["user"].(map[string]interface{})
		if !ok {
			t.Fatalf("/auth/me response missing user field; got: %v", me)
		}
		userID = int(user["id"].(float64))
	})

	var leaveID int

	t.Run("CreateLeavePeriod", func(t *testing.T) {
		body := map[string]interface{}{
			"start_date": "2026-06-01",
			"end_date":   "2026-06-15",
			"reason":     "Vacation",
		}
		resp := MakeAuthRequest(t, testServer, http.MethodPost, fmt.Sprintf("/users/%d/leave", userID), body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)

		var leave map[string]interface{}
		DecodeJSON(t, resp, &leave)
		leaveID = int(leave["id"].(float64))

		if leave["reason"] != "Vacation" {
			t.Errorf("Expected reason 'Vacation', got %v", leave["reason"])
		}
	})

	t.Run("GetLeavePeriods", func(t *testing.T) {
		resp := MakeAuthRequest(t, testServer, http.MethodGet, fmt.Sprintf("/users/%d/leave", userID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var periods []map[string]interface{}
		DecodeJSON(t, resp, &periods)
		if len(periods) < 1 {
			t.Error("Expected at least one leave period")
		}
	})

	t.Run("UpdateLeavePeriod", func(t *testing.T) {
		body := map[string]interface{}{
			"start_date": "2026-06-05",
			"end_date":   "2026-06-20",
			"reason":     "Extended vacation",
		}
		resp := MakeAuthRequest(t, testServer, http.MethodPut, fmt.Sprintf("/users/%d/leave/%d", userID, leaveID), body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var leave map[string]interface{}
		DecodeJSON(t, resp, &leave)
		if leave["reason"] != "Extended vacation" {
			t.Errorf("Expected updated reason, got %v", leave["reason"])
		}
	})

	t.Run("DeleteLeavePeriod", func(t *testing.T) {
		resp := MakeAuthRequest(t, testServer, http.MethodDelete, fmt.Sprintf("/users/%d/leave/%d", userID, leaveID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNoContent)
	})
}

// TestOnCallScheduleOperations tests on-call schedule lifecycle via HTTP
func TestOnCallScheduleOperations(t *testing.T) {
	dbType := GetDBType()
	testServer, cleanup := StartTestServer(t, dbType)
	defer cleanup()

	CreateBearerToken(t, testServer)

	// Create a team first
	var teamID int
	t.Run("CreateTeam", func(t *testing.T) {
		body := map[string]interface{}{
			"name":        "On-Call Team",
			"description": "Team for on-call testing",
		}
		resp := MakeAuthRequest(t, testServer, http.MethodPost, "/teams", body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)

		var team map[string]interface{}
		DecodeJSON(t, resp, &team)
		teamID = int(team["id"].(float64))
	})

	var scheduleID int

	t.Run("CreateSchedule", func(t *testing.T) {
		body := map[string]interface{}{
			"name":        "Primary Schedule",
			"description": "Primary on-call rotation",
			"timezone":    "Europe/Berlin",
		}
		resp := MakeAuthRequest(t, testServer, http.MethodPost, fmt.Sprintf("/teams/%d/on-call/schedules", teamID), body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)

		var schedule map[string]interface{}
		DecodeJSON(t, resp, &schedule)
		scheduleID = int(schedule["id"].(float64))
	})

	t.Run("ListSchedules", func(t *testing.T) {
		resp := MakeAuthRequest(t, testServer, http.MethodGet, fmt.Sprintf("/teams/%d/on-call/schedules", teamID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var schedules []map[string]interface{}
		DecodeJSON(t, resp, &schedules)
		if len(schedules) < 1 {
			t.Error("Expected at least one schedule")
		}
	})

	t.Run("GetSchedule", func(t *testing.T) {
		resp := MakeAuthRequest(t, testServer, http.MethodGet, fmt.Sprintf("/on-call/schedules/%d", scheduleID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var schedule map[string]interface{}
		DecodeJSON(t, resp, &schedule)
		if schedule["name"] != "Primary Schedule" {
			t.Errorf("Expected schedule name, got %v", schedule["name"])
		}
	})

	t.Run("AddLayer", func(t *testing.T) {
		body := map[string]interface{}{
			"name":                   "Primary",
			"priority":               1,
			"rotation_type":          "daily",
			"rotation_interval_days": 1,
			"handoff_time":           "09:00",
			"start_date":             "2026-04-01",
		}
		resp := MakeAuthRequest(t, testServer, http.MethodPost, fmt.Sprintf("/on-call/schedules/%d/layers", scheduleID), body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
	})

	t.Run("GetCurrentOnCall", func(t *testing.T) {
		resp := MakeAuthRequest(t, testServer, http.MethodGet, fmt.Sprintf("/on-call/schedules/%d/current", scheduleID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("DeleteSchedule", func(t *testing.T) {
		resp := MakeAuthRequest(t, testServer, http.MethodDelete, fmt.Sprintf("/on-call/schedules/%d", scheduleID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNoContent)
	})

	// Escalation policy tests
	var policyID int

	t.Run("CreateEscalationPolicy", func(t *testing.T) {
		body := map[string]interface{}{
			"name":         "Default Policy",
			"description":  "Default escalation",
			"repeat_count": 2,
		}
		resp := MakeAuthRequest(t, testServer, http.MethodPost, fmt.Sprintf("/teams/%d/on-call/escalation-policies", teamID), body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)

		var policy map[string]interface{}
		DecodeJSON(t, resp, &policy)
		policyID = int(policy["id"].(float64))
	})

	t.Run("GetPolicy", func(t *testing.T) {
		resp := MakeAuthRequest(t, testServer, http.MethodGet, fmt.Sprintf("/on-call/escalation-policies/%d", policyID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("ListPolicies", func(t *testing.T) {
		resp := MakeAuthRequest(t, testServer, http.MethodGet, fmt.Sprintf("/teams/%d/on-call/escalation-policies", teamID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("DeletePolicy", func(t *testing.T) {
		resp := MakeAuthRequest(t, testServer, http.MethodDelete, fmt.Sprintf("/on-call/escalation-policies/%d", policyID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNoContent)
	})

	// Cleanup
	t.Run("DeleteTeam", func(t *testing.T) {
		resp := MakeAuthRequest(t, testServer, http.MethodDelete, fmt.Sprintf("/teams/%d", teamID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNoContent)
	})
}

// TestTeamPermissions tests permission enforcement on team endpoints
func TestTeamPermissions(t *testing.T) {
	dbType := GetDBType()
	testServer, cleanup := StartTestServer(t, dbType)
	defer cleanup()

	CreateBearerToken(t, testServer)

	// Create a non-admin user. POST /users defaults is_active=false, so the
	// user must be activated before login can succeed.
	var nonAdminToken string
	t.Run("CreateNonAdminUser", func(t *testing.T) {
		body := map[string]interface{}{
			"email":      "regular@test.com",
			"username":   "regular",
			"first_name": "Regular",
			"last_name":  "User",
			"password":   "testpass123",
		}
		resp := MakeAuthRequest(t, testServer, http.MethodPost, "/users", body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)

		var user map[string]interface{}
		DecodeJSON(t, resp, &user)
		userID := int(user["id"].(float64))

		activateResp := MakeAuthRequest(t, testServer, http.MethodPost, fmt.Sprintf("/users/%d/activate", userID), nil)
		activateResp.Body.Close()
		AssertStatusCode(t, activateResp, http.StatusOK)

		nonAdminToken = CreateBearerTokenForUser(t, testServer, "regular", "testpass123")
	})

	t.Run("NonAdminCanListTeams", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, testServer, nonAdminToken, http.MethodGet, "/teams", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("NonAdminCannotCreateTeam", func(t *testing.T) {
		body := map[string]interface{}{
			"name":        "Unauthorized Team",
			"description": "Should fail",
		}
		resp := MakeAuthRequestWithToken(t, testServer, nonAdminToken, http.MethodPost, "/teams", body)
		defer resp.Body.Close()
		AssertRejected(t, resp)
	})

	// Admin creates a team
	var teamID int
	t.Run("AdminCreatesTeam", func(t *testing.T) {
		body := map[string]interface{}{
			"name":        "Admin Team",
			"description": "Created by admin",
		}
		resp := MakeAuthRequest(t, testServer, http.MethodPost, "/teams", body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)

		var team map[string]interface{}
		DecodeJSON(t, resp, &team)
		teamID = int(team["id"].(float64))
	})

	t.Run("NonAdminCanViewTeam", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, testServer, nonAdminToken, http.MethodGet, fmt.Sprintf("/teams/%d", teamID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("NonAdminCannotDeleteTeam", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, testServer, nonAdminToken, http.MethodDelete, fmt.Sprintf("/teams/%d", teamID), nil)
		defer resp.Body.Close()
		AssertRejected(t, resp)
	})

	// Cleanup
	t.Run("Cleanup", func(t *testing.T) {
		resp := MakeAuthRequest(t, testServer, http.MethodDelete, fmt.Sprintf("/teams/%d", teamID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNoContent)
	})
}

// DecodeJSON is a helper that decodes response body - uses existing pattern or inlines
func decodeJSONBody(t *testing.T, resp *http.Response, target interface{}) {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}
	if err := json.Unmarshal(body, target); err != nil {
		t.Fatalf("Failed to decode JSON: %v\nBody: %s", err, string(body))
	}
}

// Suppress unused variable warnings for layerID
var _ = func(id int) {}
