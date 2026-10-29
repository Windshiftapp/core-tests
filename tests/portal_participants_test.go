package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestPortalRequestParticipantsVisibilityAndReplies covers WI-1136 through the
// portal surface: an external participant added by an agent can see the request
// in My Requests, read it, and reply; a non-participant cannot; internal notes
// never leak; and removing the participant revokes access immediately.
func TestPortalRequestParticipantsVisibilityAndReplies(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Portal Participants WS", shortKey("PPW"))
	portalSlug, portalChannelID := SetupPortalChannel(t, server, workspaceID)

	_, ownerCookie := CreatePortalCustomerWithSession(t, server, portalChannelID, "Owner", "pp-owner@example.test")
	participantID, participantCookie := CreatePortalCustomerWithSession(t, server, portalChannelID, "Participant", "pp-participant@example.test")
	_, outsiderCookie := CreatePortalCustomerWithSession(t, server, portalChannelID, "Outsider", "pp-outsider@example.test")

	itemID := SubmitPortalRequest(t, server, portalSlug, ownerCookie, "Participant portal request")
	detailPath := fmt.Sprintf("/portal/%s/requests/%d", portalSlug, itemID)
	commentsPath := detailPath + "/comments"

	// Baseline: the eventual participant has no access before being added.
	resp := MakePortalRequest(t, server, participantCookie, http.MethodGet, detailPath, nil)
	AssertStatusCode(t, resp, http.StatusNotFound)
	resp.Body.Close()

	// Agent adds the participant by portal customer id.
	resp = MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/v2/items/%d/participants", itemID), map[string]interface{}{
		"portal_customer_id": participantID,
	})
	AssertStatusCode(t, resp, http.StatusOK)
	resp.Body.Close()

	// The request now appears in the participant's My Requests.
	resp = MakePortalRequest(t, server, participantCookie, http.MethodGet, fmt.Sprintf("/portal/%s/my-requests", portalSlug), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var requests []map[string]interface{}
	DecodeJSON(t, resp, &requests)
	resp.Body.Close()
	found := false
	for _, request := range requests {
		if int(request["id"].(float64)) == itemID {
			found = true
		}
	}
	if !found {
		t.Fatalf("participant My Requests = %v, want item %d", requests, itemID)
	}

	// The participant can read the request and reply.
	resp = MakePortalRequest(t, server, participantCookie, http.MethodGet, detailPath, nil)
	AssertStatusCode(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = MakePortalRequest(t, server, participantCookie, http.MethodPost, commentsPath, map[string]interface{}{
		"content": "Adding context as a participant",
	})
	AssertStatusCode(t, resp, http.StatusCreated)
	resp.Body.Close()

	// An internal note is never exposed on the portal thread.
	resp = MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/v2/items/%d/comments", itemID), map[string]interface{}{
		"content":    "internal-only note",
		"is_private": true,
	})
	AssertStatusCode(t, resp, http.StatusCreated)
	resp.Body.Close()

	resp = MakePortalRequest(t, server, participantCookie, http.MethodGet, commentsPath, nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var comments []map[string]interface{}
	DecodeJSON(t, resp, &comments)
	resp.Body.Close()
	for _, comment := range comments {
		if comment["content"] == "internal-only note" {
			t.Fatalf("internal note leaked to participant: %v", comments)
		}
	}
	participantReplyVisible := false
	for _, comment := range comments {
		if comment["content"] == "Adding context as a participant" {
			participantReplyVisible = true
		}
	}
	if !participantReplyVisible {
		t.Fatalf("participant reply missing from portal comments: %v", comments)
	}

	// A non-participant still cannot read the request.
	resp = MakePortalRequest(t, server, outsiderCookie, http.MethodGet, detailPath, nil)
	AssertStatusCode(t, resp, http.StatusNotFound)
	resp.Body.Close()

	// Removing the participant revokes portal access immediately.
	resp = MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/v2/items/%d/participants/%d", itemID, participantID), nil)
	AssertStatusCode(t, resp, http.StatusNoContent)
	resp.Body.Close()

	resp = MakePortalRequest(t, server, participantCookie, http.MethodGet, detailPath, nil)
	AssertStatusCode(t, resp, http.StatusNotFound)
	resp.Body.Close()
}
