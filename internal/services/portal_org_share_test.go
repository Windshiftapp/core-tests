//go:build test

package services

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

// TestPortalOrganisationSharedRequestVisibility pins the WI-1139 organisation
// sharing rules at the service layer: disabled hides everything, automatic
// shares every same-organisation ticket, requester_choice shares only flagged
// tickets, the roles audience gates on contact level, and cross-organisation
// access is always denied. The listing and the detail gate must agree.
func TestPortalOrganisationSharedRequestVisibility(t *testing.T) {
	tdb := testutils.CreateTestDB(t, false)
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	ctx := context.Background()

	var portalChannelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Portal', 'portal', 'inbound', 'enabled', ?)
		RETURNING id
	`, fmt.Sprintf(`{"portal_workspace_ids":[%d]}`, data.WorkspaceID)).Scan(&portalChannelID); err != nil {
		t.Fatalf("insert portal channel: %v", err)
	}

	orgA := insertPortalOrg(t, db, "Org A")
	orgB := insertPortalOrg(t, db, "Org B")

	creator := insertPortalCustomer(t, db, "Creator", "creator@example.com", &orgA)
	colleague := insertPortalCustomer(t, db, "Colleague", "colleague@example.com", &orgA)
	outsider := insertPortalCustomer(t, db, "Outsider", "outsider@example.com", &orgB)
	noOrg := insertPortalCustomer(t, db, "NoOrg", "noorg@example.com", nil)

	sharedTicket := insertPortalOrgTicket(t, db, portalChannelID, data.WorkspaceID, 201, creator, "shared", true)
	privateTicket := insertPortalOrgTicket(t, db, portalChannelID, data.WorkspaceID, 202, creator, "private", false)
	orgBTicket := insertPortalOrgTicket(t, db, portalChannelID, data.WorkspaceID, 203, outsider, "org b", true)
	noOrgTicket := insertPortalOrgTicket(t, db, portalChannelID, data.WorkspaceID, 204, noOrg, "no org", true)

	service := NewPortalService(db)

	visibleIDs := func(t *testing.T, customerID int) map[int]bool {
		t.Helper()
		requests, err := service.GetRequestsByPortalCustomerID(ctx, customerID, portalChannelID)
		if err != nil {
			t.Fatalf("GetRequestsByPortalCustomerID: %v", err)
		}
		got := map[int]bool{}
		for _, r := range requests {
			got[r.ID] = true
		}
		return got
	}

	t.Run("disabled hides organisation tickets", func(t *testing.T) {
		setOrgSharing(t, db, orgA, models.OrgRequestSharingDisabled, models.OrgRequestSharingAudienceOrganisation, nil)
		got := visibleIDs(t, colleague)
		if got[sharedTicket] || got[privateTicket] {
			t.Fatalf("disabled mode must hide org tickets; got %v", got)
		}
	})

	t.Run("automatic shares every same-organisation ticket", func(t *testing.T) {
		setOrgSharing(t, db, orgA, models.OrgRequestSharingAutomatic, models.OrgRequestSharingAudienceOrganisation, nil)
		got := visibleIDs(t, colleague)
		if !got[sharedTicket] || !got[privateTicket] {
			t.Fatalf("automatic mode must show both org tickets; got %v", got)
		}
		if got[orgBTicket] || got[noOrgTicket] {
			t.Fatalf("automatic mode must not leak cross-org or no-org tickets; got %v", got)
		}
	})

	t.Run("requester_choice shares only flagged tickets", func(t *testing.T) {
		setOrgSharing(t, db, orgA, models.OrgRequestSharingRequesterChoice, models.OrgRequestSharingAudienceOrganisation, nil)
		got := visibleIDs(t, colleague)
		if !got[sharedTicket] {
			t.Fatalf("requester_choice must show the flagged ticket; got %v", got)
		}
		if got[privateTicket] {
			t.Fatalf("requester_choice must hide the unflagged ticket; got %v", got)
		}
	})

	t.Run("roles audience gates on contact level", func(t *testing.T) {
		allowedRole := insertContactRole(t, db, "Manager")
		otherRole := insertContactRole(t, db, "Viewer")
		assignRole(t, db, colleague, allowedRole)
		setOrgSharing(t, db, orgA, models.OrgRequestSharingAutomatic, models.OrgRequestSharingAudienceRoles, []int{allowedRole})
		if got := visibleIDs(t, colleague); !got[sharedTicket] {
			t.Fatalf("colleague with the allowed role must see the ticket; got %v", got)
		}

		viewer := insertPortalCustomer(t, db, "Viewer", "viewer@example.com", &orgA)
		assignRole(t, db, viewer, otherRole)
		gotViewer := visibleIDs(t, viewer)
		if gotViewer[sharedTicket] || gotViewer[privateTicket] {
			t.Fatalf("contact without the allowed role must not see org tickets; got %v", gotViewer)
		}
	})

	t.Run("roles audience with empty allowlist exposes nothing", func(t *testing.T) {
		setOrgSharing(t, db, orgA, models.OrgRequestSharingAutomatic, models.OrgRequestSharingAudienceRoles, nil)
		got := visibleIDs(t, colleague)
		if got[sharedTicket] || got[privateTicket] {
			t.Fatalf("empty role allowlist must expose nothing; got %v", got)
		}
	})

	t.Run("creator always sees own tickets", func(t *testing.T) {
		setOrgSharing(t, db, orgA, models.OrgRequestSharingDisabled, models.OrgRequestSharingAudienceOrganisation, nil)
		got := visibleIDs(t, creator)
		if !got[sharedTicket] || !got[privateTicket] {
			t.Fatalf("creator must always see own tickets; got %v", got)
		}
	})

	t.Run("detail gate mirrors the list", func(t *testing.T) {
		setOrgSharing(t, db, orgA, models.OrgRequestSharingRequesterChoice, models.OrgRequestSharingAudienceOrganisation, nil)
		if ok, err := service.VerifyRequestOwnership(ctx, sharedTicket, portalChannelID, nil, &colleague); err != nil || !ok {
			t.Fatalf("shared ticket ownership = %v, %v; want true, nil", ok, err)
		}
		if ok, err := service.VerifyRequestOwnership(ctx, privateTicket, portalChannelID, nil, &colleague); err != nil || ok {
			t.Fatalf("unshared ticket ownership = %v, %v; want false, nil", ok, err)
		}

		setOrgSharing(t, db, orgA, models.OrgRequestSharingAutomatic, models.OrgRequestSharingAudienceOrganisation, nil)
		if ok, err := service.VerifyRequestOwnership(ctx, sharedTicket, portalChannelID, nil, &outsider); err != nil || ok {
			t.Fatalf("cross-org ownership = %v, %v; want false, nil", ok, err)
		}
	})
}

func insertPortalOrg(t *testing.T, db database.Database, name string) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`INSERT INTO customer_organisations (name) VALUES (?) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert customer organisation %s: %v", name, err)
	}
	return id
}

func insertPortalCustomer(t *testing.T, db database.Database, name, email string, orgID *int) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`
		INSERT INTO portal_customers (name, email, customer_organisation_id) VALUES (?, ?, ?) RETURNING id
	`, name, email, orgID).Scan(&id); err != nil {
		t.Fatalf("insert portal customer %s: %v", name, err)
	}
	return id
}

func insertPortalOrgTicket(t *testing.T, db database.Database, channelID, workspaceID, number, creatorCustomerID int, title string, shared bool) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, channel_id, creator_portal_customer_id, portal_org_shared, frac_index)
		VALUES (?, ?, ?, 'd', ?, ?, ?, ?) RETURNING id
	`, workspaceID, number, title, channelID, creatorCustomerID, shared, testutils.NextTestFracIndex()).Scan(&id); err != nil {
		t.Fatalf("insert ticket %q: %v", title, err)
	}
	return id
}

func insertContactRole(t *testing.T, db database.Database, name string) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`INSERT INTO contact_roles (name) VALUES (?) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert contact role %s: %v", name, err)
	}
	return id
}

func assignRole(t *testing.T, db database.Database, customerID, roleID int) {
	t.Helper()
	if _, err := db.ExecWrite(`INSERT INTO portal_customer_roles (portal_customer_id, contact_role_id) VALUES (?, ?)`, customerID, roleID); err != nil {
		t.Fatalf("assign role %d to customer %d: %v", roleID, customerID, err)
	}
}

func setOrgSharing(t *testing.T, db database.Database, orgID int, mode, audience string, roleIDs []int) {
	t.Helper()
	settings := map[string]any{
		"request_sharing":          mode,
		"request_sharing_audience": audience,
	}
	if len(roleIDs) > 0 {
		settings["request_visible_role_ids"] = roleIDs
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal org settings: %v", err)
	}
	if _, err := db.ExecWrite(`UPDATE customer_organisations SET settings = ? WHERE id = ?`, string(encoded), orgID); err != nil {
		t.Fatalf("set org sharing for %d: %v", orgID, err)
	}
}
