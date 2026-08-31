//go:build test

package handlers

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/restapi"
	"windshift/internal/testutils"
)

// createUserHandler creates a UserHandler with test services. Threads through
// newTestUserHandler (defined in invitations_test.go) so the full constructor
// surface stays in one place.
func createUserHandler(t *testing.T, tdb *testutils.TestDB) *UserHandler {
	t.Helper()
	permService, _, _ := createTestServices(t, *tdb)
	return newTestUserHandler(tdb, permService, nil)
}

func TestUserHandlerGetAssignableHidesInaccessibleWorkspace(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	deniedUserID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('roster-denied@example.test', 'roster-denied', 'Roster', 'Denied', true)
	`)
	if _, err := tdb.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id)
		VALUES (?, ?, (SELECT id FROM workspace_roles WHERE name = 'Viewer'))
	`, data.UserID, data.WorkspaceID); err != nil {
		t.Fatalf("restrict workspace Viewer access: %v", err)
	}
	handler := createUserHandler(t, tdb)
	req := testutils.CreateJSONRequest(t, http.MethodGet, "/api/workspaces/1/assignable-users", nil)
	req.SetPathValue("workspaceId", testutils.IntToString(data.WorkspaceID))
	recorder := testutils.ExecuteAuthenticatedRequest(t, handler.GetAssignable, req, testutils.TestUserWithID(deniedUserID))
	recorder.AssertStatusCode(http.StatusNotFound)

	var response restapi.ErrorResponse
	recorder.AssertJSONResponse(&response)
	if response.Code != restapi.ErrCodeWorkspaceNotFound || response.Error != "Workspace not found" {
		t.Fatalf("denial response = %#v, want workspace-not-found contract", response)
	}
}

// --- GetAll permission tests ---

func TestUserHandler_GetAll_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createUserHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/users", nil)
	rr := testutils.ExecuteRequest(t, handler.GetAll, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

func TestUserHandler_GetAll_AuthenticatedUser(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createUserHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/users", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)

	// Any authenticated user can list users (for assignment, mentions)
	rr.AssertStatusCode(http.StatusOK)
}

// --- Get (single user) permission tests ---

func TestUserHandler_Get_OwnProfile(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createUserHandler(t, tdb)

	// Default test user (ID 1) accessing their own profile
	req := testutils.CreateJSONRequest(t, "GET", fmt.Sprintf("/api/users/%d", data.UserID), nil)
	req.SetPathValue("id", testutils.IntToString(data.UserID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Get, req, nil)

	rr.AssertStatusCode(http.StatusOK)
}

func TestUserHandler_Get_OtherProfile_NoPermission(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createUserHandler(t, tdb)

	// Create a second user
	_, err := tdb.GetDatabase().ExecWrite(`
		INSERT INTO users (id, email, username, first_name, last_name, password_hash, is_active, created_at, updated_at)
		VALUES (2, 'other@test.com', 'otheruser', 'Other', 'User', 'hash', true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("Failed to create second user: %v", err)
	}

	// User 1 (not admin, no user.list perm) trying to access user 2
	req := testutils.CreateJSONRequest(t, "GET", "/api/users/2", nil)
	req.SetPathValue("id", "2")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Get, req, testutils.TestUserWithID(data.UserID))

	rr.AssertStatusCode(http.StatusForbidden)
}

func TestUserHandler_Get_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createUserHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/users/1", nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteRequest(t, handler.Get, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

// --- UpdateAvatar permission tests ---

func TestUserHandler_UpdateAvatar_OwnProfile(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createUserHandler(t, tdb)
	invalidatedUserID := 0
	handler.invalidateSessions = func(userID int) { invalidatedUserID = userID }

	body := map[string]interface{}{
		"avatar_url": "https://example.com/avatar.png",
	}

	req := testutils.CreateJSONRequest(t, "PUT", fmt.Sprintf("/api/users/%d/avatar", data.UserID), body)
	req.SetPathValue("id", testutils.IntToString(data.UserID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateAvatar, req, nil)

	rr.AssertStatusCode(http.StatusOK)
	if invalidatedUserID != data.UserID {
		t.Fatalf("invalidated user ID = %d, want %d", invalidatedUserID, data.UserID)
	}
}

func TestUserHandler_UpdateAvatar_OtherProfile_Forbidden(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createUserHandler(t, tdb)

	// Create a second user through the user repository (assigned ID 2 in a fresh DB)
	newServiceSetup(t, tdb).CreateUser("other@test.com", "otheruser", "Other", "User")

	// User 99 (non-admin) trying to update user 2's avatar
	nonAdmin := testutils.TestUserWithID(99)
	body := map[string]interface{}{
		"avatar_url": "https://example.com/hacked.png",
	}

	req := testutils.CreateJSONRequest(t, "PUT", "/api/users/2/avatar", body)
	req.SetPathValue("id", "2")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateAvatar, req, nonAdmin)

	rr.AssertStatusCode(http.StatusForbidden)
}

// --- UpdateRegionalSettings permission tests ---

func TestUserHandler_UpdateRegionalSettings_OwnProfile(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createUserHandler(t, tdb)

	body := map[string]interface{}{
		"timezone": "Europe/Berlin",
		"language": "de",
	}

	req := testutils.CreateJSONRequest(t, "PUT", fmt.Sprintf("/api/users/%d/regional-settings", data.UserID), body)
	req.SetPathValue("id", testutils.IntToString(data.UserID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateRegionalSettings, req, nil)

	rr.AssertStatusCode(http.StatusOK)
}

func TestUserHandler_UpdateRegionalSettings_OtherProfile_Forbidden(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := createUserHandler(t, tdb)

	// Create a second user through the user repository (assigned ID 2 in a fresh DB)
	newServiceSetup(t, tdb).CreateUser("other2@test.com", "otheruser2", "Other", "User2")

	nonAdmin := testutils.TestUserWithID(99)
	body := map[string]interface{}{
		"timezone": "US/Pacific",
		"language": "en",
	}

	req := testutils.CreateJSONRequest(t, "PUT", "/api/users/2/regional-settings", body)
	req.SetPathValue("id", "2")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateRegionalSettings, req, nonAdmin)

	rr.AssertStatusCode(http.StatusForbidden)
}

// --- DeactivateUser permission tests ---

func TestUserHandler_DeactivateUser_SelfPrevention(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createUserHandler(t, tdb)

	// User trying to deactivate themselves - should be forbidden
	req := testutils.CreateJSONRequest(t, "POST", fmt.Sprintf("/api/users/%d/deactivate", data.UserID), nil)
	req.SetPathValue("id", testutils.IntToString(data.UserID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.DeactivateUser, req, nil)

	rr.AssertStatusCode(http.StatusForbidden)
}

// --- Delete permission tests ---

func TestUserHandler_Delete_SelfDeletion_Forbidden(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createUserHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "DELETE", fmt.Sprintf("/api/users/%d", data.UserID), nil)
	req.SetPathValue("id", testutils.IntToString(data.UserID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, req, nil)

	rr.AssertStatusCode(http.StatusForbidden)
}
