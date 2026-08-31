//go:build test

package handlers

import (
	"net/http"
	"strings"
	"testing"

	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// createGroupHandler creates a GroupHandler with test services
func createGroupHandler(t *testing.T, tdb *testutils.TestDB) *GroupHandler {
	t.Helper()
	permService, _, _ := createTestServices(t, *tdb)
	return NewGroupHandler(repository.NewGroupRepository(tdb.GetDatabase()), permService, logger.NewAuditor(tdb.GetDatabase()))
}

// createTestGroup creates a group and returns its ID
func createTestGroup(t *testing.T, handler *GroupHandler, name string) int {
	t.Helper()

	body := models.TeamGroupCreateRequest{
		Name:        name,
		Description: "Test group",
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/groups", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
	rr.AssertStatusCode(http.StatusCreated)

	var group models.TeamGroup
	rr.AssertJSONResponse(&group)
	return group.ID
}

// --- Create permission tests ---

func TestGroupHandler_Create_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createGroupHandler(t, tdb)

	body := models.TeamGroupCreateRequest{
		Name: "Test Group",
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/groups", body)
	rr := testutils.ExecuteRequest(t, handler.Create, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

func TestGroupHandler_Create_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createGroupHandler(t, tdb)

	body := models.TeamGroupCreateRequest{
		Name:        "Engineering Team",
		Description: "All engineers",
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/groups", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

	rr.AssertStatusCode(http.StatusCreated).
		AssertContentType("application/json")

	var group models.TeamGroup
	rr.AssertJSONResponse(&group)

	if group.Name != "Engineering Team" {
		t.Errorf("Expected name 'Engineering Team', got %s", group.Name)
	}
}

// --- Update permission tests ---

// --- Delete permission tests ---

func TestGroupHandler_Delete_SystemGroup_Forbidden(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createGroupHandler(t, tdb)

	// Create a system group directly in the DB
	var groupID int
	err := tdb.GetDatabase().QueryRow(`
		INSERT INTO groups (name, description, is_system_group, is_active, created_at, updated_at)
		VALUES ('System Group', 'Built-in group', true, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`).Scan(&groupID)
	if err != nil {
		t.Fatalf("Failed to create system group: %v", err)
	}

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/groups/"+testutils.IntToString(groupID), nil)
	req.SetPathValue("id", testutils.IntToString(groupID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, req, nil)

	rr.AssertStatusCode(http.StatusForbidden)
}

func TestGroupHandler_Delete_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createGroupHandler(t, tdb)
	groupID := createTestGroup(t, handler, "Deletable Group")

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/groups/"+testutils.IntToString(groupID), nil)
	req.SetPathValue("id", testutils.IntToString(groupID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, req, nil)

	rr.AssertStatusCode(http.StatusNoContent)
}

func TestGroupHandler_Delete_NotFound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createGroupHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/groups/99999", nil)
	req.SetPathValue("id", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}

func TestGroupHandler_Delete_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createGroupHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/groups/1", nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteRequest(t, handler.Delete, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

// --- AddMembers permission tests ---

func TestGroupHandler_AddMembers_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createGroupHandler(t, tdb)

	// Create a group so the existence check passes and we reach RequireAuth
	groupID := createTestGroup(t, handler, "Auth Test Group")

	body := models.TeamGroupMemberRequest{
		UserIDs: []int{1},
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/groups/"+testutils.IntToString(groupID)+"/members", body)
	req.SetPathValue("id", testutils.IntToString(groupID))
	rr := testutils.ExecuteRequest(t, handler.AddMembers, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

func TestGroupHandler_AddMembers_NonExistentGroup(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createGroupHandler(t, tdb)

	body := models.TeamGroupMemberRequest{
		UserIDs: []int{1},
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/groups/99999/members", body)
	req.SetPathValue("id", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.AddMembers, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}

func TestGroupHandler_AddMembers_EmptyUserIDs(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createGroupHandler(t, tdb)
	groupID := createTestGroup(t, handler, "Member Test Group")

	body := models.TeamGroupMemberRequest{
		UserIDs: []int{},
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/groups/"+testutils.IntToString(groupID)+"/members", body)
	req.SetPathValue("id", testutils.IntToString(groupID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.AddMembers, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
	if !strings.Contains(rr.Body.String(), "At least one user ID is required") {
		t.Errorf("Expected validation error, got %s", rr.Body.String())
	}
}

// --- GetUserMemberships permission tests ---

func TestGroupHandler_GetUserMemberships_OwnMemberships(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createGroupHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/users/1/groups", nil)
	req.SetPathValue("userId", testutils.IntToString(data.UserID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetUserMemberships, req, nil)

	rr.AssertStatusCode(http.StatusOK)
}

// --- Create validation tests ---

func TestGroupHandler_Create_ValidationErrors(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createGroupHandler(t, tdb)

	tests := []struct {
		name        string
		body        models.TeamGroupCreateRequest
		expectedErr string
	}{
		{
			name:        "Empty name",
			body:        models.TeamGroupCreateRequest{Name: ""},
			expectedErr: "Name is required",
		},
		{
			name:        "Whitespace-only name",
			body:        models.TeamGroupCreateRequest{Name: "   "},
			expectedErr: "Name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := testutils.CreateJSONRequest(t, "POST", "/api/groups", tt.body)
			rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

			rr.AssertStatusCode(http.StatusBadRequest)
			if !strings.Contains(rr.Body.String(), tt.expectedErr) {
				t.Errorf("Expected body to contain %q, got %q", tt.expectedErr, rr.Body.String())
			}
		})
	}
}

func TestGroupHandler_Create_DuplicateName(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createGroupHandler(t, tdb)

	// Create a group
	createTestGroup(t, handler, "Unique Group")

	// Try to create another with the same name
	body := models.TeamGroupCreateRequest{
		Name: "Unique Group",
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/groups", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

	rr.AssertStatusCode(http.StatusConflict)
}
