//go:build test

package handlers

import (
	"net/http"
	"testing"

	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// createTeamHandler creates a TeamHandler with test dependencies and grants teams.manage to user 1
func createTeamHandler(t *testing.T, tdb *testutils.TestDB) *TeamHandler {
	t.Helper()
	permService, _, _ := createTestServices(t, *tdb)
	teamRepo := repository.NewTeamRepository(tdb.GetDatabase())
	leaveRepo := repository.NewLeaveRepository(tdb.GetDatabase())

	// Grant teams.manage to the default test user (ID 1) through the API
	newServiceSetup(t, tdb).GrantGlobal(1, "teams.manage")

	return NewTeamHandler(teamRepo, leaveRepo, permService, logger.NewAuditor(tdb.GetDatabase()))
}

// createTestTeam creates a team via the handler and returns its ID
func createTestTeam(t *testing.T, handler *TeamHandler, name string) int {
	t.Helper()
	body := models.TeamCreateRequest{
		Name:        name,
		Description: "Test team",
	}
	req := testutils.CreateJSONRequest(t, "POST", "/api/teams", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
	rr.AssertStatusCode(http.StatusCreated)

	var team models.Team
	rr.AssertJSONResponse(&team)
	return team.ID
}

// --- GetAll ---

func TestTeamHandler_GetAll_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/teams", nil)
	rr := testutils.ExecuteRequest(t, handler.GetAll, req)
	rr.AssertStatusCode(http.StatusUnauthorized)
}

func TestTeamHandler_GetAll_Empty(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/teams", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	var teams []models.Team
	rr.AssertJSONResponse(&teams)
	if len(teams) != 0 {
		t.Errorf("Expected empty list, got %d teams", len(teams))
	}
}

func TestTeamHandler_GetAll_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	createTestTeam(t, handler, "Alpha Team")
	createTestTeam(t, handler, "Beta Team")

	req := testutils.CreateJSONRequest(t, "GET", "/api/teams", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	var teams []models.Team
	rr.AssertJSONResponse(&teams)
	if len(teams) != 2 {
		t.Errorf("Expected 2 teams, got %d", len(teams))
	}
}

// --- Get ---

func TestTeamHandler_Get_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "Get Team")

	req := testutils.CreateJSONRequest(t, "GET", "/api/teams/"+testutils.IntToString(teamID), nil)
	req.SetPathValue("id", testutils.IntToString(teamID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Get, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	var team models.Team
	rr.AssertJSONResponse(&team)
	if team.Name != "Get Team" {
		t.Errorf("Expected name 'Get Team', got %s", team.Name)
	}
}

func TestTeamHandler_Get_NotFound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/teams/99999", nil)
	req.SetPathValue("id", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Get, req, nil)
	rr.AssertStatusCode(http.StatusNotFound)
}

// --- Create ---

func TestTeamHandler_Create_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	body := models.TeamCreateRequest{Name: "Test Team"}
	req := testutils.CreateJSONRequest(t, "POST", "/api/teams", body)
	rr := testutils.ExecuteRequest(t, handler.Create, req)
	rr.AssertStatusCode(http.StatusUnauthorized)
}

func TestTeamHandler_Create_NoPermission(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)

	// Create handler without granting teams.manage
	permService, _, _ := createTestServices(t, *tdb)
	teamRepo := repository.NewTeamRepository(tdb.GetDatabase())
	leaveRepo := repository.NewLeaveRepository(tdb.GetDatabase())
	handler := NewTeamHandler(teamRepo, leaveRepo, permService, logger.NewAuditor(tdb.GetDatabase()))

	body := models.TeamCreateRequest{Name: "Test Team"}
	req := testutils.CreateJSONRequest(t, "POST", "/api/teams", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
	rr.AssertStatusCode(http.StatusForbidden)
}

func TestTeamHandler_Create_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	body := models.TeamCreateRequest{
		Name:        "Engineering",
		Description: "Engineering team",
	}
	req := testutils.CreateJSONRequest(t, "POST", "/api/teams", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
	rr.AssertStatusCode(http.StatusCreated)

	var team models.Team
	rr.AssertJSONResponse(&team)
	if team.Name != "Engineering" {
		t.Errorf("Expected name 'Engineering', got %s", team.Name)
	}
	if !team.IsActive {
		t.Error("Expected team to be active")
	}
}

func TestTeamHandler_Create_DuplicateName(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	createTestTeam(t, handler, "Duplicate Team")

	body := models.TeamCreateRequest{Name: "Duplicate Team"}
	req := testutils.CreateJSONRequest(t, "POST", "/api/teams", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
	rr.AssertStatusCode(http.StatusConflict)
}

func TestTeamHandler_Create_EmptyName(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	body := models.TeamCreateRequest{Name: ""}
	req := testutils.CreateJSONRequest(t, "POST", "/api/teams", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
	rr.AssertStatusCode(http.StatusBadRequest)
}

// --- Update ---

func TestTeamHandler_Update_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "Old Name")

	body := models.TeamUpdateRequest{
		Name:        "New Name",
		Description: "Updated desc",
		IsActive:    true,
	}
	req := testutils.CreateJSONRequest(t, "PUT", "/api/teams/"+testutils.IntToString(teamID), body)
	req.SetPathValue("id", testutils.IntToString(teamID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	var team models.Team
	rr.AssertJSONResponse(&team)
	if team.Name != "New Name" {
		t.Errorf("Expected name 'New Name', got %s", team.Name)
	}
	var auditCount int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action_type = ? AND resource_id = ?`, logger.ActionTeamUpdate, teamID).Scan(&auditCount); err != nil {
		t.Fatalf("count team update audits: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("team update audit count = %d, want 1", auditCount)
	}
}

func TestTeamHandler_Update_NotFound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	body := models.TeamUpdateRequest{Name: "Name", IsActive: true}
	req := testutils.CreateJSONRequest(t, "PUT", "/api/teams/99999", body)
	req.SetPathValue("id", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)
	// canManageTeam checks teams.manage first (which user has), then IsTeamAdmin (which returns false for nonexistent team),
	// but since user has global perm, they pass canManageTeam. Then Update returns ErrNotFound.
	rr.AssertStatusCode(http.StatusNotFound)
}

// --- Delete ---

func TestTeamHandler_Delete_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "Deletable Team")

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/teams/"+testutils.IntToString(teamID), nil)
	req.SetPathValue("id", testutils.IntToString(teamID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, req, nil)
	rr.AssertStatusCode(http.StatusNoContent)
}

func TestTeamHandler_Delete_NotFound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/teams/99999", nil)
	req.SetPathValue("id", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, req, nil)
	rr.AssertStatusCode(http.StatusNotFound)
}

func TestTeamHandler_Delete_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/teams/1", nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteRequest(t, handler.Delete, req)
	rr.AssertStatusCode(http.StatusUnauthorized)
}

// --- AddMembers ---

func TestTeamHandler_AddMembers_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "Members Team")

	body := models.TeamMemberRequest{UserIDs: []int{1}, Role: "member"}
	req := testutils.CreateJSONRequest(t, "POST", "/api/teams/"+testutils.IntToString(teamID)+"/members", body)
	req.SetPathValue("id", testutils.IntToString(teamID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.AddMembers, req, nil)
	rr.AssertStatusCode(http.StatusOK)
}

func TestTeamHandler_AddMembers_EmptyUserIDs(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "Members Team 2")

	body := models.TeamMemberRequest{UserIDs: []int{}}
	req := testutils.CreateJSONRequest(t, "POST", "/api/teams/"+testutils.IntToString(teamID)+"/members", body)
	req.SetPathValue("id", testutils.IntToString(teamID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.AddMembers, req, nil)
	rr.AssertStatusCode(http.StatusBadRequest)
}

func TestTeamHandler_AddMembers_InvalidUser(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "Members Team 3")

	body := models.TeamMemberRequest{UserIDs: []int{99999}}
	req := testutils.CreateJSONRequest(t, "POST", "/api/teams/"+testutils.IntToString(teamID)+"/members", body)
	req.SetPathValue("id", testutils.IntToString(teamID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.AddMembers, req, nil)
	rr.AssertStatusCode(http.StatusBadRequest)
}

func TestTeamHandler_AddMembers_InvalidRole(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "Members Team 4")

	body := models.TeamMemberRequest{UserIDs: []int{1}, Role: "superadmin"}
	req := testutils.CreateJSONRequest(t, "POST", "/api/teams/"+testutils.IntToString(teamID)+"/members", body)
	req.SetPathValue("id", testutils.IntToString(teamID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.AddMembers, req, nil)
	rr.AssertStatusCode(http.StatusBadRequest)
}

// --- RemoveMembers ---

func TestTeamHandler_RemoveMembers_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "Remove Members Team")

	// Add member first
	addBody := models.TeamMemberRequest{UserIDs: []int{1}, Role: "member"}
	addReq := testutils.CreateJSONRequest(t, "POST", "/api/teams/"+testutils.IntToString(teamID)+"/members", addBody)
	addReq.SetPathValue("id", testutils.IntToString(teamID))
	testutils.ExecuteAuthenticatedRequest(t, handler.AddMembers, addReq, nil).AssertStatusCode(http.StatusOK)

	// Remove member
	body := models.TeamMemberRequest{UserIDs: []int{1}}
	req := testutils.CreateJSONRequest(t, "DELETE", "/api/teams/"+testutils.IntToString(teamID)+"/members", body)
	req.SetPathValue("id", testutils.IntToString(teamID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.RemoveMembers, req, nil)
	rr.AssertStatusCode(http.StatusOK)
}

// --- UpdateMemberRole ---

func TestTeamHandler_UpdateMemberRole_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "Role Update Team")

	// Add member
	addBody := models.TeamMemberRequest{UserIDs: []int{1}, Role: "member"}
	addReq := testutils.CreateJSONRequest(t, "POST", "/api/teams/"+testutils.IntToString(teamID)+"/members", addBody)
	addReq.SetPathValue("id", testutils.IntToString(teamID))
	testutils.ExecuteAuthenticatedRequest(t, handler.AddMembers, addReq, nil).AssertStatusCode(http.StatusOK)

	// Update role to admin
	body := models.TeamMemberRoleRequest{Role: "admin"}
	req := testutils.CreateJSONRequest(t, "PUT", "/api/teams/"+testutils.IntToString(teamID)+"/members/1/role", body)
	req.SetPathValue("id", testutils.IntToString(teamID))
	req.SetPathValue("userId", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateMemberRole, req, nil)
	rr.AssertStatusCode(http.StatusOK)
}

func TestTeamHandler_UpdateMemberRole_NotFound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "Role Update Team 2")

	body := models.TeamMemberRoleRequest{Role: "admin"}
	req := testutils.CreateJSONRequest(t, "PUT", "/api/teams/"+testutils.IntToString(teamID)+"/members/99999/role", body)
	req.SetPathValue("id", testutils.IntToString(teamID))
	req.SetPathValue("userId", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateMemberRole, req, nil)
	rr.AssertStatusCode(http.StatusNotFound)
}

func TestTeamHandler_UpdateMemberRole_InvalidRole(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "Role Update Team 3")

	body := models.TeamMemberRoleRequest{Role: "superadmin"}
	req := testutils.CreateJSONRequest(t, "PUT", "/api/teams/"+testutils.IntToString(teamID)+"/members/1/role", body)
	req.SetPathValue("id", testutils.IntToString(teamID))
	req.SetPathValue("userId", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateMemberRole, req, nil)
	rr.AssertStatusCode(http.StatusBadRequest)
}

// --- AddGroups ---

func TestTeamHandler_AddGroups_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "Group Team")

	// Create a group
	var groupID int
	err := tdb.GetDatabase().QueryRow(`
		INSERT INTO groups (name, description, is_active, created_at, updated_at)
		VALUES ('Test Group', 'desc', true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`).Scan(&groupID)
	if err != nil {
		t.Fatalf("Failed to create group: %v", err)
	}

	body := models.TeamGroupRequest{GroupIDs: []int{groupID}}
	req := testutils.CreateJSONRequest(t, "POST", "/api/teams/"+testutils.IntToString(teamID)+"/groups", body)
	req.SetPathValue("id", testutils.IntToString(teamID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.AddGroups, req, nil)
	rr.AssertStatusCode(http.StatusOK)
	var auditCount int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action_type = ? AND resource_id = ?`, logger.ActionTeamAddGroup, teamID).Scan(&auditCount); err != nil {
		t.Fatalf("count team add-group audits: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("team add-group audit count = %d, want 1", auditCount)
	}
}

func TestTeamHandler_AddGroups_EmptyGroupIDs(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "Group Team 2")

	body := models.TeamGroupRequest{GroupIDs: []int{}}
	req := testutils.CreateJSONRequest(t, "POST", "/api/teams/"+testutils.IntToString(teamID)+"/groups", body)
	req.SetPathValue("id", testutils.IntToString(teamID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.AddGroups, req, nil)
	rr.AssertStatusCode(http.StatusBadRequest)
}

func TestTeamHandler_AddGroups_InvalidGroup(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "Group Team 3")

	body := models.TeamGroupRequest{GroupIDs: []int{99999}}
	req := testutils.CreateJSONRequest(t, "POST", "/api/teams/"+testutils.IntToString(teamID)+"/groups", body)
	req.SetPathValue("id", testutils.IntToString(teamID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.AddGroups, req, nil)
	rr.AssertStatusCode(http.StatusBadRequest)
}

// --- RemoveGroups ---

func TestTeamHandler_RemoveGroups_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "Remove Group Team")

	// Create and add group
	var groupID int
	err := tdb.GetDatabase().QueryRow(`
		INSERT INTO groups (name, description, is_active, created_at, updated_at)
		VALUES ('Removable Group', 'desc', true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`).Scan(&groupID)
	if err != nil {
		t.Fatalf("Failed to create group: %v", err)
	}

	addBody := models.TeamGroupRequest{GroupIDs: []int{groupID}}
	addReq := testutils.CreateJSONRequest(t, "POST", "/api/teams/"+testutils.IntToString(teamID)+"/groups", addBody)
	addReq.SetPathValue("id", testutils.IntToString(teamID))
	testutils.ExecuteAuthenticatedRequest(t, handler.AddGroups, addReq, nil).AssertStatusCode(http.StatusOK)

	// Remove group
	body := models.TeamGroupRequest{GroupIDs: []int{groupID}}
	req := testutils.CreateJSONRequest(t, "DELETE", "/api/teams/"+testutils.IntToString(teamID)+"/groups", body)
	req.SetPathValue("id", testutils.IntToString(teamID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.RemoveGroups, req, nil)
	rr.AssertStatusCode(http.StatusOK)
	var auditCount int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action_type = ? AND resource_id = ?`, logger.ActionTeamRemoveGroup, teamID).Scan(&auditCount); err != nil {
		t.Fatalf("count team remove-group audits: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("team remove-group audit count = %d, want 1", auditCount)
	}
}

// --- GetResolvedMembers ---

func TestTeamHandler_GetResolvedMembers_DirectOnly(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "Resolved Team")

	// Add direct member (user ID 1 from seed)
	addBody := models.TeamMemberRequest{UserIDs: []int{1}, Role: "member"}
	addReq := testutils.CreateJSONRequest(t, "POST", "/api/teams/"+testutils.IntToString(teamID)+"/members", addBody)
	addReq.SetPathValue("id", testutils.IntToString(teamID))
	testutils.ExecuteAuthenticatedRequest(t, handler.AddMembers, addReq, nil).AssertStatusCode(http.StatusOK)

	req := testutils.CreateJSONRequest(t, "GET", "/api/teams/"+testutils.IntToString(teamID)+"/resolved-members", nil)
	req.SetPathValue("id", testutils.IntToString(teamID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetResolvedMembers, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	var members []models.ResolvedTeamMember
	rr.AssertJSONResponse(&members)
	if len(members) != 1 {
		t.Fatalf("Expected 1 resolved member, got %d", len(members))
	}
	if members[0].Source != "direct" {
		t.Errorf("Expected source 'direct', got %s", members[0].Source)
	}
}

func TestTeamHandler_GetResolvedMembers_Empty(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "Empty Resolved Team")

	req := testutils.CreateJSONRequest(t, "GET", "/api/teams/"+testutils.IntToString(teamID)+"/resolved-members", nil)
	req.SetPathValue("id", testutils.IntToString(teamID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetResolvedMembers, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	var members []models.ResolvedTeamMember
	rr.AssertJSONResponse(&members)
	if len(members) != 0 {
		t.Errorf("Expected empty list, got %d members", len(members))
	}
}

// --- GetTeamsForUser ---

func TestTeamHandler_GetTeamsForUser_Self(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	handler := createTeamHandler(t, tdb)

	teamID := createTestTeam(t, handler, "User Teams Test")

	// Add user 1 to team
	addBody := models.TeamMemberRequest{UserIDs: []int{1}, Role: "member"}
	addReq := testutils.CreateJSONRequest(t, "POST", "/api/teams/"+testutils.IntToString(teamID)+"/members", addBody)
	addReq.SetPathValue("id", testutils.IntToString(teamID))
	testutils.ExecuteAuthenticatedRequest(t, handler.AddMembers, addReq, nil).AssertStatusCode(http.StatusOK)

	// Get teams for user 1 (self)
	req := testutils.CreateJSONRequest(t, "GET", "/api/users/1/teams", nil)
	req.SetPathValue("userId", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetTeamsForUser, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	var teams []models.Team
	rr.AssertJSONResponse(&teams)
	if len(teams) != 1 {
		t.Fatalf("Expected 1 team, got %d", len(teams))
	}
	if teams[0].Name != "User Teams Test" {
		t.Errorf("Expected team name 'User Teams Test', got %s", teams[0].Name)
	}
}
