//go:build test

package handlers

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/logger"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// createWorkspaceRoleHandler creates a WorkspaceRoleHandler with test services
func createWorkspaceRoleHandler(t *testing.T, tdb *testutils.TestDB) *WorkspaceRoleHandler {
	t.Helper()
	permService, _, _ := createTestServices(t, *tdb)
	return NewWorkspaceRoleHandlerWithPool(repository.NewWorkspaceRoleRepository(tdb.GetDatabase()), permService, logger.NewAuditor(tdb.GetDatabase()))
}

// --- GetAll tests ---

func TestWorkspaceRoleHandler_GetAll_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createWorkspaceRoleHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/workspace-roles", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")
}

// --- AssignRoleToUser tests ---

func TestWorkspaceRoleHandler_AssignRoleToUser_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createWorkspaceRoleHandler(t, tdb)

	// Get a role ID from the DB
	var roleID int
	err := tdb.GetDatabase().QueryRow("SELECT id FROM workspace_roles LIMIT 1").Scan(&roleID)
	if err != nil {
		t.Fatalf("Failed to get role ID: %v", err)
	}

	body := map[string]interface{}{
		"user_id":      data.UserID,
		"workspace_id": data.WorkspaceID,
		"role_id":      roleID,
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/workspace-roles/assign", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.AssignRoleToUser, req, nil)

	if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
		t.Errorf("Expected 200 or 201, got %d: %s", rr.Code, rr.Body.String())
	}
}

// --- RevokeRoleFromUser tests ---

func TestWorkspaceRoleHandler_RevokeRoleFromUser_NotAssigned(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createWorkspaceRoleHandler(t, tdb)

	// RevokeRoleFromUser uses path parameters, not JSON body
	req := testutils.CreateJSONRequest(t, "DELETE",
		fmt.Sprintf("/api/workspaces/%d/roles/99999/users/%d", data.WorkspaceID, data.UserID), nil)
	req.SetPathValue("userId", testutils.IntToString(data.UserID))
	req.SetPathValue("workspaceId", testutils.IntToString(data.WorkspaceID))
	req.SetPathValue("roleId", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.RevokeRoleFromUser, req, nil)

	// Should handle gracefully (not found or OK)
	if rr.Code != http.StatusOK && rr.Code != http.StatusNotFound {
		t.Errorf("Expected 200 or 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// --- GetUserRolesInWorkspace tests ---

func TestWorkspaceRoleHandler_GetUserRolesInWorkspace_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createWorkspaceRoleHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET",
		fmt.Sprintf("/api/workspace-roles/users/%d/workspaces/%d", data.UserID, data.WorkspaceID), nil)
	req.SetPathValue("userId", testutils.IntToString(data.UserID))
	req.SetPathValue("workspaceId", testutils.IntToString(data.WorkspaceID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetUserRolesInWorkspace, req, nil)

	rr.AssertStatusCode(http.StatusOK)
}
