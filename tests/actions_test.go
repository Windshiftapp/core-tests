package tests

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// --- helpers ---

// createTestAction creates an action via the API and returns its ID.
func createTestAction(t *testing.T, server *TestServer, workspaceKey string, payload map[string]interface{}) int {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%s/actions", workspaceKey), payload)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)
	return ExtractIDFromResponse(t, result)
}

// setupActionWorkspace creates a workspace and returns the default workflow status IDs.
// Uses the server's built-in default workflow (Open→In Progress→Done) to avoid
// config set / item type conflicts.
//
// Default workflow transitions: null→Open, Open→InProgress, Open→Done, InProgress→Done
// Returned statusIDs: [Open(1), InProgress(2), Done(3)]
func setupActionWorkspace(t *testing.T, server *TestServer) (workspaceID int, workspaceKey string, statusIDs []int, workflowID int) {
	t.Helper()

	workspaceID, workspaceKey = CreateTestWorkspace(t, server, "Action Test Workspace", shortKey("ACT"))

	// Use default workflow (ID 1) and its statuses (Open=1, InProgress=2, Done=3)
	workflowID = 1
	statusIDs = []int{1, 2, 3} // Open, In Progress, Done

	return
}

// waitForActionLog polls action execution logs until at least one log with the expected
// status appears, or the timeout elapses.
func waitForActionLog(t *testing.T, server *TestServer, workspaceKey string, actionID int, expectedStatus string, timeout time.Duration) map[string]interface{} {
	t.Helper()

	endpoint := fmt.Sprintf("/workspaces/%s/actions/%d/logs", workspaceKey, actionID)
	var matchingLog map[string]interface{}
	waitForCondition(t, timeout, fmt.Sprintf("action %d log with status %q", actionID, expectedStatus), func() bool {
		resp := MakeAuthRequest(t, server, http.MethodGet, endpoint, nil)
		var logs []map[string]interface{}
		DecodeJSON(t, resp, &logs)
		resp.Body.Close()

		for _, log := range logs {
			if log["status"] == expectedStatus {
				matchingLog = log
				return true
			}
		}
		return false
	})
	return matchingLog
}

// getItem fetches a single item by ID.
func getItem(t *testing.T, server *TestServer, itemID int) map[string]interface{} {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d", itemID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var item map[string]interface{}
	DecodeJSON(t, resp, &item)
	return item
}

// makeSimpleSetFieldAction builds a payload for an action with trigger → set_field.
func makeSimpleSetFieldAction(name, triggerType, fieldName, value string) map[string]interface{} {
	return map[string]interface{}{
		"name":         name,
		"trigger_type": triggerType,
		"nodes": []map[string]interface{}{
			{"id": -1, "node_type": "trigger", "node_config": "{}", "position_x": 0, "position_y": 0},
			{"id": -2, "node_type": "set_field", "node_config": fmt.Sprintf(`{"field_name":"%s","value":"%s"}`, fieldName, value), "position_x": 100, "position_y": 0},
		},
		"edges": []map[string]interface{}{
			{"source_node_id": -1, "target_node_id": -2, "edge_type": "default"},
		},
	}
}

// --- tests ---

func TestActionCRUD(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	_, workspaceKey := CreateTestWorkspace(t, server, "Action CRUD WS", shortKey("ACR"))

	payload := makeSimpleSetFieldAction("CRUD Action", "manual", "description", "auto")

	var actionID int

	t.Run("Create", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%s/actions", workspaceKey), payload)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)

		var result map[string]interface{}
		DecodeJSON(t, resp, &result)
		actionID = ExtractIDFromResponse(t, result)

		AssertJSONField(t, result, "name", "CRUD Action")
		AssertJSONField(t, result, "trigger_type", "manual")
		AssertJSONField(t, result, "is_enabled", true)

		nodes, ok := result["nodes"].([]interface{})
		if !ok || len(nodes) != 2 {
			t.Fatalf("Expected 2 nodes, got %v", nodes)
		}
		edges, ok := result["edges"].([]interface{})
		if !ok || len(edges) != 1 {
			t.Fatalf("Expected 1 edge, got %v", edges)
		}
	})

	t.Run("List", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/workspaces/%s/actions", workspaceKey), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var actions []map[string]interface{}
		DecodeJSON(t, resp, &actions)
		if len(actions) < 1 {
			t.Fatal("Expected at least 1 action in list")
		}
	})

	t.Run("Get", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/workspaces/%s/actions/%d", workspaceKey, actionID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var result map[string]interface{}
		DecodeJSON(t, resp, &result)
		AssertJSONField(t, result, "name", "CRUD Action")
	})

	t.Run("Update", func(t *testing.T) {
		updatePayload := map[string]interface{}{
			"name":        "Updated Action",
			"description": "Updated description",
		}
		resp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/workspaces/%s/actions/%d", workspaceKey, actionID), updatePayload)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var result map[string]interface{}
		DecodeJSON(t, resp, &result)
		AssertJSONField(t, result, "name", "Updated Action")
		AssertJSONField(t, result, "description", "Updated description")
	})

	t.Run("Delete", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/workspaces/%s/actions/%d", workspaceKey, actionID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNoContent)

		// Verify it's gone
		resp2 := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/workspaces/%s/actions/%d", workspaceKey, actionID), nil)
		defer resp2.Body.Close()
		AssertStatusCode(t, resp2, http.StatusNotFound)
	})
}

func TestActionToggle(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	_, workspaceKey := CreateTestWorkspace(t, server, "Toggle WS", shortKey("TGL"))

	actionID := createTestAction(t, server, workspaceKey, makeSimpleSetFieldAction("Toggle Action", "manual", "description", "x"))

	// Newly created actions are enabled by default
	t.Run("DisableAction", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%s/actions/%d/toggle", workspaceKey, actionID),
			map[string]interface{}{"is_enabled": false})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var result map[string]interface{}
		DecodeJSON(t, resp, &result)
		AssertJSONField(t, result, "is_enabled", false)
	})

	t.Run("EnableAction", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%s/actions/%d/toggle", workspaceKey, actionID),
			map[string]interface{}{"is_enabled": true})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var result map[string]interface{}
		DecodeJSON(t, resp, &result)
		AssertJSONField(t, result, "is_enabled", true)
	})
}

func TestActionValidation(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	_, workspaceKey := CreateTestWorkspace(t, server, "Validation WS", shortKey("VAL"))

	t.Run("MissingName", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%s/actions", workspaceKey),
			map[string]interface{}{
				"trigger_type": "manual",
			})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("MissingTriggerType", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%s/actions", workspaceKey),
			map[string]interface{}{
				"name": "No Trigger",
			})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("WrongWorkspace", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, "/workspaces/NONEXIST/actions", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})
}

func TestActionManualExecution(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, workspaceKey, _, _ := setupActionWorkspace(t, server)

	// Create item
	itemID := CreateTestItem(t, server, workspaceID, "Manual Exec Item")

	// Create action: manual trigger → set_field(description="auto-set")
	actionID := createTestAction(t, server, workspaceKey, makeSimpleSetFieldAction("Manual Set", "manual", "description", "auto-set"))

	// Execute manually
	resp := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/workspaces/%s/actions/%d/execute", workspaceKey, actionID),
		map[string]interface{}{"item_id": itemID})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var execResult map[string]interface{}
	DecodeJSON(t, resp, &execResult)
	if execResult["status"] != "completed" {
		t.Fatalf("Expected status=completed, got %v", execResult["status"])
	}

	// Verify item was updated
	item := getItem(t, server, itemID)
	if item["description"] != "auto-set" {
		t.Errorf("Expected description='auto-set', got %v", item["description"])
	}
}

func TestManualActionRoleVisibilityAndExecution(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, workspaceKey, _, _ := setupActionWorkspace(t, server)
	itemID := CreateTestItem(t, server, workspaceID, "Role-scoped manual action item")

	editorID, editorUsername, editorPassword := CreateTestUserWithCredentials(
		t, server, "manual_action_editor", "manual_action_editor@test.com",
	)
	AssignWorkspaceRole(t, server, editorID, workspaceID, "Editor")
	editorSession := CreateBearerTokenForUser(t, server, editorUsername, editorPassword)

	viewerID, viewerUsername, viewerPassword := CreateTestUserWithCredentials(
		t, server, "manual_action_viewer", "manual_action_viewer@test.com",
	)
	AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")
	viewerSession := CreateBearerTokenForUser(t, server, viewerUsername, viewerPassword)

	unrestrictedID := createTestAction(t, server, workspaceKey, map[string]interface{}{
		"name":         "Open to editors",
		"trigger_type": "manual",
	})
	viewerRoleID := GetWorkspaceRoles(t, server)["Viewer"]
	restrictedID := createTestAction(t, server, workspaceKey, map[string]interface{}{
		"name":             "Viewer-only handoff",
		"trigger_type":     "manual",
		"allowed_role_ids": []int{viewerRoleID},
	})

	manualActionIDs := func(t *testing.T, session string) map[int]bool {
		t.Helper()
		resp := MakeAuthRequestWithToken(t, server, session, http.MethodGet,
			fmt.Sprintf("/items/%d/detail-summary", itemID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var summary struct {
			ManualActions []struct {
				ID int `json:"id"`
			} `json:"manual_actions"`
		}
		DecodeJSON(t, resp, &summary)
		ids := make(map[int]bool, len(summary.ManualActions))
		for _, action := range summary.ManualActions {
			ids[action.ID] = true
		}
		return ids
	}

	t.Run("EditorSeesAndExecutesUnrestrictedAction", func(t *testing.T) {
		ids := manualActionIDs(t, editorSession)
		if !ids[unrestrictedID] || ids[restrictedID] {
			t.Fatalf("editor manual actions = %v, want unrestricted %d only", ids, unrestrictedID)
		}
		resp := MakeAuthRequestWithToken(t, server, editorSession, http.MethodPost,
			fmt.Sprintf("/workspaces/%s/actions/%d/execute", workspaceKey, unrestrictedID),
			map[string]interface{}{"item_id": itemID})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("ViewerCannotDiscoverOrExecuteUnrestrictedAction", func(t *testing.T) {
		ids := manualActionIDs(t, viewerSession)
		if ids[unrestrictedID] {
			t.Fatalf("viewer unexpectedly saw unrestricted action %d", unrestrictedID)
		}
		resp := MakeAuthRequestWithToken(t, server, viewerSession, http.MethodPost,
			fmt.Sprintf("/workspaces/%s/actions/%d/execute", workspaceKey, unrestrictedID),
			map[string]interface{}{"item_id": itemID})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})

	t.Run("SelectedViewerRoleCanDiscoverAndExecuteRestrictedAction", func(t *testing.T) {
		ids := manualActionIDs(t, viewerSession)
		if !ids[restrictedID] || ids[unrestrictedID] {
			t.Fatalf("viewer manual actions = %v, want restricted %d only", ids, restrictedID)
		}
		resp := MakeAuthRequestWithToken(t, server, viewerSession, http.MethodPost,
			fmt.Sprintf("/workspaces/%s/actions/%d/execute", workspaceKey, restrictedID),
			map[string]interface{}{"item_id": itemID})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("NonMatchingEditorCannotExecuteRestrictedAction", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, editorSession, http.MethodPost,
			fmt.Sprintf("/workspaces/%s/actions/%d/execute", workspaceKey, restrictedID),
			map[string]interface{}{"item_id": itemID})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})

	t.Run("WorkspaceAdministratorRetainsOverride", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost,
			fmt.Sprintf("/workspaces/%s/actions/%d/execute", workspaceKey, restrictedID),
			map[string]interface{}{"item_id": itemID})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})
}

func TestManualActionRoleCannotBeDeletedWhileReferenced(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	_, workspaceKey, _, _ := setupActionWorkspace(t, server)

	roleResp := MakeAuthRequest(t, server, http.MethodPost, "/workspace-roles", map[string]interface{}{
		"name":        "Manual action operators",
		"description": "May run selected manual actions",
	})
	AssertStatusCode(t, roleResp, http.StatusCreated)
	var role map[string]interface{}
	DecodeJSON(t, roleResp, &role)
	roleResp.Body.Close()
	roleID := ExtractIDFromResponse(t, role)

	actionID := createTestAction(t, server, workspaceKey, map[string]interface{}{
		"name":             "Restricted operation",
		"trigger_type":     "manual",
		"allowed_role_ids": []int{roleID},
	})

	blocked := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/workspace-roles/%d", roleID), nil)
	defer blocked.Body.Close()
	AssertStatusCode(t, blocked, http.StatusConflict)

	deleteAction := MakeAuthRequest(t, server, http.MethodDelete,
		fmt.Sprintf("/workspaces/%s/actions/%d", workspaceKey, actionID), nil)
	deleteAction.Body.Close()
	AssertStatusCode(t, deleteAction, http.StatusNoContent)

	deleted := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/workspace-roles/%d", roleID), nil)
	defer deleted.Body.Close()
	AssertStatusCode(t, deleted, http.StatusNoContent)
}

func TestActionExecutionLogs(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, workspaceKey, _, _ := setupActionWorkspace(t, server)

	itemID := CreateTestItem(t, server, workspaceID, "Log Test Item")
	actionID := createTestAction(t, server, workspaceKey, makeSimpleSetFieldAction("Log Action", "manual", "description", "logged"))

	// Execute
	resp := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/workspaces/%s/actions/%d/execute", workspaceKey, actionID),
		map[string]interface{}{"item_id": itemID})
	resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	t.Run("ActionLogs", func(t *testing.T) {
		logResp := MakeAuthRequest(t, server, http.MethodGet,
			fmt.Sprintf("/workspaces/%s/actions/%d/logs", workspaceKey, actionID), nil)
		defer logResp.Body.Close()
		AssertStatusCode(t, logResp, http.StatusOK)

		var logs []map[string]interface{}
		DecodeJSON(t, logResp, &logs)
		if len(logs) < 1 {
			t.Fatal("Expected at least 1 execution log")
		}
		log := logs[0]
		if log["status"] != "completed" {
			t.Errorf("Expected log status=completed, got %v", log["status"])
		}
		if log["trigger_event"] != "manual" {
			t.Errorf("Expected trigger_event=manual, got %v", log["trigger_event"])
		}
		if log["started_at"] == nil {
			t.Error("Expected started_at to be set")
		}
	})

	t.Run("WorkspaceLogs", func(t *testing.T) {
		wsLogResp := MakeAuthRequest(t, server, http.MethodGet,
			fmt.Sprintf("/workspaces/%s/action-logs", workspaceKey), nil)
		defer wsLogResp.Body.Close()
		AssertStatusCode(t, wsLogResp, http.StatusOK)

		var wsLogs []map[string]interface{}
		DecodeJSON(t, wsLogResp, &wsLogs)
		if len(wsLogs) < 1 {
			t.Fatal("Expected at least 1 workspace-level log")
		}
	})
}

func TestActionStatusTransitionTrigger(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, workspaceKey, statusIDs, _ := setupActionWorkspace(t, server)

	// Create action: status_transition(any→statusIDs[1]) → set_field(description="transitioned")
	triggerConfig := fmt.Sprintf(`{"to_status_id":%d}`, statusIDs[1])
	payload := map[string]interface{}{
		"name":           "On Transition",
		"trigger_type":   "status_transition",
		"trigger_config": triggerConfig,
		"nodes": []map[string]interface{}{
			{"id": -1, "node_type": "trigger", "node_config": "{}", "position_x": 0, "position_y": 0},
			{"id": -2, "node_type": "set_field", "node_config": `{"field_name":"description","value":"transitioned"}`, "position_x": 100, "position_y": 0},
		},
		"edges": []map[string]interface{}{
			{"source_node_id": -1, "target_node_id": -2, "edge_type": "default"},
		},
	}
	actionID := createTestAction(t, server, workspaceKey, payload)

	// Create item (will have initial status from workflow)
	itemID := CreateTestItem(t, server, workspaceID, "Transition Item")

	// Item starts at Open(statusIDs[0]). Transition to InProgress(statusIDs[1]).
	resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/transition", itemID),
		map[string]interface{}{"to_status_id": statusIDs[1]})
	AssertStatusCode(t, resp, http.StatusOK)
	resp.Body.Close()

	// Wait for async execution
	waitForActionLog(t, server, workspaceKey, actionID, "completed", 3*time.Second)

	// Verify field was updated
	item := getItem(t, server, itemID)
	if item["description"] != "transitioned" {
		t.Errorf("Expected description='transitioned', got %v", item["description"])
	}
}

func TestActionItemCreatedTrigger(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, workspaceKey, _, _ := setupActionWorkspace(t, server)

	// Create action: item_created → set_field(description="auto-created")
	actionID := createTestAction(t, server, workspaceKey, makeSimpleSetFieldAction("On Created", "item_created", "description", "auto-created"))

	// Create a new item — this should trigger the action
	itemID := CreateTestItem(t, server, workspaceID, "Created Trigger Item")

	// Wait for async execution
	waitForActionLog(t, server, workspaceKey, actionID, "completed", 3*time.Second)

	// Verify field was updated
	item := getItem(t, server, itemID)
	if item["description"] != "auto-created" {
		t.Errorf("Expected description='auto-created', got %v", item["description"])
	}
}

func TestActionConditionBranching(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, workspaceKey, _, _ := setupActionWorkspace(t, server)

	// Create action: manual → condition(description is_not_empty) → true:set_field("not empty") / false:set_field("was empty")
	payload := map[string]interface{}{
		"name":         "Condition Branch",
		"trigger_type": "manual",
		"nodes": []map[string]interface{}{
			{"id": -1, "node_type": "trigger", "node_config": "{}", "position_x": 0, "position_y": 0},
			{"id": -2, "node_type": "condition", "node_config": `{"field_name":"description","operator":"is_not_empty","value":""}`, "position_x": 100, "position_y": 0},
			{"id": -3, "node_type": "set_field", "node_config": `{"field_name":"description","value":"not empty"}`, "position_x": 200, "position_y": -50},
			{"id": -4, "node_type": "set_field", "node_config": `{"field_name":"description","value":"was empty"}`, "position_x": 200, "position_y": 50},
		},
		"edges": []map[string]interface{}{
			{"source_node_id": -1, "target_node_id": -2, "edge_type": "default"},
			{"source_node_id": -2, "target_node_id": -3, "edge_type": "true"},
			{"source_node_id": -2, "target_node_id": -4, "edge_type": "false"},
		},
	}
	actionID := createTestAction(t, server, workspaceKey, payload)

	// Create item with empty description
	itemID := CreateTestItem(t, server, workspaceID, "Condition Item")

	// Execute manually — description is empty, so false branch should fire
	resp := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/workspaces/%s/actions/%d/execute", workspaceKey, actionID),
		map[string]interface{}{"item_id": itemID})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	// Verify false branch was taken
	item := getItem(t, server, itemID)
	if item["description"] != "was empty" {
		t.Errorf("Expected description='was empty' (false branch), got %v", item["description"])
	}
}

func TestActionCascadeChain(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, workspaceKey, statusIDs, _ := setupActionWorkspace(t, server)

	// Action A: item_created → set_status to statusIDs[1]
	payloadA := map[string]interface{}{
		"name":         "Cascade A - Set Status",
		"trigger_type": "item_created",
		"nodes": []map[string]interface{}{
			{"id": -1, "node_type": "trigger", "node_config": "{}", "position_x": 0, "position_y": 0},
			{"id": -2, "node_type": "set_status", "node_config": fmt.Sprintf(`{"status_id":%d}`, statusIDs[1]), "position_x": 100, "position_y": 0},
		},
		"edges": []map[string]interface{}{
			{"source_node_id": -1, "target_node_id": -2, "edge_type": "default"},
		},
	}
	createTestAction(t, server, workspaceKey, payloadA)

	// Action B: status_transition to statusIDs[1], respond_to_cascades=true → set_field(description="cascaded")
	triggerConfigB := fmt.Sprintf(`{"to_status_id":%d,"respond_to_cascades":true}`, statusIDs[1])
	payloadB := map[string]interface{}{
		"name":           "Cascade B - Set Field",
		"trigger_type":   "status_transition",
		"trigger_config": triggerConfigB,
		"nodes": []map[string]interface{}{
			{"id": -1, "node_type": "trigger", "node_config": "{}", "position_x": 0, "position_y": 0},
			{"id": -2, "node_type": "set_field", "node_config": `{"field_name":"description","value":"cascaded"}`, "position_x": 100, "position_y": 0},
		},
		"edges": []map[string]interface{}{
			{"source_node_id": -1, "target_node_id": -2, "edge_type": "default"},
		},
	}
	actionBID := createTestAction(t, server, workspaceKey, payloadB)

	// Create item — triggers Action A (item_created) → sets status → triggers Action B (status_transition)
	itemID := CreateTestItem(t, server, workspaceID, "Cascade Item")

	// Wait for Action B to complete
	waitForActionLog(t, server, workspaceKey, actionBID, "completed", 5*time.Second)

	// Verify both effects applied
	item := getItem(t, server, itemID)

	statusID := item["status_id"]
	if statusID == nil {
		t.Fatal("Expected status_id to be set")
	}
	if int(statusID.(float64)) != statusIDs[1] {
		t.Errorf("Expected status_id=%d, got %v", statusIDs[1], statusID)
	}

	if item["description"] != "cascaded" {
		t.Errorf("Expected description='cascaded', got %v", item["description"])
	}
}
