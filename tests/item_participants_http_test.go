package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestItemParticipantHTTPLifecycle covers the external request participant
// surface end to end: empty list, add by email (auto-creating the portal
// customer), idempotent re-add, persisted reload, removal, and the item
// history entries the change records.
func TestItemParticipantHTTPLifecycle(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Participants", shortKey("PRT"))
	itemID := CreateTestItem(t, server, workspaceID, "Participant ticket")
	base := fmt.Sprintf("/v2/items/%d/participants", itemID)

	// Empty list.
	resp := MakeAuthRequest(t, server, http.MethodGet, base, nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var empty []map[string]interface{}
	DecodeJSON(t, resp, &empty)
	resp.Body.Close()
	if len(empty) != 0 {
		t.Fatalf("initial participants = %v, want empty", empty)
	}

	// Add by email. The address is normalized and the customer auto-created.
	resp = MakeAuthRequest(t, server, http.MethodPost, base, map[string]interface{}{
		"email": "Alice.External@Example.test",
		"name":  "Alice External",
	})
	AssertStatusCode(t, resp, http.StatusOK)
	var list []map[string]interface{}
	DecodeJSON(t, resp, &list)
	resp.Body.Close()
	if len(list) != 1 {
		t.Fatalf("participants after add = %v, want one", list)
	}
	participant := list[0]
	customerID := int(participant["portal_customer_id"].(float64))
	if customerID == 0 {
		t.Fatalf("portal_customer_id = %v, want non-zero", participant["portal_customer_id"])
	}
	if participant["customer_email"] != "alice.external@example.test" {
		t.Fatalf("customer_email = %v, want normalized address", participant["customer_email"])
	}
	if participant["customer_name"] != "Alice External" {
		t.Fatalf("customer_name = %v, want Alice External", participant["customer_name"])
	}

	// Idempotent re-add: same email, still exactly one participant.
	resp = MakeAuthRequest(t, server, http.MethodPost, base, map[string]interface{}{
		"email": "alice.external@example.test",
	})
	AssertStatusCode(t, resp, http.StatusOK)
	var reAdded []map[string]interface{}
	DecodeJSON(t, resp, &reAdded)
	resp.Body.Close()
	if len(reAdded) != 1 {
		t.Fatalf("participants after duplicate add = %v, want one", reAdded)
	}

	// Reload proves persistence.
	resp = MakeAuthRequest(t, server, http.MethodGet, base, nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var reloaded []map[string]interface{}
	DecodeJSON(t, resp, &reloaded)
	resp.Body.Close()
	if len(reloaded) != 1 || int(reloaded[0]["portal_customer_id"].(float64)) != customerID {
		t.Fatalf("reloaded participants = %v, want customer %d", reloaded, customerID)
	}

	// Add a second participant by explicit portal customer id.
	resp = MakeAuthRequest(t, server, http.MethodPost, base, map[string]interface{}{
		"email": "bob@example.test", "name": "Bob External",
	})
	AssertStatusCode(t, resp, http.StatusOK)
	var two []map[string]interface{}
	DecodeJSON(t, resp, &two)
	resp.Body.Close()
	if len(two) != 2 {
		t.Fatalf("participants after second add = %v, want two", two)
	}
	var bobID int
	for _, p := range two {
		if p["customer_email"] == "bob@example.test" {
			bobID = int(p["portal_customer_id"].(float64))
		}
	}
	if bobID == 0 {
		t.Fatalf("bob participant not found in %v", two)
	}

	// Remove Alice by customer id.
	resp = MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("%s/%d", base, customerID), nil)
	AssertStatusCode(t, resp, http.StatusNoContent)
	resp.Body.Close()
	resp = MakeAuthRequest(t, server, http.MethodGet, base, nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var afterRemove []map[string]interface{}
	DecodeJSON(t, resp, &afterRemove)
	resp.Body.Close()
	if len(afterRemove) != 1 || int(afterRemove[0]["portal_customer_id"].(float64)) != bobID {
		t.Fatalf("participants after remove = %v, want only bob %d", afterRemove, bobID)
	}

	// Removing an unknown portal customer is a 404.
	resp = MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("%s/%d", base, 999999), nil)
	AssertStatusCode(t, resp, http.StatusNotFound)
	resp.Body.Close()

	// Item history records both the add and the removal.
	resp = MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/items/%d/history", itemID), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var history []map[string]interface{}
	DecodeJSON(t, resp, &history)
	resp.Body.Close()
	fields := make(map[string]bool)
	for _, entry := range history {
		if name, ok := entry["field_name"].(string); ok {
			fields[name] = true
		}
	}
	if !fields["participant_added"] || !fields["participant_removed"] {
		t.Fatalf("item history fields = %v, want participant_added and participant_removed", fields)
	}
}

// TestItemParticipantHTTPValidation rejects missing and malformed identities.
func TestItemParticipantHTTPValidation(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Participants validation", shortKey("PRV"))
	itemID := CreateTestItem(t, server, workspaceID, "Validation ticket")
	base := fmt.Sprintf("/v2/items/%d/participants", itemID)

	tests := []struct {
		name string
		body map[string]interface{}
	}{
		{name: "no identity", body: map[string]interface{}{}},
		{name: "blank email", body: map[string]interface{}{"email": "   "}},
		{name: "malformed email", body: map[string]interface{}{"email": "not-an-email"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resp := MakeAuthRequest(t, server, http.MethodPost, base, test.body)
			AssertStatusCode(t, resp, http.StatusBadRequest)
			resp.Body.Close()
		})
	}
}

// TestItemParticipantHTTPDeniedWithoutWorkspaceAccess asserts the exact denial
// contract: a caller with no access to the item's workspace cannot list or
// mutate participants and cannot tell the item exists.
func TestItemParticipantHTTPDeniedWithoutWorkspaceAccess(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Participants deny", shortKey("PRD"))
	LockDownWorkspace(t, server, workspaceID)
	itemID := CreateTestItem(t, server, workspaceID, "Private participant ticket")
	base := fmt.Sprintf("/v2/items/%d/participants", itemID)

	_, username, password := CreateTestUserWithCredentials(t, server, "participant_outsider", "outsider@example.test")
	cookie, _ := CreateAuthCredentialsForUser(t, server, username, password)

	resp := MakeAuthRequestWithToken(t, server, cookie, http.MethodGet, base, nil)
	AssertStatusCode(t, resp, http.StatusNotFound)
	resp.Body.Close()

	resp = MakeAuthRequestWithToken(t, server, cookie, http.MethodPost, base, map[string]interface{}{
		"email": "intruder@example.test",
	})
	AssertStatusCode(t, resp, http.StatusNotFound)
	resp.Body.Close()

	resp = MakeAuthRequestWithToken(t, server, cookie, http.MethodDelete, fmt.Sprintf("%s/1", base), nil)
	AssertStatusCode(t, resp, http.StatusNotFound)
	resp.Body.Close()
}
