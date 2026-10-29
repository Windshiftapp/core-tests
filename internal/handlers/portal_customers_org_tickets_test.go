package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"windshift/internal/services"
	"windshift/internal/testutils/factory"
)

// GetOrganisationTickets is gated by the per-org ACL and then limited to the
// requesting user's workspace visibility: an org member without item.view on a
// ticket's workspace does not see that ticket at all. This supersedes the
// earlier F7 behaviour, which kept the ticket and blanked only workspace_name /
// workspace_key, leaving the title, body, status and workspace_id exposed.

func setupOrgTicketsHandler(t *testing.T, userID int, grantCallerAccess bool) (*PortalCustomersHandler, func(orgID int) *httptest.ResponseRecorder) {
	t.Helper()
	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	permService := newNegativeTestPermissionService(t, db)
	timePermService := services.NewTimePermissionService(db, permService)
	orgPermService := services.NewCustomerOrganisationPermissionService(db, permService, timePermService)
	handler := NewPortalCustomersHandler(db, permService, orgPermService)

	call := func(orgID int) *httptest.ResponseRecorder {
		req := authedRequest(http.MethodGet, "/api/portal/customer-organisations/"+strconv.Itoa(orgID)+"/tickets", userID, nil)
		req.SetPathValue("id", strconv.Itoa(orgID))
		rr := httptest.NewRecorder()
		handler.GetOrganisationTickets(rr, req)
		return rr
	}

	if _, err := db.Exec(`INSERT INTO customer_organisations (id, name) VALUES (100, 'Acme Corp')`); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO portal_customers (id, name, email, customer_organisation_id) VALUES (500, 'PC One', 'pc1@example.com', 100)`); err != nil {
		t.Fatalf("seed portal_customer: %v", err)
	}
	// Workspace 10 holds the org's tickets. Assigning a workspace role flips
	// the workspace from open-by-default into gated mode (see CLAUDE.md /
	// project_workspace_permissions_open_default), so a user without an
	// explicit role there loses item.view.
	if _, err := db.Exec(`INSERT INTO workspaces (id, name, key, active, is_personal) VALUES (10, 'Locked WS', 'LOCK', TRUE, FALSE)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	seedNegativeTestUser(t, db, 999)
	var roleID int
	if err := db.QueryRow(`SELECT id FROM workspace_roles ORDER BY id LIMIT 1`).Scan(&roleID); err != nil {
		t.Fatalf("locate workspace role: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO user_workspace_roles (user_id, workspace_id, role_id) VALUES (999, 10, ?)`, roleID); err != nil {
		t.Fatalf("flip workspace to gated: %v", err)
	}
	if grantCallerAccess {
		if _, err := db.Exec(`INSERT INTO user_workspace_roles (user_id, workspace_id, role_id) VALUES (?, 10, ?)`, userID, roleID); err != nil {
			t.Fatalf("grant caller workspace access: %v", err)
		}
	}
	// Create the org ticket through the production path with the portal
	// customer pinned as creator, exactly like a portal submit would.
	portalCustomerID := 500
	f := factory.NewTestFactory(db)
	if _, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID:             10,
		Title:                   "Org ticket #1",
		CreatorPortalCustomerID: &portalCustomerID,
	}); err != nil {
		t.Fatalf("seed item: %v", err)
	}

	return handler, call
}

type orgTicketResponse struct {
	ID            int    `json:"id"`
	WorkspaceID   int    `json:"workspace_id"`
	Title         string `json:"title"`
	WorkspaceName string `json:"workspace_name"`
	WorkspaceKey  string `json:"workspace_key"`
}

// An org member without workspace-level item.view must not see tickets filed
// in workspaces they cannot view.
func TestPortalCustomers_GetOrganisationTickets_OmitsInaccessibleWorkspaces(t *testing.T) {
	const (
		userID = 1
		orgID  = 100
	)
	_, call := setupOrgTicketsHandler(t, userID, false)

	// Org has no whitelist rows → CanView is true. User 1 has zero workspace
	// permissions because workspace 10 is gated, so the ticket is omitted.
	rr := call(orgID)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if body := strings.TrimSpace(rr.Body.String()); body != "[]" {
		t.Fatalf("expected no tickets for a caller without workspace access, got %s", body)
	}
}

// An org member with item.view on the ticket's workspace still sees the ticket
// with its workspace metadata.
func TestPortalCustomers_GetOrganisationTickets_IncludesViewableWorkspaces(t *testing.T) {
	const (
		userID = 1
		orgID  = 100
	)
	_, call := setupOrgTicketsHandler(t, userID, true)

	rr := call(orgID)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var got []orgTicketResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 org ticket for a caller with workspace access, got %d: %s", len(got), rr.Body.String())
	}
	if got[0].WorkspaceID != 10 || got[0].WorkspaceName != "Locked WS" || got[0].WorkspaceKey != "LOCK" {
		t.Errorf("unexpected org ticket metadata: %#v", got[0])
	}
	if got[0].Title != "Org ticket #1" {
		t.Errorf("org ticket title = %q, want Org ticket #1", got[0].Title)
	}
}
