package tests

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/testutils"
)

// TestRequesterOpenTicketsEndpoint verifies the WI-1548 duplicate-candidates
// endpoint: it returns the requester's other open tickets (cross-workspace for
// admins), excludes the item itself and closed tickets, filters candidates to
// workspaces the caller can view, and 404s callers without item view.
func TestRequesterOpenTicketsEndpoint(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsA, _ := CreateTestWorkspace(t, server, "Candidates A", shortKey("CPA"))
	wsB, _ := CreateTestWorkspace(t, server, "Candidates B", shortKey("CPB"))

	// Intake-shaped requester with tickets across two workspaces.
	var customerID int
	if err := server.DB().QueryRow(`
		INSERT INTO portal_customers (name, email, created_via)
		VALUES ('Dup Customer', 'dup-customer@example.test', 'email-intake')
		RETURNING id
	`).Scan(&customerID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}

	insertTicket := func(workspaceID, number int, title string) int {
		t.Helper()
		var id int
		if err := server.DB().QueryRow(`
			INSERT INTO items (workspace_id, workspace_item_number, title, description, creator_portal_customer_id, frac_index)
			VALUES (?, ?, ?, 'sent by email', ?, ?) RETURNING id
		`, workspaceID, number, title, customerID, testutils.NextTestFracIndex()).Scan(&id); err != nil {
			t.Fatalf("insert ticket %q: %v", title, err)
		}
		return id
	}
	ticketA1 := insertTicket(wsA, 101, "Open ticket A1")
	ticketA2 := insertTicket(wsA, 102, "Open ticket A2")
	ticketB1 := insertTicket(wsB, 201, "Open ticket B1")
	closedTicket := insertTicket(wsA, 103, "Closed ticket")

	var completedStatusID int
	if err := server.DB().QueryRow(`
		SELECT s.id FROM statuses s
		JOIN status_categories sc ON s.category_id = sc.id
		WHERE sc.is_completed = true LIMIT 1
	`).Scan(&completedStatusID); err != nil {
		t.Fatalf("find completed status: %v", err)
	}
	if _, err := server.DB().Exec(`UPDATE items SET status_id = ? WHERE id = ?`, completedStatusID, closedTicket); err != nil {
		t.Fatalf("close ticket: %v", err)
	}

	// Admin sees the open candidates across both workspaces, excluding the
	// item itself and the closed ticket.
	adminResp := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/items/%d/requester-open-tickets", ticketA1), nil)
	defer adminResp.Body.Close()
	AssertStatusCode(t, adminResp, http.StatusOK)
	var candidates []map[string]interface{}
	DecodeJSON(t, adminResp, &candidates)
	got := map[int]bool{}
	for _, c := range candidates {
		got[int(c["id"].(float64))] = true
	}
	if !got[ticketA2] || !got[ticketB1] {
		t.Fatalf("candidates must include %d and %d; got %v", ticketA2, ticketB1, got)
	}
	if got[ticketA1] || got[closedTicket] {
		t.Fatalf("candidates must exclude the item itself and closed tickets; got %v", got)
	}
	// The merge dialog compares requesters before folding tickets, so every
	// candidate carries the shared requester id.
	for _, c := range candidates {
		requester, ok := c["creator_portal_customer_id"].(float64)
		if !ok || int(requester) != customerID {
			t.Fatalf("candidate %v requester = %v, want %d", c["id"], c["creator_portal_customer_id"], customerID)
		}
	}

	// Workspaces are open to all internal users by default (everyone-granted
	// item.view). Lock wsB down by assigning its Viewer role to another user,
	// which switches the workspace to explicit-only access; the test viewer
	// then has no view there.
	lockdownUserID, _, _ := CreateTestUserWithCredentials(
		t, server, "cand_lockdown", "cand_lockdown@test.com")
	AssignWorkspaceRole(t, server, lockdownUserID, wsB, "Viewer")

	// A viewer of workspace A only sees the wsA candidate; the wsB item and
	// its candidates endpoint are 404 (no view).
	viewerID, viewerUsername, viewerPassword := CreateTestUserWithCredentials(
		t, server, "cand_viewer", "cand_viewer@test.com")
	AssignWorkspaceRole(t, server, viewerID, wsA, "Viewer")
	viewerSession, _ := CreateAuthCredentialsForUser(t, server, viewerUsername, viewerPassword)

	viewerResp := MakeAuthRequestWithToken(t, server, viewerSession, http.MethodGet,
		fmt.Sprintf("/items/%d/requester-open-tickets", ticketA1), nil)
	defer viewerResp.Body.Close()
	AssertStatusCode(t, viewerResp, http.StatusOK)
	var viewerCandidates []map[string]interface{}
	DecodeJSON(t, viewerResp, &viewerCandidates)
	if len(viewerCandidates) != 1 || int(viewerCandidates[0]["id"].(float64)) != ticketA2 {
		t.Fatalf("viewer candidates = %v, want only %d (wsB candidate filtered by permission)",
			viewerCandidates, ticketA2)
	}

	deniedResp := MakeAuthRequestWithToken(t, server, viewerSession, http.MethodGet,
		fmt.Sprintf("/items/%d/requester-open-tickets", ticketB1), nil)
	defer deniedResp.Body.Close()
	if deniedResp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET candidates without item view = %d, want 404", deniedResp.StatusCode)
	}

	// An item without a requester (internal creator) returns an empty list.
	var noRequesterItem int
	if err := server.DB().QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, creator_id, frac_index)
		VALUES (?, 105, 'Internal item', 'd', 1, ?) RETURNING id
	`, wsA, testutils.NextTestFracIndex()).Scan(&noRequesterItem); err != nil {
		t.Fatalf("insert internal item: %v", err)
	}
	emptyResp := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/items/%d/requester-open-tickets", noRequesterItem), nil)
	defer emptyResp.Body.Close()
	AssertStatusCode(t, emptyResp, http.StatusOK)
	var empty []map[string]interface{}
	DecodeJSON(t, emptyResp, &empty)
	if len(empty) != 0 {
		t.Fatalf("internal-creator item must have no candidates, got %v", empty)
	}
}
