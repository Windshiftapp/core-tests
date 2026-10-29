package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestItemTeamAssignmentUpdatePersists covers the canonical item PATCH path:
// team_id must survive validation, the repository update, and the reloaded
// response, and an inactive team must be rejected.
func TestItemTeamAssignmentUpdatePersists(t *testing.T) {
	dbType := GetDBType()
	server, cleanup := StartTestServer(t, dbType)
	defer cleanup()
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Team assignment", shortKey("TMA"))
	itemTypes := GetItemTypes(t, server, GetDefaultConfigurationSet(t, server))
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")

	teamResp := MakeAuthRequest(t, server, http.MethodPost, "/teams", map[string]interface{}{"name": "Assignment Team"})
	AssertStatusCode(t, teamResp, http.StatusCreated)
	var team map[string]interface{}
	DecodeJSON(t, teamResp, &team)
	teamResp.Body.Close()
	teamID := int(team["id"].(float64))

	inactiveResp := MakeAuthRequest(t, server, http.MethodPost, "/teams", map[string]interface{}{"name": "Inactive Team"})
	AssertStatusCode(t, inactiveResp, http.StatusCreated)
	var inactive map[string]interface{}
	DecodeJSON(t, inactiveResp, &inactive)
	inactiveResp.Body.Close()
	inactiveTeamID := int(inactive["id"].(float64))
	deactivateResp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/teams/%d", inactiveTeamID), map[string]interface{}{
		"name": "Inactive Team", "is_active": false,
	})
	AssertStatusCode(t, deactivateResp, http.StatusOK)
	deactivateResp.Body.Close()

	createResp := MakeAuthRequest(t, server, http.MethodPost, "/v2/items", map[string]interface{}{
		"workspace_id": workspaceID, "item_type_id": itemTypeID, "title": "Assign a team",
	})
	AssertStatusCode(t, createResp, http.StatusCreated)
	var item map[string]interface{}
	DecodeJSON(t, createResp, &item)
	createResp.Body.Close()
	itemID := int(item["id"].(float64))

	patchResp := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/items/%d", itemID), map[string]interface{}{
		"team_id": teamID,
	})
	AssertStatusCode(t, patchResp, http.StatusOK)
	var patched map[string]interface{}
	DecodeJSON(t, patchResp, &patched)
	patchResp.Body.Close()
	if patched["team_id"] == nil || int(patched["team_id"].(float64)) != teamID {
		t.Fatalf("patched item team_id = %v, want %d", patched["team_id"], teamID)
	}
	if patched["team_name"] != "Assignment Team" {
		t.Fatalf("patched item team_name = %v, want Assignment Team", patched["team_name"])
	}

	// Reload proves persistence.
	reloadResp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/items/%d", itemID), nil)
	AssertStatusCode(t, reloadResp, http.StatusOK)
	var reloaded map[string]interface{}
	DecodeJSON(t, reloadResp, &reloaded)
	reloadResp.Body.Close()
	if reloaded["team_id"] == nil || int(reloaded["team_id"].(float64)) != teamID {
		t.Fatalf("reloaded item team_id = %v, want %d", reloaded["team_id"], teamID)
	}

	// Inactive teams are rejected on the update path too.
	inactivePatch := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/items/%d", itemID), map[string]interface{}{
		"team_id": inactiveTeamID,
	})
	AssertStatusCode(t, inactivePatch, http.StatusBadRequest)
	inactivePatch.Body.Close()
}
