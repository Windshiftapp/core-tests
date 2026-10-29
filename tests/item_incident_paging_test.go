package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestItemIncidentPagesResponder proves the escalation step actually delivers a
// page: a triggered incident with an in-app notification rule targeting an
// on-call responder produces a visible notification for them, even though the
// responder has no workspace role on the item's workspace.
func TestItemIncidentPagesResponder(t *testing.T) {
	dbType := GetDBType()
	server, cleanup := StartTestServer(t, dbType)
	defer cleanup()
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Incident paging", shortKey("INP"))
	itemTypes := GetItemTypes(t, server, GetDefaultConfigurationSet(t, server))
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")

	teamResp := MakeAuthRequest(t, server, http.MethodPost, "/teams", map[string]interface{}{"name": "Paging Team"})
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

	responderID, responderUser, responderPass := CreateTestUserWithCredentials(t, server, "incident-paged", "incident-paged@example.com")
	addResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/teams/%d/members", teamID), map[string]interface{}{
		"user_ids": []int{responderID}, "role": "member",
	})
	AssertStatusCode(t, addResp, http.StatusOK)
	addResp.Body.Close()
	responderToken := CreateBearerTokenForUser(t, server, responderUser, responderPass)

	rulesResp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/on-call/escalation-policies/%d/rules", policyID), map[string]interface{}{
		"rules": []map[string]interface{}{
			{
				"step_order":               0,
				"escalation_delay_minutes": 30,
				"target_type":              "user",
				"target_id":                responderID,
				"notification_rules": []map[string]interface{}{
					{"notification_type": "in_app", "delay_minutes": 0, "repeat_count": 1},
				},
			},
		},
	})
	AssertStatusCode(t, rulesResp, http.StatusOK)
	rulesResp.Body.Close()

	createResp := MakeAuthRequest(t, server, http.MethodPost, "/v2/items", map[string]interface{}{
		"workspace_id": workspaceID, "item_type_id": itemTypeID, "title": "Page someone", "team_id": teamID,
	})
	AssertStatusCode(t, createResp, http.StatusCreated)
	var item map[string]interface{}
	DecodeJSON(t, createResp, &item)
	createResp.Body.Close()
	itemID := int(item["id"].(float64))

	triggerResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/incident", itemID), nil)
	AssertStatusCode(t, triggerResp, http.StatusCreated)
	triggerResp.Body.Close()

	notifResp := MakeAuthRequestWithToken(t, server, responderToken, http.MethodGet, "/notifications", nil)
	AssertStatusCode(t, notifResp, http.StatusOK)
	var notifications []map[string]interface{}
	DecodeJSON(t, notifResp, &notifications)
	notifResp.Body.Close()

	found := false
	for _, notification := range notifications {
		if notification["type"] != "incident" {
			continue
		}
		actionURL, _ := notification["action_url"].(string)
		if !strings.Contains(actionURL, fmt.Sprintf("/items/%d", itemID)) {
			continue
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("responder notifications = %#v, want an incident page for item %d", notifications, itemID)
	}
}
