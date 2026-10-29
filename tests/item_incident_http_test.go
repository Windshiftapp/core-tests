package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestItemIncidentLifecycleAuthorization covers the item-scoped incident
// surface end to end: trigger requires item edit permission, acknowledge and
// resolve require responder-ship (team membership) rather than item
// permission, and a resolved incident can be re-triggered as a new row.
func TestItemIncidentLifecycleAuthorization(t *testing.T) {
	dbType := GetDBType()
	server, cleanup := StartTestServer(t, dbType)
	defer cleanup()
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Incident lifecycle", shortKey("INL"))
	itemTypes := GetItemTypes(t, server, GetDefaultConfigurationSet(t, server))
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")

	// Team and its active escalation policy.
	teamResp := MakeAuthRequest(t, server, http.MethodPost, "/teams", map[string]interface{}{"name": "Incident Team"})
	AssertStatusCode(t, teamResp, http.StatusCreated)
	var team map[string]interface{}
	DecodeJSON(t, teamResp, &team)
	teamResp.Body.Close()
	teamID := int(team["id"].(float64))

	policyResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/teams/%d/on-call/escalation-policies", teamID), map[string]interface{}{
		"name": "Primary", "repeat_count": 1,
	})
	AssertStatusCode(t, policyResp, http.StatusCreated)
	var policy map[string]interface{}
	DecodeJSON(t, policyResp, &policy)
	policyResp.Body.Close()
	policyID := int(policy["id"].(float64))

	// Responder is a team member with no workspace role.
	responderID, responderUser, responderPass := CreateTestUserWithCredentials(t, server, "incident-responder", "incident-responder@example.com")
	addResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/teams/%d/members", teamID), map[string]interface{}{
		"user_ids": []int{responderID}, "role": "member",
	})
	AssertStatusCode(t, addResp, http.StatusOK)
	addResp.Body.Close()
	responderToken := CreateBearerTokenForUser(t, server, responderUser, responderPass)

	// Non-responder is a workspace viewer, not on the team.
	viewerID, viewerUser, viewerPass := CreateTestUserWithCredentials(t, server, "incident-viewer", "incident-viewer@example.com")
	AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")
	viewerToken := CreateBearerTokenForUser(t, server, viewerUser, viewerPass)

	// Item assigned to the team.
	createResp := MakeAuthRequest(t, server, http.MethodPost, "/v2/items", map[string]interface{}{
		"workspace_id": workspaceID, "item_type_id": itemTypeID, "title": "Disk full on db1", "team_id": teamID,
	})
	AssertStatusCode(t, createResp, http.StatusCreated)
	var item map[string]interface{}
	DecodeJSON(t, createResp, &item)
	createResp.Body.Close()
	itemID := int(item["id"].(float64))
	if item["team_id"] == nil || int(item["team_id"].(float64)) != teamID {
		t.Fatalf("created item team_id = %v, want %d", item["team_id"], teamID)
	}

	// Workspace viewers cannot trigger (needs item edit).
	triggerDenied := MakeAuthRequestWithToken(t, server, viewerToken, http.MethodPost, fmt.Sprintf("/items/%d/incident", itemID), nil)
	AssertStatusCode(t, triggerDenied, http.StatusForbidden)
	triggerDenied.Body.Close()

	triggerResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/incident", itemID), nil)
	AssertStatusCode(t, triggerResp, http.StatusCreated)
	var incident map[string]interface{}
	DecodeJSON(t, triggerResp, &incident)
	triggerResp.Body.Close()
	if incident["status"] != "triggered" {
		t.Fatalf("triggered incident status = %v, want triggered", incident["status"])
	}
	if incident["escalation_policy_id"] == nil || int(incident["escalation_policy_id"].(float64)) != policyID {
		t.Fatalf("incident policy = %v, want %d", incident["escalation_policy_id"], policyID)
	}

	// A second trigger while open is a conflict.
	duplicate := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/incident", itemID), nil)
	AssertStatusCode(t, duplicate, http.StatusConflict)
	duplicate.Body.Close()

	// The workspace viewer can read the incident but cannot acknowledge it.
	getResp := MakeAuthRequestWithToken(t, server, viewerToken, http.MethodGet, fmt.Sprintf("/items/%d/incident", itemID), nil)
	AssertStatusCode(t, getResp, http.StatusOK)
	getResp.Body.Close()
	ackDenied := MakeAuthRequestWithToken(t, server, viewerToken, http.MethodPost, fmt.Sprintf("/items/%d/incident/acknowledge", itemID), nil)
	AssertStatusCode(t, ackDenied, http.StatusForbidden)
	ackDenied.Body.Close()

	// The on-team responder can acknowledge without any workspace role.
	ackResp := MakeAuthRequestWithToken(t, server, responderToken, http.MethodPost, fmt.Sprintf("/items/%d/incident/acknowledge", itemID), nil)
	AssertStatusCode(t, ackResp, http.StatusOK)
	var acked map[string]interface{}
	DecodeJSON(t, ackResp, &acked)
	ackResp.Body.Close()
	if acked["status"] != "acknowledged" || acked["acknowledged_by"] == nil || int(acked["acknowledged_by"].(float64)) != responderID {
		t.Fatalf("acked incident = %v, want acknowledged by %d", acked, responderID)
	}

	unackResp := MakeAuthRequestWithToken(t, server, responderToken, http.MethodPost, fmt.Sprintf("/items/%d/incident/unacknowledge", itemID), nil)
	AssertStatusCode(t, unackResp, http.StatusOK)
	var unacked map[string]interface{}
	DecodeJSON(t, unackResp, &unacked)
	unackResp.Body.Close()
	if unacked["status"] != "triggered" {
		t.Fatalf("unacked incident status = %v, want triggered", unacked["status"])
	}

	resolveResp := MakeAuthRequestWithToken(t, server, responderToken, http.MethodPost, fmt.Sprintf("/items/%d/incident/resolve", itemID), nil)
	AssertStatusCode(t, resolveResp, http.StatusOK)
	var resolved map[string]interface{}
	DecodeJSON(t, resolveResp, &resolved)
	resolveResp.Body.Close()
	if resolved["status"] != "resolved" {
		t.Fatalf("resolved incident status = %v, want resolved", resolved["status"])
	}

	// Re-triggering after resolve creates a new incident on the same item.
	retriggerResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/incident", itemID), nil)
	AssertStatusCode(t, retriggerResp, http.StatusCreated)
	var second map[string]interface{}
	DecodeJSON(t, retriggerResp, &second)
	retriggerResp.Body.Close()
	if second["id"] == incident["id"] {
		t.Fatalf("re-trigger reused incident id %v, want a new incident", incident["id"])
	}
}
