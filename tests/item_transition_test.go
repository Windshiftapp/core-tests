package tests

import (
	"fmt"
	"io"
	"net/http"
	"testing"

	"windshift/internal/constants"
)

// TestTransitionEndpoint_HappyPath verifies POST /items/{id}/transition moves
// an item through a valid workflow transition.
func TestTransitionEndpoint_HappyPath(t *testing.T) {
	env := setupConditionTestData(t)

	resp := MakeAuthRequest(t, env.server, http.MethodPost,
		fmt.Sprintf("/items/%d/transition", env.itemID),
		map[string]interface{}{"to_status_id": env.inProgressID})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)

	if noOp, _ := result["no_op"].(bool); noOp {
		t.Error("expected no_op=false for a real transition")
	}
	if newID, _ := result["new_status_id"].(float64); int(newID) != env.inProgressID {
		t.Errorf("expected new_status_id=%d, got %v", env.inProgressID, result["new_status_id"])
	}
	if oldID, _ := result["old_status_id"].(float64); int(oldID) != env.openID {
		t.Errorf("expected old_status_id=%d, got %v", env.openID, result["old_status_id"])
	}
}

// TestTransitionEndpoint_PersonalTask verifies workflow-free personal tasks
// can be completed and reopened through the shared transition endpoint.
func TestTransitionEndpoint_PersonalTask(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	workspaceResponse := MakeAuthRequest(t, server, http.MethodGet, "/workspaces/personal", nil)
	defer workspaceResponse.Body.Close()
	AssertStatusCode(t, workspaceResponse, http.StatusCreated)
	var workspace struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, workspaceResponse, &workspace)
	if workspace.ID == 0 {
		t.Fatal("personal workspace response did not include an ID")
	}

	itemResponse := MakeAuthRequest(t, server, http.MethodPost, "/items", map[string]interface{}{
		"workspace_id": workspace.ID,
		"title":        "Personal task",
	})
	defer itemResponse.Body.Close()
	AssertStatusCode(t, itemResponse, http.StatusCreated)
	var item struct {
		ID       int  `json:"id"`
		StatusID *int `json:"status_id"`
	}
	DecodeJSON(t, itemResponse, &item)
	if item.ID == 0 || item.StatusID == nil {
		t.Fatalf("personal task response = %#v, want an ID and current status", item)
	}
	if *item.StatusID != constants.StatusIDOpen {
		t.Fatalf("new personal task status = %d, want Open (%d)", *item.StatusID, constants.StatusIDOpen)
	}

	completeResponse := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/items/%d/transition", item.ID),
		map[string]interface{}{"to_status_id": constants.StatusIDDone})
	defer completeResponse.Body.Close()
	AssertStatusCode(t, completeResponse, http.StatusOK)

	var completeResult struct {
		NewStatusID int `json:"new_status_id"`
	}
	DecodeJSON(t, completeResponse, &completeResult)
	if completeResult.NewStatusID != constants.StatusIDDone {
		t.Fatalf("completed task status = %d, want %d", completeResult.NewStatusID, constants.StatusIDDone)
	}

	reopenResponse := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/items/%d/transition", item.ID),
		map[string]interface{}{"to_status_id": constants.StatusIDOpen})
	defer reopenResponse.Body.Close()
	AssertStatusCode(t, reopenResponse, http.StatusOK)

	var reopenResult struct {
		NewStatusID int `json:"new_status_id"`
	}
	DecodeJSON(t, reopenResponse, &reopenResult)
	if reopenResult.NewStatusID != constants.StatusIDOpen {
		t.Fatalf("reopened task status = %d, want %d", reopenResult.NewStatusID, constants.StatusIDOpen)
	}
}

// TestTransitionEndpoint_NoOp verifies transitioning to the current status
// returns 200 with no_op=true and does not emit side effects.
func TestTransitionEndpoint_NoOp(t *testing.T) {
	env := setupConditionTestData(t)

	resp := MakeAuthRequest(t, env.server, http.MethodPost,
		fmt.Sprintf("/items/%d/transition", env.itemID),
		map[string]interface{}{"to_status_id": env.openID})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)

	if noOp, _ := result["no_op"].(bool); !noOp {
		t.Error("expected no_op=true when target equals current status")
	}
}

// TestTransitionEndpoint_InvalidWorkflowTransition verifies the endpoint
// returns 400 when the from→to pair is not configured in the workflow.
func TestTransitionEndpoint_InvalidWorkflowTransition(t *testing.T) {
	env := setupConditionTestData(t)

	// Move Open → In Progress first so we can try an invalid In Progress → Open next.
	step := MakeAuthRequest(t, env.server, http.MethodPost,
		fmt.Sprintf("/items/%d/transition", env.itemID),
		map[string]interface{}{"to_status_id": env.inProgressID})
	AssertStatusCode(t, step, http.StatusOK)
	step.Body.Close()

	// In Progress → Open is not in env.transitions (see setupConditionTestData).
	resp := MakeAuthRequest(t, env.server, http.MethodPost,
		fmt.Sprintf("/items/%d/transition", env.itemID),
		map[string]interface{}{"to_status_id": env.openID})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for transition not in workflow, got %d", resp.StatusCode)
	}
}

// TestTransitionEndpoint_ValidatorBlocks verifies a validator-mode condition
// returning false causes the transition endpoint to return 400 with the
// condition's error_message.
func TestTransitionEndpoint_ValidatorBlocks(t *testing.T) {
	env := setupConditionTestData(t)

	trID := env.findTransitionID(t, env.openID, env.inProgressID)
	csID := env.createConditionSet(t, "Validator Block Endpoint", trID, "return false", "validator")
	env.associateConditionSet(t, csID)

	resp := MakeAuthRequest(t, env.server, http.MethodPost,
		fmt.Sprintf("/items/%d/transition", env.itemID),
		map[string]interface{}{"to_status_id": env.inProgressID})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 400 when validator blocks transition, got %d. body=%s", resp.StatusCode, string(body))
	}
}

// TestTransitionEndpoint_ConditionModeHardBlocks verifies that condition-mode
// scripts (not just validator-mode) hard-block the transition endpoint. This
// is the core behavior change introduced alongside the dedicated endpoint.
// Previously, condition-mode was UI-filter only and could be bypassed by
// calling the API with a direct status_id.
func TestTransitionEndpoint_ConditionModeHardBlocks(t *testing.T) {
	env := setupConditionTestData(t)

	trID := env.findTransitionID(t, env.openID, env.inProgressID)
	csID := env.createConditionSet(t, "Condition Mode Endpoint Block", trID, "return false", "condition")
	env.associateConditionSet(t, csID)

	resp := MakeAuthRequest(t, env.server, http.MethodPost,
		fmt.Sprintf("/items/%d/transition", env.itemID),
		map[string]interface{}{"to_status_id": env.inProgressID})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 400 when condition-mode blocks transition endpoint, got %d. body=%s", resp.StatusCode, string(body))
	}
}

// TestTransitionEndpoint_ConditionModeStillFiltersAvailableTransitions
// verifies that even though condition-mode now hard-blocks on the transition
// endpoint, it continues to filter the GET /items/{id}/available-status-transitions
// list. Both behaviors coexist.
func TestTransitionEndpoint_ConditionModeStillFiltersAvailableTransitions(t *testing.T) {
	env := setupConditionTestData(t)

	trID := env.findTransitionID(t, env.openID, env.inProgressID)
	csID := env.createConditionSet(t, "Condition Mode UI Filter", trID, "return false", "condition")
	env.associateConditionSet(t, csID)

	_, transitions := env.getAvailableTransitions(t)
	if hasTransitionToStatus(transitions, env.inProgressID) {
		t.Error("condition-mode should still hide In Progress from the GET list")
	}
	if !hasTransitionToStatus(transitions, env.doneID) {
		t.Error("Done should still be available (not gated)")
	}
}

// TestTransitionEndpoint_MissingToStatusID verifies a 400 is returned when
// to_status_id is omitted from the request body.
func TestTransitionEndpoint_MissingToStatusID(t *testing.T) {
	env := setupConditionTestData(t)

	resp := MakeAuthRequest(t, env.server, http.MethodPost,
		fmt.Sprintf("/items/%d/transition", env.itemID),
		map[string]interface{}{})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 when to_status_id missing, got %d", resp.StatusCode)
	}
}

// TestTransitionEndpoint_UnknownItem verifies a 404 for a non-existent item.
func TestTransitionEndpoint_UnknownItem(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	resp := MakeAuthRequest(t, server, http.MethodPost,
		"/items/999999/transition",
		map[string]interface{}{"to_status_id": 1})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for unknown item, got %d", resp.StatusCode)
	}
}
