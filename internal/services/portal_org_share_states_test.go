//go:build test

package services

import (
	"context"
	"fmt"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// TestPortalOrgShareReassignedContact verifies that organisation-shared
// visibility follows the requester when a contact is moved to another
// organisation: the old organisation loses access and the new one gains it.
func TestPortalOrgShareReassignedContact(t *testing.T) {
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
	colleagueA := insertPortalCustomer(t, db, "Colleague A", "colleague-a@example.com", &orgA)
	colleagueB := insertPortalCustomer(t, db, "Colleague B", "colleague-b@example.com", &orgB)

	ticket := insertPortalOrgTicket(t, db, portalChannelID, data.WorkspaceID, 301, creator, "shared", true)
	setOrgSharing(t, db, orgA, models.OrgRequestSharingAutomatic, models.OrgRequestSharingAudienceOrganisation, nil)
	setOrgSharing(t, db, orgB, models.OrgRequestSharingAutomatic, models.OrgRequestSharingAudienceOrganisation, nil)

	service := NewPortalService(db)
	sees := func(customerID int) bool {
		t.Helper()
		requests, err := service.GetRequestsByPortalCustomerID(ctx, customerID, portalChannelID)
		if err != nil {
			t.Fatalf("GetRequestsByPortalCustomerID(%d): %v", customerID, err)
		}
		for _, r := range requests {
			if r.ID == ticket {
				return true
			}
		}
		return false
	}

	if !sees(colleagueA) {
		t.Fatalf("org A colleague must see the shared ticket before reassignment")
	}
	if sees(colleagueB) {
		t.Fatalf("org B colleague must not see the org A ticket")
	}

	if _, err := db.ExecWrite(`UPDATE portal_customers SET customer_organisation_id = ? WHERE id = ?`, orgB, creator); err != nil {
		t.Fatalf("reassign creator to org B: %v", err)
	}

	if sees(colleagueA) {
		t.Fatalf("after reassignment the org A colleague must not see the ticket")
	}
	if !sees(colleagueB) {
		t.Fatalf("after reassignment the org B colleague must see the ticket")
	}
}

// TestPortalOrgShareDeletedRequest verifies that a deleted request disappears
// from the list and is not readable by direct id.
func TestPortalOrgShareDeletedRequest(t *testing.T) {
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

	org := insertPortalOrg(t, db, "Org")
	creator := insertPortalCustomer(t, db, "Creator", "creator@example.com", &org)
	colleague := insertPortalCustomer(t, db, "Colleague", "colleague@example.com", &org)
	ticket := insertPortalOrgTicket(t, db, portalChannelID, data.WorkspaceID, 302, creator, "shared", true)
	setOrgSharing(t, db, org, models.OrgRequestSharingAutomatic, models.OrgRequestSharingAudienceOrganisation, nil)

	service := NewPortalService(db)
	sees := func() bool {
		t.Helper()
		requests, err := service.GetRequestsByPortalCustomerID(ctx, colleague, portalChannelID)
		if err != nil {
			t.Fatalf("GetRequestsByPortalCustomerID: %v", err)
		}
		for _, r := range requests {
			if r.ID == ticket {
				return true
			}
		}
		return false
	}

	if !sees() {
		t.Fatalf("shared ticket must be visible before deletion")
	}

	if _, err := db.ExecWrite(`DELETE FROM items WHERE id = ?`, ticket); err != nil {
		t.Fatalf("delete ticket: %v", err)
	}

	if sees() {
		t.Fatalf("deleted ticket must not appear in the portal list")
	}
	ok, err := service.VerifyRequestOwnership(ctx, ticket, portalChannelID, nil, &colleague)
	if err != nil {
		t.Fatalf("VerifyRequestOwnership: %v", err)
	}
	if ok {
		t.Fatalf("deleted ticket must not be readable by direct id")
	}
}

// TestPortalOrgShareMergedDuplicateStates pins the WI-1528 portal contract: a
// same-requester merge hides the duplicate (its thread moved to the canonical),
// while a cross-requester merge keeps the source visible because its files and
// original request stay with its own requester.
func TestPortalOrgShareMergedDuplicateStates(t *testing.T) {
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

	creator := insertPortalCustomer(t, db, "Creator", "creator@example.com", nil)
	other := insertPortalCustomer(t, db, "Other", "other@example.com", nil)

	sameCanonical := insertPortalOrgTicket(t, db, portalChannelID, data.WorkspaceID, 401, creator, "same canonical", false)
	sameDuplicate := insertPortalOrgTicket(t, db, portalChannelID, data.WorkspaceID, 402, creator, "same duplicate", false)
	crossCanonical := insertPortalOrgTicket(t, db, portalChannelID, data.WorkspaceID, 403, creator, "cross canonical", false)
	crossDuplicate := insertPortalOrgTicket(t, db, portalChannelID, data.WorkspaceID, 404, other, "cross duplicate", false)

	merge := func(duplicateID, canonicalID int) {
		t.Helper()
		if _, err := db.ExecWrite(`UPDATE items SET merged_into_item_id = ? WHERE id = ?`, canonicalID, duplicateID); err != nil {
			t.Fatalf("merge %d into %d: %v", duplicateID, canonicalID, err)
		}
	}
	merge(sameDuplicate, sameCanonical)
	merge(crossDuplicate, crossCanonical)

	service := NewPortalService(db)
	sees := func(customerID, itemID int) bool {
		t.Helper()
		requests, err := service.GetRequestsByPortalCustomerID(ctx, customerID, portalChannelID)
		if err != nil {
			t.Fatalf("GetRequestsByPortalCustomerID(%d): %v", customerID, err)
		}
		for _, r := range requests {
			if r.ID == itemID {
				return true
			}
		}
		return false
	}

	if !sees(creator, sameCanonical) {
		t.Fatalf("same-requester canonical must stay visible")
	}
	if sees(creator, sameDuplicate) {
		t.Fatalf("same-requester merged duplicate must be hidden as a redirect")
	}
	if !sees(other, crossDuplicate) {
		t.Fatalf("cross-requester merged source must stay visible to its own requester")
	}
}

// TestPortalOrgShareArchivedWorkspace verifies that deactivating (archiving)
// the served workspace does not make the customer's own history vanish or
// error: the request still renders in the portal list.
func TestPortalOrgShareArchivedWorkspace(t *testing.T) {
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

	creator := insertPortalCustomer(t, db, "Creator", "creator@example.com", nil)
	ticket := insertPortalOrgTicket(t, db, portalChannelID, data.WorkspaceID, 501, creator, "archived workspace ticket", false)

	if _, err := db.ExecWrite(`UPDATE workspaces SET active = false WHERE id = ?`, data.WorkspaceID); err != nil {
		t.Fatalf("archive workspace: %v", err)
	}

	service := NewPortalService(db)
	requests, err := service.GetRequestsByPortalCustomerID(ctx, creator, portalChannelID)
	if err != nil {
		t.Fatalf("GetRequestsByPortalCustomerID: %v", err)
	}
	for _, r := range requests {
		if r.ID == ticket {
			return
		}
	}
	t.Fatalf("the customer's own request must remain visible after the workspace is archived")
}
