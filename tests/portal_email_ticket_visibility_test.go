package tests

import (
	"fmt"
	"io"
	"net/http"
	"testing"

	"windshift/internal/testutils"
)

// TestPortalVisibilityForEmailOriginatedTickets verifies WI-1547/WI-1644 end
// to end over HTTP: tickets created through an enabled intake linked to a
// portal appear in the customer's My Requests and are readable there, while
// disabled-intake, internal-intake, wrong-workspace, and other-customer
// tickets stay invisible.
//
// Fixture exception (documented per the repository test contract): the
// email-originated tickets cannot be produced through any HTTP API — email
// intake is IMAP-driven — so the tickets themselves are inserted directly,
// with channel and intake provenance exactly as the intake pipeline writes
// them. Every other step (portal channel, mailbox, intakes, customer session,
// portal submission) uses the production HTTP API.
func TestPortalVisibilityForEmailOriginatedTickets(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Email Portal WS", shortKey("EPW"))
	portalSlug, portalChannelID := SetupPortalChannel(t, server, wsID)

	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, "Bug")

	// One mailbox with a portal-linked intake and a sibling internal intake,
	// mirroring an upgraded mailbox whose tickets must not bleed between intakes.
	mailboxID := createMailboxChannel(t, server, "Connected mailbox")
	// The stale legacy key the intake split leaves in the channel config must not
	// grant portal visibility to the internal sibling intake (WI-1713).
	legacyConfigResp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/channels/%d/config", mailboxID), map[string]interface{}{
		"config": map[string]interface{}{"email_connected_portal_id": portalChannelID},
	})
	AssertStatusCode(t, legacyConfigResp, http.StatusOK)
	legacyConfigResp.Body.Close()
	portalIntakeID := createMailboxIntake(t, server, mailboxID, "Support", &portalChannelID, wsID, itemTypeID, "enabled")
	internalIntakeID := createMailboxIntake(t, server, mailboxID, "Alerts", nil, wsID, itemTypeID, "enabled")

	// A second mailbox whose portal-linked intake stays disabled: its tickets
	// must not surface.
	disabledMailboxID := createMailboxChannel(t, server, "Disabled mailbox")
	disabledIntakeID := createMailboxIntake(t, server, disabledMailboxID, "INBOX", &portalChannelID, wsID, itemTypeID, "disabled")

	customerID, customerCookie := CreatePortalCustomerWithSession(t, server, portalChannelID, "Vis Customer", "vis-customer@example.test")
	otherCustomerID, _ := CreatePortalCustomerWithSession(t, server, portalChannelID, "Other Customer", "other-customer@example.test")

	// Baseline: a real portal submission.
	portalTicketID := SubmitPortalRequest(t, server, portalSlug, customerCookie, "Portal request")

	// Email-originated tickets, inserted exactly as intake would write them.
	insertEmailTicket := func(t *testing.T, channelID int, intakeID *int, workspaceID, creatorID, number int, title string) int {
		t.Helper()
		var intake any
		if intakeID != nil {
			intake = *intakeID
		}
		var id int
		if err := server.DB().QueryRow(`
			INSERT INTO items (workspace_id, workspace_item_number, title, description, channel_id, intake_id, creator_portal_customer_id, frac_index)
			VALUES (?, ?, ?, 'sent by email', ?, ?, ?, ?) RETURNING id
		`, workspaceID, number, title, channelID, intake, creatorID, testutils.NextTestFracIndex()).Scan(&id); err != nil {
			t.Fatalf("insert email ticket %q: %v", title, err)
		}
		return id
	}
	linkedTicketID := insertEmailTicket(t, mailboxID, &portalIntakeID, wsID, customerID, 901, "Email ticket")
	internalTicketID := insertEmailTicket(t, mailboxID, &internalIntakeID, wsID, customerID, 902, "Internal intake ticket")
	disabledTicketID := insertEmailTicket(t, disabledMailboxID, &disabledIntakeID, wsID, customerID, 903, "Disabled intake ticket")
	otherWsID, _ := CreateTestWorkspace(t, server, "Other Email WS", shortKey("OEW"))
	wrongWorkspaceTicketID := insertEmailTicket(t, mailboxID, &portalIntakeID, otherWsID, customerID, 904, "Wrong workspace ticket")
	otherTicketID := insertEmailTicket(t, mailboxID, &portalIntakeID, wsID, otherCustomerID, 905, "Someone else's email ticket")

	// My Requests shows the portal ticket and the linked-intake email ticket,
	// and nothing else.
	myReqResp := MakePortalRequest(t, server, customerCookie, http.MethodGet,
		fmt.Sprintf("/portal/%s/my-requests", portalSlug), nil)
	defer myReqResp.Body.Close()
	AssertStatusCode(t, myReqResp, http.StatusOK)
	var requests []map[string]interface{}
	DecodeJSON(t, myReqResp, &requests)
	visible := map[int]bool{}
	for _, r := range requests {
		visible[int(r["id"].(float64))] = true
	}
	if !visible[portalTicketID] || !visible[linkedTicketID] {
		t.Fatalf("my-requests must contain portal ticket %d and email ticket %d; got %v",
			portalTicketID, linkedTicketID, visible)
	}
	for hidden, why := range map[int]string{
		internalTicketID:       "internal intake on the same mailbox",
		disabledTicketID:       "disabled linked intake",
		wrongWorkspaceTicketID: "item outside served workspaces",
		otherTicketID:          "another customer's ticket",
	} {
		if visible[hidden] {
			t.Fatalf("ticket %d visible in my-requests but must be hidden (%s)", hidden, why)
		}
	}

	// The linked-intake email ticket is readable and commentable through the
	// portal.
	detailResp := MakePortalRequest(t, server, customerCookie, http.MethodGet,
		fmt.Sprintf("/portal/%s/requests/%d", portalSlug, linkedTicketID), nil)
	defer detailResp.Body.Close()
	AssertStatusCode(t, detailResp, http.StatusOK)
	commentResp := MakePortalRequest(t, server, customerCookie, http.MethodPost,
		fmt.Sprintf("/portal/%s/requests/%d/comments", portalSlug, linkedTicketID),
		map[string]interface{}{"content": "Adding context from the portal"})
	defer commentResp.Body.Close()
	AssertStatusCode(t, commentResp, http.StatusCreated)

	// Hidden tickets stay 404-material through direct URLs.
	for hidden, why := range map[int]string{
		internalTicketID:       "internal intake on the same mailbox",
		disabledTicketID:       "disabled linked intake",
		wrongWorkspaceTicketID: "item outside served workspaces",
		otherTicketID:          "another customer's ticket",
	} {
		resp := MakePortalRequest(t, server, customerCookie, http.MethodGet,
			fmt.Sprintf("/portal/%s/requests/%d", portalSlug, hidden), nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("GET hidden ticket %d (%s) = %d, want 404; body=%s", hidden, why, resp.StatusCode, body)
		}
	}
}

// createMailboxIntake creates an intake on a mailbox through the production API.
func createMailboxIntake(t *testing.T, server *TestServer, mailboxID int, folder string, portalChannelID *int, workspaceID, itemTypeID int, status string) int {
	t.Helper()
	body := map[string]interface{}{
		"folder":       folder,
		"workspace_id": workspaceID,
		"item_type_id": itemTypeID,
		"status":       status,
	}
	if portalChannelID != nil {
		body["portal_channel_id"] = *portalChannelID
	}
	resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/intakes", mailboxID), body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create intake %s: status=%d body=%s", folder, resp.StatusCode, b)
	}
	var intake map[string]interface{}
	DecodeJSON(t, resp, &intake)
	return ExtractIDFromResponse(t, intake)
}
