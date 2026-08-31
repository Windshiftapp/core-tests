package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"windshift/internal/services"
	"windshift/internal/testutils/factory"
)

// Regression test for docs/bughunt-2026-05-19-pass-2.md F7.
//
// GetOrganisationTickets used to intersect the result with the requesting
// user's workspace permissions and bail to []. That defeated the per-org
// ACL: a customer-organisation member or manager without any workspace
// permission saw zero tickets. After the fix the org ACL (CanView) is the
// gate; workspace permissions only decide whether workspace_name /
// workspace_key are blanked in the response.

func setupOrgTicketsHandler(t *testing.T, userID int) (*PortalCustomersHandler, func(orgID int) *httptest.ResponseRecorder) {
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
	// Workspace 10 holds the org's tickets. We assign a workspace role to a
	// *different* user, which flips the workspace from open-by-default into
	// gated mode (see CLAUDE.md / project_workspace_permissions_open_default).
	// The test user thereby loses workspace-level item.view, which is the
	// exact condition F7 must handle gracefully.
	if _, err := db.Exec(`INSERT INTO workspaces (id, name, key, active, is_personal) VALUES (10, 'Locked WS', 'LOCK', TRUE, FALSE)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	seedNegativeTestUser(t, db, 999)
	var ownerRoleID int
	if err := db.QueryRow(`SELECT id FROM workspace_roles ORDER BY id LIMIT 1`).Scan(&ownerRoleID); err != nil {
		t.Fatalf("locate workspace role: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO user_workspace_roles (user_id, workspace_id, role_id) VALUES (999, 10, ?)`, ownerRoleID); err != nil {
		t.Fatalf("flip workspace to gated: %v", err)
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

// F7 happy path: an org member with no workspace-level item.view permission
// must still see their org's tickets via the org endpoint. Workspace
// metadata is blanked because the caller cannot view the workspace itself.
func TestPortalCustomers_GetOrganisationTickets_NoWorkspacePermNoLongerBlocks(t *testing.T) {
	const (
		userID = 1
		orgID  = 100
	)
	_, call := setupOrgTicketsHandler(t, userID)

	// Org has no whitelist rows → CanView is true (open access for any user).
	// User 1 has zero workspace permissions because workspace 10 is gated.
	rr := call(orgID)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var got []orgTicketResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 org ticket for user without workspace perms, got %d: %s", len(got), rr.Body.String())
	}
	if got[0].WorkspaceID != 10 {
		t.Errorf("expected workspace_id 10, got %d", got[0].WorkspaceID)
	}
	if got[0].WorkspaceName != "" || got[0].WorkspaceKey != "" {
		t.Errorf("expected scrubbed workspace metadata for non-accessible workspace; got name=%q key=%q", got[0].WorkspaceName, got[0].WorkspaceKey)
	}
}
