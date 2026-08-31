package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// conditionTestEnv holds shared state for condition transition tests.
type conditionTestEnv struct {
	server      *TestServer
	workspaceID int
	// System statuses: openID, inProgressID, doneID
	openID       int
	inProgressID int
	doneID       int
	workflowID   int
	transitions  []map[string]interface{} // from PUT /workflows/{id}/transitions
	itemID       int
	configSetID  int
}

// setupConditionTestData creates a full test environment using the system's
// default statuses (Open, In Progress, Done). Creates workspace, workflow with
// transitions, updates default config set, and creates an item at Open status.
func setupConditionTestData(t *testing.T) *conditionTestEnv {
	t.Helper()

	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	env := &conditionTestEnv{server: server}

	// Get system statuses (created during setup)
	resp := MakeAuthRequest(t, server, http.MethodGet, "/statuses", nil)
	var statuses []map[string]interface{}
	DecodeJSON(t, resp, &statuses)
	resp.Body.Close()

	for _, s := range statuses {
		name, _ := s["name"].(string)
		id := int(s["id"].(float64))
		switch name {
		case "Open":
			env.openID = id
		case "In Progress":
			env.inProgressID = id
		case "Done":
			env.doneID = id
		}
	}
	if env.openID == 0 || env.inProgressID == 0 || env.doneID == 0 {
		t.Fatalf("missing system statuses: Open=%d, InProgress=%d, Done=%d", env.openID, env.inProgressID, env.doneID)
	}

	// Create workspace
	env.workspaceID, _ = CreateTestWorkspace(t, server, "Condition Test WS", shortKey("CND"))

	// Create item FIRST (before config set association, so all item types allowed)
	env.itemID = CreateTestItem(t, server, env.workspaceID, "Condition Test Item")

	// Create workflow with transitions on system statuses
	wfData := map[string]interface{}{
		"name":       "Condition Test Workflow",
		"is_default": false,
	}
	wfResp := MakeAuthRequest(t, server, http.MethodPost, "/workflows", wfData)
	var wfResult map[string]interface{}
	DecodeJSON(t, wfResp, &wfResult)
	wfResp.Body.Close()
	env.workflowID = ExtractIDFromResponse(t, wfResult)

	// Transitions:
	//   nil → Open (initial)
	//   Open → In Progress (will be condition-gated in tests)
	//   Open → Done (always available, used as control)
	//   In Progress → Done
	transitions := []map[string]interface{}{
		{"workflow_id": env.workflowID, "from_status_id": nil, "to_status_id": env.openID, "display_order": 0},
		{"workflow_id": env.workflowID, "from_status_id": env.openID, "to_status_id": env.inProgressID, "display_order": 0},
		{"workflow_id": env.workflowID, "from_status_id": env.openID, "to_status_id": env.doneID, "display_order": 1},
		{"workflow_id": env.workflowID, "from_status_id": env.inProgressID, "to_status_id": env.doneID, "display_order": 0},
	}
	trResp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/workflows/%d/transitions", env.workflowID), transitions)
	DecodeJSON(t, trResp, &env.transitions)
	trResp.Body.Close()

	if len(env.transitions) != len(transitions) {
		t.Fatalf("expected %d transitions, got %d", len(transitions), len(env.transitions))
	}

	// Get default config set and update it with our workflow + workspace
	env.configSetID = GetDefaultConfigurationSet(t, server)

	getResp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/configuration-sets/%d", env.configSetID), nil)
	var currentCS map[string]interface{}
	DecodeJSON(t, getResp, &currentCS)
	getResp.Body.Close()

	updateData := map[string]interface{}{
		"name":          currentCS["name"],
		"description":   currentCS["description"],
		"is_default":    true,
		"workflow_id":   env.workflowID,
		"workspace_ids": []int{env.workspaceID},
	}
	for _, key := range []string{"create_screen_id", "edit_screen_id", "view_screen_id"} {
		if v, ok := currentCS[key]; ok && v != nil {
			updateData[key] = v
		}
	}

	csUpdateResp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/configuration-sets/%d", env.configSetID), updateData)
	AssertStatusCode(t, csUpdateResp, http.StatusOK)
	csUpdateResp.Body.Close()

	return env
}

// findTransitionID finds the transition ID for a from→to status pair.
func (e *conditionTestEnv) findTransitionID(t *testing.T, fromStatusID, toStatusID int) int {
	t.Helper()
	for _, tr := range e.transitions {
		fromID := tr["from_status_id"]
		toID := tr["to_status_id"]

		var from int
		switch v := fromID.(type) {
		case float64:
			from = int(v)
		case nil:
			continue
		}

		to := int(toID.(float64))
		if from == fromStatusID && to == toStatusID {
			return int(tr["id"].(float64))
		}
	}
	t.Fatalf("transition %d → %d not found", fromStatusID, toStatusID)
	return 0
}

// createConditionSet creates a condition set with a single script condition on a transition.
func (e *conditionTestEnv) createConditionSet(t *testing.T, name string, transitionID int, script, mode string) int {
	t.Helper()

	if mode == "" {
		mode = "condition"
	}

	condSetData := map[string]interface{}{
		"name":        name,
		"workflow_id": e.workflowID,
		"transition_conditions": []map[string]interface{}{
			{
				"transition_id": transitionID,
				"logic_mode":    "and",
				"conditions": []map[string]interface{}{
					{
						"condition_type": "script",
						"config":         json.RawMessage(fmt.Sprintf(`{"script":%q}`, script)),
						"display_order":  0,
						"mode":           mode,
						"error_message":  "Transition blocked by condition",
					},
				},
			},
		},
	}

	resp := MakeAuthRequest(t, e.server, http.MethodPost, "/condition-sets", condSetData)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)
	return ExtractIDFromResponse(t, result)
}

// associateConditionSet updates the config set to use the given condition set.
func (e *conditionTestEnv) associateConditionSet(t *testing.T, conditionSetID int) {
	t.Helper()

	getResp := MakeAuthRequest(t, e.server, http.MethodGet, fmt.Sprintf("/configuration-sets/%d", e.configSetID), nil)
	var currentCS map[string]interface{}
	DecodeJSON(t, getResp, &currentCS)
	getResp.Body.Close()

	updateData := map[string]interface{}{
		"name":             currentCS["name"],
		"description":      currentCS["description"],
		"is_default":       true,
		"workflow_id":      e.workflowID,
		"workspace_ids":    []int{e.workspaceID},
		"condition_set_id": conditionSetID,
	}
	for _, key := range []string{"create_screen_id", "edit_screen_id", "view_screen_id"} {
		if v, ok := currentCS[key]; ok && v != nil {
			updateData[key] = v
		}
	}

	resp := MakeAuthRequest(t, e.server, http.MethodPut, fmt.Sprintf("/configuration-sets/%d", e.configSetID), updateData)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
}

// getAvailableTransitions fetches available transitions for the test item.
func (e *conditionTestEnv) getAvailableTransitions(t *testing.T) (currentStatus string, transitions []map[string]interface{}) {
	t.Helper()

	resp := MakeAuthRequest(t, e.server, http.MethodGet, fmt.Sprintf("/items/%d/available-status-transitions", e.itemID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)

	currentStatus, _ = result["current_status"].(string)
	raw, _ := result["available_transitions"].([]interface{})
	for _, r := range raw {
		if m, ok := r.(map[string]interface{}); ok {
			transitions = append(transitions, m)
		}
	}
	return currentStatus, transitions
}

// hasTransitionToStatus checks if a status ID is in the available transitions list.
func hasTransitionToStatus(transitions []map[string]interface{}, statusID int) bool {
	for _, tr := range transitions {
		if id, ok := tr["id"].(float64); ok && int(id) == statusID {
			return true
		}
	}
	return false
}

// TestConditionSet_ScriptBlocksTransition verifies that "return false" hides
// the Open→In Progress transition while Open→Done remains available.
func TestConditionSet_ScriptBlocksTransition(t *testing.T) {
	env := setupConditionTestData(t)

	trID := env.findTransitionID(t, env.openID, env.inProgressID)
	csID := env.createConditionSet(t, "Block InProgress", trID, "return false", "condition")
	env.associateConditionSet(t, csID)

	_, transitions := env.getAvailableTransitions(t)

	if hasTransitionToStatus(transitions, env.inProgressID) {
		t.Error("In Progress should NOT be in available transitions when script returns false")
	}
	if !hasTransitionToStatus(transitions, env.doneID) {
		t.Error("Done should still be in available transitions (not gated)")
	}
}

// TestConditionSet_ScriptAllowsTransition verifies that "return true" keeps
// the transition available.
func TestConditionSet_ScriptAllowsTransition(t *testing.T) {
	env := setupConditionTestData(t)

	trID := env.findTransitionID(t, env.openID, env.inProgressID)
	csID := env.createConditionSet(t, "Allow InProgress", trID, "return true", "condition")
	env.associateConditionSet(t, csID)

	_, transitions := env.getAvailableTransitions(t)

	if !hasTransitionToStatus(transitions, env.inProgressID) {
		t.Error("In Progress should be in available transitions when script returns true")
	}
}

// TestConditionSet_ReturnFalseBlocksTransition tests the exact case that was broken:
// "return false" as a script (the IIFE fallback fix).
func TestConditionSet_ReturnFalseBlocksTransition(t *testing.T) {
	env := setupConditionTestData(t)

	trID := env.findTransitionID(t, env.openID, env.inProgressID)
	csID := env.createConditionSet(t, "Return False Block", trID, "return false", "condition")
	env.associateConditionSet(t, csID)

	_, transitions := env.getAvailableTransitions(t)

	if hasTransitionToStatus(transitions, env.inProgressID) {
		t.Error("'return false' script should block the transition")
	}
}

// TestConditionSet_BareExpressionFalse tests a bare "false" expression (no return).
func TestConditionSet_BareExpressionFalse(t *testing.T) {
	env := setupConditionTestData(t)

	trID := env.findTransitionID(t, env.openID, env.inProgressID)
	csID := env.createConditionSet(t, "Bare False", trID, "false", "condition")
	env.associateConditionSet(t, csID)

	_, transitions := env.getAvailableTransitions(t)

	if hasTransitionToStatus(transitions, env.inProgressID) {
		t.Error("bare 'false' expression should block the transition")
	}
}

// TestConditionSet_ScriptWithItemContext tests a script that uses the item context variable.
func TestConditionSet_ScriptWithItemContext(t *testing.T) {
	env := setupConditionTestData(t)

	trID := env.findTransitionID(t, env.openID, env.inProgressID)
	csID := env.createConditionSet(t, "Item Context", trID, "return item.priority_id === 999", "condition")
	env.associateConditionSet(t, csID)

	_, transitions := env.getAvailableTransitions(t)

	if hasTransitionToStatus(transitions, env.inProgressID) {
		t.Error("script checking non-matching item.priority_id should block transition")
	}
}

// TestItemUpdate_RejectsStatusID verifies that PUT /items/{id} with status_id
// in the body is rejected outright. Status changes must go through the
// dedicated transition endpoint so workflow + condition rules always apply.
// This closes a bypass that previously allowed skipping condition-mode rules
// by calling the generic update with a direct status_id.
func TestItemUpdate_RejectsStatusID(t *testing.T) {
	env := setupConditionTestData(t)

	resp := MakeAuthRequest(t, env.server, http.MethodPut, fmt.Sprintf("/items/%d", env.itemID), map[string]interface{}{
		"status_id": env.inProgressID,
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 when PUT /items/{id} includes status_id, got %d", resp.StatusCode)
	}
}

// TestItemUpdate_RejectsStatusIDEvenWhenConditionsWouldAllow verifies the
// reject is categorical — even a transition that a validator would pass is
// still rejected at the update endpoint.
func TestItemUpdate_RejectsStatusIDEvenWhenConditionsWouldAllow(t *testing.T) {
	env := setupConditionTestData(t)

	trID := env.findTransitionID(t, env.openID, env.inProgressID)
	csID := env.createConditionSet(t, "Validator Allow", trID, "return true", "validator")
	env.associateConditionSet(t, csID)

	resp := MakeAuthRequest(t, env.server, http.MethodPut, fmt.Sprintf("/items/%d", env.itemID), map[string]interface{}{
		"status_id": env.inProgressID,
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 regardless of condition outcome; got %d", resp.StatusCode)
	}
}
