//go:build test

package handlers

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// createActionsHandler creates an ActionsHandler with test services
func createActionsHandler(t *testing.T, tdb *testutils.TestDB) *ActionsHandler {
	t.Helper()
	permService, _, _ := createTestServices(t, *tdb)

	db := tdb.GetDatabase()
	actionConfig := services.DefaultActionServiceConfig()
	actionService := services.NewActionService(db, actionConfig, nil)

	handler := NewActionsHandler(
		repository.NewActionRepository(db),
		repository.NewActionCredentialRepository(db),
		repository.NewItemRepository(db),
		logger.NewAuditor(db),
		actionService,
		permService,
		nil,
	)
	handler.SetAssetService(services.NewAssetService(db, repository.NewAssetRepository(db)))
	return handler
}

// createTestAction creates a manual action in the workspace through the
// actions API and returns its ID
func createTestAction(t *testing.T, tdb *testutils.TestDB, workspaceID int) int {
	t.Helper()
	return newServiceSetup(t, tdb).CreateAction(workspaceID, "Test Action")
}

// --- CreateAction permission tests ---

func TestActionsHandler_CreateAction_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createActionsHandler(t, tdb)

	body := models.CreateActionRequest{
		Name:        "New Action",
		TriggerType: "manual",
	}

	req := testutils.CreateJSONRequest(t, "POST", fmt.Sprintf("/api/workspaces/%d/actions", data.WorkspaceID), body)
	req.SetPathValue("workspaceId", testutils.IntToString(data.WorkspaceID))
	rr := testutils.ExecuteRequest(t, handler.CreateAction, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

// --- ExecuteAction permission tests ---

func TestActionsHandler_ExecuteAction_RequiresItemEditPermission(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createActionsHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)
	actionID := createTestAction(t, tdb, data.WorkspaceID)

	body := map[string]interface{}{
		"item_id": itemID,
	}

	req := testutils.CreateJSONRequest(t, "POST",
		fmt.Sprintf("/api/workspaces/%d/actions/%d/execute", data.WorkspaceID, actionID), body)
	req.SetPathValue("workspaceId", testutils.IntToString(data.WorkspaceID))
	req.SetPathValue("id", testutils.IntToString(actionID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.ExecuteAction, req, nil)

	// Permission check should pass (user has item.edit in the test workspace).
	// Execution itself may return 200 or 500 depending on action service setup,
	// but it should NOT return 401/403/404.
	if rr.Code == http.StatusUnauthorized || rr.Code == http.StatusForbidden || rr.Code == http.StatusNotFound {
		t.Errorf("Expected permission check to pass, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestActionsHandler_ExecuteAction_Unauthenticated(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createActionsHandler(t, tdb)
	actionID := createTestAction(t, tdb, data.WorkspaceID)

	body := map[string]interface{}{
		"item_id": 1,
	}

	req := testutils.CreateJSONRequest(t, "POST",
		fmt.Sprintf("/api/workspaces/%d/actions/%d/execute", data.WorkspaceID, actionID), body)
	req.SetPathValue("workspaceId", testutils.IntToString(data.WorkspaceID))
	req.SetPathValue("id", testutils.IntToString(actionID))
	rr := testutils.ExecuteRequest(t, handler.ExecuteAction, req)

	// CheckItemPermission returns 401 for unauthenticated users
	rr.AssertStatusCode(http.StatusUnauthorized)
}

func TestActionsHandler_ExecuteAction_MissingItemID(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createActionsHandler(t, tdb)

	body := map[string]interface{}{
		"item_id": 0,
	}

	req := testutils.CreateJSONRequest(t, "POST",
		fmt.Sprintf("/api/workspaces/%d/actions/1/execute", data.WorkspaceID), body)
	req.SetPathValue("workspaceId", testutils.IntToString(data.WorkspaceID))
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.ExecuteAction, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
}

func TestActionsHandler_ExecuteAction_NonExistentAction(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	handler := createActionsHandler(t, tdb)
	itemID := createTestItemForComments(t, tdb, data)

	body := map[string]interface{}{
		"item_id": itemID,
	}

	req := testutils.CreateJSONRequest(t, "POST",
		fmt.Sprintf("/api/workspaces/%d/actions/99999/execute", data.WorkspaceID), body)
	req.SetPathValue("workspaceId", testutils.IntToString(data.WorkspaceID))
	req.SetPathValue("id", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.ExecuteAction, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}
