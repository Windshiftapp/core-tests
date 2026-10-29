package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestWorkspaceStatusesWorkflowScope pins GH #265's contract: a workspace on a
// configuration set without item-type workflow mappings must expose only the
// statuses of that set's effective workflow — never statuses from unrelated
// workflows. Once a type is explicitly mapped to another workflow, the
// workspace-level list unions every applicable workflow (reference data;
// actual transitions stay gated by the item's own effective workflow).
func TestWorkspaceStatusesWorkflowScope(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Status Scope WS", "")

	// Distinct statuses reachable only through the foreign workflow.
	categoryResp := MakeAuthRequest(t, server, http.MethodGet, "/status-categories", nil)
	defer categoryResp.Body.Close()
	AssertStatusCode(t, categoryResp, http.StatusOK)
	var categories []map[string]interface{}
	DecodeJSON(t, categoryResp, &categories)
	categoryIDs := make([]int, 0, len(categories))
	for _, category := range categories {
		categoryIDs = append(categoryIDs, ExtractIDFromResponse(t, category))
	}
	if len(categoryIDs) < 3 {
		t.Fatalf("seeded status categories = %d, want at least 3", len(categoryIDs))
	}
	foreignStatusIDs := CreateTestStatuses(t, server, "ForeignScope", categoryIDs[:3])

	foreignWorkflowID := func() int {
		wfResp := MakeAuthRequest(t, server, http.MethodPost, "/workflows", map[string]interface{}{
			"name": "Foreign Scope Flow",
		})
		defer wfResp.Body.Close()
		AssertStatusCode(t, wfResp, http.StatusCreated)
		var wf map[string]interface{}
		DecodeJSON(t, wfResp, &wf)
		workflowID := ExtractIDFromResponse(t, wf)

		transitions := []map[string]interface{}{
			{"from_status_id": nil, "to_status_id": foreignStatusIDs[0]},
		}
		for _, sid := range foreignStatusIDs[1:] {
			transitions = append(transitions, map[string]interface{}{
				"from_status_id": foreignStatusIDs[0],
				"to_status_id":   sid,
			})
		}
		txResp := MakeAuthRequest(t, server, http.MethodPut,
			fmt.Sprintf("/workflows/%d/transitions", workflowID), transitions)
		defer txResp.Body.Close()
		AssertStatusCode(t, txResp, http.StatusOK)
		return workflowID
	}()

	// The seeded default configuration set is assigned to every workspace and
	// runs on the seeded default workflow — the reporter's "relies on the
	// default workflow without explicitly mapping issue types" scenario.
	configSetID := GetDefaultConfigurationSet(t, server)

	listStatuses := func() []map[string]interface{} {
		resp := MakeAuthRequest(t, server, http.MethodGet,
			fmt.Sprintf("/workspaces/%d/statuses", wsID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var statuses []map[string]interface{}
		DecodeJSON(t, resp, &statuses)
		return statuses
	}
	containsStatus := func(statuses []map[string]interface{}, statusID int) bool {
		for _, status := range statuses {
			if ExtractIDFromResponse(t, status) == statusID {
				return true
			}
		}
		return false
	}

	// Unmapped: the foreign workflow's statuses must not be offered.
	unmapped := listStatuses()
	for _, foreignID := range foreignStatusIDs {
		if containsStatus(unmapped, foreignID) {
			t.Fatalf("unmapped config set exposes foreign status %d: %#v", foreignID, unmapped)
		}
	}

	// Explicitly map one item type to the foreign workflow: its statuses join
	// the workspace-level reference list (union of applicable workflows).
	typesResp := MakeAuthRequest(t, server, http.MethodGet, "/item-types", nil)
	defer typesResp.Body.Close()
	AssertStatusCode(t, typesResp, http.StatusOK)
	var itemTypes []map[string]interface{}
	DecodeJSON(t, typesResp, &itemTypes)
	if len(itemTypes) == 0 {
		t.Fatal("no item types seeded")
	}
	mappedTypeConfig := map[string]interface{}{
		"item_type_id": ExtractIDFromResponse(t, itemTypes[0]),
		"workflow_id":  foreignWorkflowID,
	}
	getResp := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/configuration-sets/%d", configSetID), nil)
	var current map[string]interface{}
	DecodeJSON(t, getResp, &current)
	getResp.Body.Close()
	currentConfigs, _ := current["item_type_configs"].([]interface{})
	mappedResp := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/configuration-sets/%d", configSetID),
		map[string]interface{}{
			"item_type_configs": append(currentConfigs, mappedTypeConfig),
		})
	defer mappedResp.Body.Close()
	AssertStatusCode(t, mappedResp, http.StatusOK)

	mapped := listStatuses()
	for _, foreignID := range foreignStatusIDs {
		if !containsStatus(mapped, foreignID) {
			t.Fatalf("explicitly mapped workflow status %d missing from workspace list: %#v", foreignID, mapped)
		}
	}
}
