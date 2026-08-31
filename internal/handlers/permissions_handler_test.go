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

// createPermissionHandler creates a PermissionHandler with test services
func createPermissionHandler(t *testing.T, tdb *testutils.TestDB) *PermissionHandler {
	t.Helper()
	permService, _, _ := createTestServices(t, *tdb)
	return NewPermissionHandlerWithCache(repository.NewPermissionRepository(tdb.GetDatabase()), permService, logger.NewAuditor(tdb.GetDatabase()))
}

// --- GetUserPermissions permission tests ---

func TestPermissionHandler_GetUserPermissions_OwnPermissions(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createPermissionHandler(t, tdb)

	// User accessing their own permissions
	req := testutils.CreateJSONRequest(t, "GET", "/api/permissions/users/1", nil)
	req.SetPathValue("userId", testutils.IntToString(data.UserID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetUserPermissions, req, nil)

	rr.AssertStatusCode(http.StatusOK)
}

func TestPermissionHandler_GetUserPermissions_OtherUser_Forbidden(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createPermissionHandler(t, tdb)

	// Create a second user through the user repository (assigned ID 2 in a fresh DB)
	newServiceSetup(t, tdb).CreateUser("other@test.com", "otheruser", "Other", "User")

	// Non-admin user trying to access another user's permissions
	nonAdmin := testutils.TestUserWithID(99)
	req := testutils.CreateJSONRequest(t, "GET", "/api/permissions/users/2", nil)
	req.SetPathValue("userId", "2")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetUserPermissions, req, nonAdmin)

	rr.AssertStatusCode(http.StatusForbidden)
}

func TestPermissionHandler_GetUserPermissions_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createPermissionHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/permissions/users/1", nil)
	req.SetPathValue("userId", "1")
	rr := testutils.ExecuteRequest(t, handler.GetUserPermissions, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

// --- GrantGlobalPermission permission tests ---

func TestPermissionHandler_GrantGlobalPermission_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createPermissionHandler(t, tdb)

	// Get a global permission ID
	var permID int
	err := tdb.GetDatabase().QueryRow("SELECT id FROM permissions WHERE scope = ? LIMIT 1", models.PermissionScopeGlobal).Scan(&permID)
	if err != nil {
		t.Fatalf("Failed to get permission ID: %v", err)
	}

	body := models.PermissionRequest{
		UserID:       1,
		PermissionID: permID,
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/permissions/global", body)
	rr := testutils.ExecuteRequest(t, handler.GrantGlobalPermission, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

func TestPermissionHandler_GrantGlobalPermission_NonGlobalPermission(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createPermissionHandler(t, tdb)

	// Get a workspace-scoped permission ID
	var permID int
	err := tdb.GetDatabase().QueryRow("SELECT id FROM permissions WHERE scope = ? LIMIT 1", "workspace").Scan(&permID)
	if err != nil {
		t.Skipf("No workspace-scoped permission found: %v", err)
	}

	body := models.PermissionRequest{
		UserID:       1,
		PermissionID: permID,
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/permissions/global", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GrantGlobalPermission, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
}

// --- RevokeGlobalPermission - last admin protection ---

func TestPermissionHandler_RevokeGlobalPermission_LastAdminProtection(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createPermissionHandler(t, tdb)

	// Get the system.admin permission ID
	var adminPermID int
	if err := tdb.GetDatabase().QueryRow("SELECT id FROM permissions WHERE permission_key = ?", models.PermissionSystemAdmin).Scan(&adminPermID); err != nil {
		t.Fatalf("Failed to get system.admin permission ID: %v", err)
	}

	// Grant system.admin below the HTTP layer so this revoke test does not use
	// the grant handler under test as fixture setup.
	newServiceSetup(t, tdb).GrantGlobal(data.UserID, models.PermissionSystemAdmin)

	// Try to revoke system.admin from the last admin - should be forbidden
	req := testutils.CreateJSONRequest(t, "DELETE", "/api/permissions/users/1/global/1", nil)
	req.SetPathValue("userId", testutils.IntToString(data.UserID))
	req.SetPathValue("permissionId", testutils.IntToString(adminPermID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.RevokeGlobalPermission, req, nil)

	rr.AssertStatusCode(http.StatusForbidden)
}

func TestPermissionHandler_RevokeGlobalPermission_NotFound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createPermissionHandler(t, tdb)

	// Try to revoke a permission that was never granted
	req := testutils.CreateJSONRequest(t, "DELETE", "/api/permissions/users/1/global/99999", nil)
	req.SetPathValue("userId", "1")
	req.SetPathValue("permissionId", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.RevokeGlobalPermission, req, nil)

	// Permission doesn't exist in the DB, should get internal error or not found
	if rr.Code != http.StatusInternalServerError && rr.Code != http.StatusNotFound {
		t.Errorf("Expected 500 or 404, got %d", rr.Code)
	}
}

// --- GrantGlobalPermissionToGroup tests ---

func TestPermissionHandler_GrantGlobalPermissionToGroup_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createPermissionHandler(t, tdb)

	body := map[string]interface{}{
		"group_id":      1,
		"permission_id": 1,
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/permissions/groups/global", body)
	rr := testutils.ExecuteRequest(t, handler.GrantGlobalPermissionToGroup, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

func TestPermissionHandler_GrantGlobalPermissionToGroup_NonExistentGroup(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createPermissionHandler(t, tdb)

	// Get a global permission ID
	var permID int
	err := tdb.GetDatabase().QueryRow("SELECT id FROM permissions WHERE scope = ? LIMIT 1", models.PermissionScopeGlobal).Scan(&permID)
	if err != nil {
		t.Fatalf("Failed to get permission ID: %v", err)
	}

	body := map[string]interface{}{
		"group_id":      99999,
		"permission_id": permID,
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/permissions/groups/global", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GrantGlobalPermissionToGroup, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}
