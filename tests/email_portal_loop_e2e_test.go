package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"windshift/internal/blobstore"
	"windshift/internal/email"
	"windshift/internal/models"
	"windshift/internal/services"
)

// TestEmailPortalLoopEndToEnd drives the complete WI-1546 conversation loop
// with production components: a portal submission grows a thread anchor; an
// agent comment enqueues a threaded customer email in the portal channel's
// outbox; the customer's reply — simulated through the production intake
// processor, which is exactly what the IMAP scheduler invokes — lands back on
// the portal ticket; and the whole conversation is visible to the customer in
// the portal.
func TestEmailPortalLoopEndToEnd(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Loop WS", shortKey("LOOP"))
	portalSlug, portalChannelID := SetupPortalChannel(t, server, wsID)

	// Connected intake channel routed at the served workspace.
	itemTypesResp := MakeAuthRequest(t, server, http.MethodGet, "/v2/item-types", nil)
	defer itemTypesResp.Body.Close()
	AssertStatusCode(t, itemTypesResp, http.StatusOK)
	var itemTypesDoc struct {
		Data []map[string]interface{} `json:"data"`
	}
	DecodeJSON(t, itemTypesResp, &itemTypesDoc)
	if len(itemTypesDoc.Data) == 0 {
		t.Fatalf("no item types available")
	}

	createResp := MakeAuthRequest(t, server, http.MethodPost, "/channels", map[string]interface{}{
		"name":      "Loop intake",
		"type":      "email",
		"direction": "inbound",
		"status":    "disabled",
	})
	defer createResp.Body.Close()
	AssertStatusCode(t, createResp, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, createResp, &created)
	intakeChannelID := int(created["id"].(float64))
	configResp := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/channels/%d/config", intakeChannelID), map[string]interface{}{
			"config": map[string]interface{}{
				"email_auth_method":         "basic",
				"imap_host":                 fmt.Sprintf("imap-%d.example.com", intakeChannelID),
				"imap_port":                 993,
				"imap_username":             fmt.Sprintf("support-%d@example.com", intakeChannelID),
				"imap_password":             "imap-secret",
				"email_workspace_id":        wsID,
				"email_item_type_id":        itemTypesDoc.Data[0]["id"],
				"email_mailbox":             "INBOX",
				"email_connected_portal_id": portalChannelID,
			},
		})
	defer configResp.Body.Close()
	AssertStatusCode(t, configResp, http.StatusOK)

	_, customerCookie := CreatePortalCustomerWithSession(t, server, portalChannelID, "Loop Customer", "loop-customer@example.test")

	// 1. Portal submission — the anchor is minted inside the creation flow.
	itemID := SubmitPortalRequest(t, server, portalSlug, customerCookie, "Loop ticket")
	anchorID := fmt.Sprintf("<ws-item-%d@windshift.local>", itemID)
	var anchorFrom, anchorDirection string
	if err := server.DB().QueryRow(`
		SELECT from_email, direction FROM email_message_tracking
		WHERE item_id = ? AND message_id = ?
	`, itemID, anchorID).Scan(&anchorFrom, &anchorDirection); err != nil {
		t.Fatalf("portal submission did not mint its thread anchor: %v", err)
	}
	if anchorFrom != "" || anchorDirection != "outbound" {
		t.Fatalf("anchor row wrong: from=%q direction=%q", anchorFrom, anchorDirection)
	}

	// 2. Agent replies publicly — the customer must be emailed.
	commentResp := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/items/%d/comments", itemID),
		map[string]interface{}{"content": "We replaced the fuser; please test."})
	defer commentResp.Body.Close()
	AssertStatusCode(t, commentResp, http.StatusCreated)

	// The reply is enqueued by the durable comment consumer (500ms poll), so
	// wait for the row instead of racing the pump.
	deadline := time.Now().Add(10 * time.Second)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	var outboxCommentID int
	var outboxChannelID int
	var outboxTo, outboxInReplyTo, outboxMessageID string
	for {
		err := server.DB().QueryRow(`
			SELECT comment_id, channel_id, to_email, in_reply_to, message_id
			FROM email_reply_outbox WHERE item_id = ?
		`, itemID).Scan(&outboxCommentID, &outboxChannelID, &outboxTo, &outboxInReplyTo, &outboxMessageID)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent comment did not enqueue a customer reply within 10s: %v", err)
		}
		<-tick.C
	}
	if outboxChannelID != portalChannelID {
		t.Fatalf("outbox channel = %d, want origin portal channel %d", outboxChannelID, portalChannelID)
	}
	if outboxTo != "loop-customer@example.test" {
		t.Fatalf("outbox to = %q, want the customer", outboxTo)
	}
	if outboxInReplyTo != anchorID {
		t.Fatalf("outbox In-Reply-To = %q, want anchor %q", outboxInReplyTo, anchorID)
	}

	// 3. The customer replies by email, quoting the notification. The intake
	// processor is the production component the IMAP scheduler invokes.
	processor := email.NewProcessor(server.DB(), blobstore.Disabled())
	processor.SetCommentService(services.NewCommentService(server.DB()))
	reply := &email.ParsedEmail{
		UID:        1,
		MessageID:  "<customer-reply-1@example.test>",
		Subject:    "Re: Loop ticket",
		PlainBody:  "Tested — works now, thanks!",
		InReplyTo:  outboxMessageID,
		References: []string{anchorID, outboxMessageID},
		From: email.EmailAddress{
			Name:    "Loop Customer",
			Address: "loop-customer@example.test",
		},
	}
	// The intake channel polls the mailbox in production; process against it.
	result, err := processor.ProcessEmail(context.Background(), reply, intakeChannelID, 0, mapIntakeConfig(t, server, intakeChannelID))
	if err != nil {
		t.Fatalf("intake processing failed: %v", err)
	}
	if result.Action != email.ActionCommentAdded || result.ItemID == nil || *result.ItemID != itemID {
		t.Fatalf("customer reply action = %s item = %v, want comment_added on portal item %d",
			result.Action, result.ItemID, itemID)
	}

	// 4. The customer sees the whole conversation in the portal: the agent's
	// comment and their own emailed reply.
	commentsResp := MakePortalRequest(t, server, customerCookie, http.MethodGet,
		fmt.Sprintf("/portal/%s/requests/%d/comments", portalSlug, itemID), nil)
	defer commentsResp.Body.Close()
	AssertStatusCode(t, commentsResp, http.StatusOK)
	var comments []map[string]interface{}
	DecodeJSON(t, commentsResp, &comments)
	var sawAgent, sawCustomer bool
	for _, c := range comments {
		content, _ := c["content"].(string)
		if content == "We replaced the fuser; please test." {
			sawAgent = true
		}
		if content == "Tested — works now, thanks!" {
			sawCustomer = true
		}
	}
	if !sawAgent || !sawCustomer {
		t.Fatalf("portal conversation incomplete: agent=%v customer=%v in %+v", sawAgent, sawCustomer, comments)
	}

	// 5. The ticket stays visible in My Requests.
	myReqResp := MakePortalRequest(t, server, customerCookie, http.MethodGet,
		fmt.Sprintf("/portal/%s/my-requests", portalSlug), nil)
	defer myReqResp.Body.Close()
	AssertStatusCode(t, myReqResp, http.StatusOK)
	var requests []map[string]interface{}
	DecodeJSON(t, myReqResp, &requests)
	found := false
	for _, r := range requests {
		if int(r["id"].(float64)) == itemID {
			found = true
		}
	}
	if !found {
		t.Fatalf("portal ticket %d missing from my-requests", itemID)
	}
}

// mapIntakeConfig loads the intake channel's stored config for the processor,
// mirroring what the scheduler passes in.
func mapIntakeConfig(t *testing.T, server *TestServer, channelID int) *models.ChannelConfig {
	t.Helper()
	var configJSON string
	if err := server.DB().QueryRow(`SELECT config FROM channels WHERE id = ?`, channelID).Scan(&configJSON); err != nil {
		t.Fatalf("load intake config: %v", err)
	}
	var cfg models.ChannelConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		t.Fatalf("decode intake config: %v", err)
	}
	return &cfg
}
