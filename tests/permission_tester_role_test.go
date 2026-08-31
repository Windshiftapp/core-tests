package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestWorkspaceRoles_Tester tests that users with the Tester role have correct permissions.
// The Tester role should grant test.view, test.execute, and test.manage permissions
// but NOT grant item CRUD permissions (create/edit/delete).
func TestWorkspaceRoles_Tester(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminToken := CreateBearerToken(t, server)
	server.BearerToken = adminToken

	// Create a test workspace and lock it down
	workspaceID, _ := CreateTestWorkspace(t, server, "Tester Role Workspace", shortKey("TRW"))
	LockDownWorkspace(t, server, workspaceID)

	// Create a tester user and assign role
	testerID, testerUsername, testerPassword := CreateTestUserWithCredentials(t, server, "tester_user", "tester@test.com")
	AssignWorkspaceRole(t, server, testerID, workspaceID, "Tester")
	testerToken := CreateBearerTokenForUser(t, server, testerUsername, testerPassword)

	// Create a test item as admin for context
	testItemID := CreateTestItem(t, server, workspaceID, "Item for Tester Tests")

	// --- Test viewing permissions (test.view) ---

	t.Run("Tester_CanViewTestCases", func(t *testing.T) {
		endpoint := fmt.Sprintf("/workspaces/%d/test-cases", workspaceID)
		resp := MakeAuthRequestWithToken(t, server, testerToken, http.MethodGet, endpoint, nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("Tester_CanViewTestFolders", func(t *testing.T) {
		endpoint := fmt.Sprintf("/workspaces/%d/test-folders", workspaceID)
		resp := MakeAuthRequestWithToken(t, server, testerToken, http.MethodGet, endpoint, nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("Tester_CanViewTestSets", func(t *testing.T) {
		endpoint := fmt.Sprintf("/workspaces/%d/test-sets", workspaceID)
		resp := MakeAuthRequestWithToken(t, server, testerToken, http.MethodGet, endpoint, nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("Tester_CanViewTestRuns", func(t *testing.T) {
		endpoint := fmt.Sprintf("/workspaces/%d/test-runs", workspaceID)
		resp := MakeAuthRequestWithToken(t, server, testerToken, http.MethodGet, endpoint, nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	// --- Test management permissions (test.manage) ---

	t.Run("Tester_CanCreateTestCase", func(t *testing.T) {
		endpoint := fmt.Sprintf("/workspaces/%d/test-cases", workspaceID)
		testCase := map[string]interface{}{
			"title":       "Login Test Case",
			"description": "Test that login works correctly",
			"priority":    "high",
		}
		resp := MakeAuthRequestWithToken(t, server, testerToken, http.MethodPost, endpoint, testCase)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
	})

	t.Run("Tester_CanCreateTestFolder", func(t *testing.T) {
		endpoint := fmt.Sprintf("/workspaces/%d/test-folders", workspaceID)
		folder := map[string]interface{}{
			"name": "Authentication Tests",
		}
		resp := MakeAuthRequestWithToken(t, server, testerToken, http.MethodPost, endpoint, folder)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
	})

	// testSetID is captured by Tester_CanCreateTestSet and consumed by
	// Tester_CanCreateTestRun — test_runs has a NOT NULL FK to test_sets, so a
	// run cannot be created without a set_id.
	var testSetID int

	t.Run("Tester_CanCreateTestSet", func(t *testing.T) {
		endpoint := fmt.Sprintf("/workspaces/%d/test-sets", workspaceID)
		set := map[string]interface{}{
			"name":        "Smoke Test Set",
			"description": "Basic smoke tests",
		}
		resp := MakeAuthRequestWithToken(t, server, testerToken, http.MethodPost, endpoint, set)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var result map[string]interface{}
		DecodeJSON(t, resp, &result)
		testSetID = ExtractIDFromResponse(t, result)
	})

	// --- Test execution permissions (test.execute) ---

	t.Run("Tester_CanCreateTestRun", func(t *testing.T) {
		if testSetID == 0 {
			t.Skip("test set was not created — Tester_CanCreateTestSet must run first")
		}
		endpoint := fmt.Sprintf("/workspaces/%d/test-runs", workspaceID)
		run := map[string]interface{}{
			"name":   "Sprint 1 Test Run",
			"set_id": testSetID,
		}
		resp := MakeAuthRequestWithToken(t, server, testerToken, http.MethodPost, endpoint, run)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
	})

	// --- Item permissions (should NOT be granted to Tester role) ---

	t.Run("Tester_CannotCreateItems", func(t *testing.T) {
		configSetID := GetDefaultConfigurationSet(t, server)
		itemTypes := GetItemTypes(t, server, configSetID)
		itemTypeID := RequireItemTypeID(t, itemTypes, "Task")

		itemData := map[string]interface{}{
			"title":        "Tester Created Item",
			"workspace_id": workspaceID,
			"item_type_id": itemTypeID,
		}
		resp := MakeAuthRequestWithToken(t, server, testerToken, http.MethodPost, "/items", itemData)
		defer resp.Body.Close()
		AssertRejected(t, resp)
	})

	t.Run("Tester_CannotEditItems", func(t *testing.T) {
		endpoint := fmt.Sprintf("/items/%d", testItemID)
		updateData := map[string]interface{}{
			"title": "Updated by Tester",
		}
		resp := MakeAuthRequestWithToken(t, server, testerToken, http.MethodPut, endpoint, updateData)
		defer resp.Body.Close()
		AssertRejected(t, resp)
	})

	t.Run("Tester_CannotDeleteItems", func(t *testing.T) {
		endpoint := fmt.Sprintf("/items/%d", testItemID)
		resp := MakeAuthRequestWithToken(t, server, testerToken, http.MethodDelete, endpoint, nil)
		defer resp.Body.Close()
		AssertRejected(t, resp)
	})

	t.Run("Tester_CannotAdministerWorkspace", func(t *testing.T) {
		endpoint := fmt.Sprintf("/workspaces/%d", workspaceID)
		updateData := map[string]interface{}{
			"name":        "Updated by Tester",
			"key":         shortKey("TRW"),
			"description": "Should fail",
		}
		resp := MakeAuthRequestWithToken(t, server, testerToken, http.MethodPut, endpoint, updateData)
		defer resp.Body.Close()
		AssertRejected(t, resp)
	})
}

// TestWorkspaceRoles_Tester_NoRole tests that users without the Tester role
// cannot access test management endpoints in a locked workspace.
func TestWorkspaceRoles_Tester_NoRole(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminToken := CreateBearerToken(t, server)
	server.BearerToken = adminToken

	// Create a test workspace and lock it down
	workspaceID, _ := CreateTestWorkspace(t, server, "No Tester Workspace", shortKey("NTW"))
	LockDownWorkspace(t, server, workspaceID)

	// Create a user with NO roles (locked out of workspace)
	_, noRoleUsername, noRolePassword := CreateTestUserWithCredentials(t, server, "norole_tester", "norole_test@test.com")
	noRoleToken := CreateBearerTokenForUser(t, server, noRoleUsername, noRolePassword)

	t.Run("NoRole_CannotViewTestCases", func(t *testing.T) {
		endpoint := fmt.Sprintf("/workspaces/%d/test-cases", workspaceID)
		resp := MakeAuthRequestWithToken(t, server, noRoleToken, http.MethodGet, endpoint, nil)
		defer resp.Body.Close()
		AssertRejected(t, resp)
	})

	t.Run("NoRole_CannotCreateTestCase", func(t *testing.T) {
		endpoint := fmt.Sprintf("/workspaces/%d/test-cases", workspaceID)
		testCase := map[string]interface{}{
			"title":       "Unauthorized Test Case",
			"description": "Should not be created",
		}
		resp := MakeAuthRequestWithToken(t, server, noRoleToken, http.MethodPost, endpoint, testCase)
		defer resp.Body.Close()
		AssertRejected(t, resp)
	})

	t.Run("NoRole_CannotCreateTestRun", func(t *testing.T) {
		endpoint := fmt.Sprintf("/workspaces/%d/test-runs", workspaceID)
		run := map[string]interface{}{
			"name": "Unauthorized Test Run",
		}
		resp := MakeAuthRequestWithToken(t, server, noRoleToken, http.MethodPost, endpoint, run)
		defer resp.Body.Close()
		AssertRejected(t, resp)
	})
}
