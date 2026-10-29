//go:build test

package repository

import (
	"testing"
	"time"

	"windshift/internal/testutils"
)

// TestFindByIDWithDetailsExposesRequesterOrganisation verifies the item
// detail projection joins the portal-customer creator and its organisation,
// which backs the customer-context panel (WI-1139).
func TestFindByIDWithDetailsExposesRequesterOrganisation(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	repo := NewItemRepository(tdb.GetDatabase())
	now := time.Now()

	var orgID int64
	if err := tdb.DB.QueryRow(`
		INSERT INTO customer_organisations (name, email, description, active, created_at, updated_at)
		VALUES ('Acme Inc', 'hello@acme.example', '', true, ?, ?)
		RETURNING id
	`, now, now).Scan(&orgID); err != nil {
		t.Fatalf("create customer organisation: %v", err)
	}

	var customerID int64
	if err := tdb.DB.QueryRow(`
		INSERT INTO portal_customers (name, email, customer_organisation_id, created_at, updated_at)
		VALUES ('Jane Doe', 'jane@acme.example', ?, ?, ?)
		RETURNING id
	`, orgID, now, now).Scan(&customerID); err != nil {
		t.Fatalf("create portal customer: %v", err)
	}

	var workspaceID int64
	if err := tdb.DB.QueryRow(`
		INSERT INTO workspaces (name, key, description, created_at, updated_at)
		VALUES ('Requester Context', 'RCX', '', ?, ?)
		RETURNING id
	`, now, now).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	var itemID int64
	if err := tdb.DB.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, is_task, creator_portal_customer_id, frac_index, path, created_at, updated_at)
		VALUES (?, 1, 'Printer down', 'Cannot print', false, ?, 'rc0', '/1/', ?, ?)
		RETURNING id
	`, workspaceID, customerID, now, now).Scan(&itemID); err != nil {
		t.Fatalf("create item: %v", err)
	}

	item, err := repo.FindByIDWithDetails(int(itemID))
	if err != nil {
		t.Fatalf("FindByIDWithDetails: %v", err)
	}

	if item.CreatorPortalCustomerName != "Jane Doe" {
		t.Errorf("CreatorPortalCustomerName = %q, want Jane Doe", item.CreatorPortalCustomerName)
	}
	if item.CreatorPortalCustomerEmail != "jane@acme.example" {
		t.Errorf("CreatorPortalCustomerEmail = %q, want jane@acme.example", item.CreatorPortalCustomerEmail)
	}
	if item.CreatorCustomerOrganisationID == nil || *item.CreatorCustomerOrganisationID != int(orgID) {
		t.Errorf("CreatorCustomerOrganisationID = %v, want %d", item.CreatorCustomerOrganisationID, orgID)
	}
	if item.CreatorCustomerOrganisationName != "Acme Inc" {
		t.Errorf("CreatorCustomerOrganisationName = %q, want Acme Inc", item.CreatorCustomerOrganisationName)
	}
}

// TestFindByIDWithDetailsLeavesOrganisationEmptyForInternalCreator guards the
// other half of the contract: an internal-creator item exposes no requester
// organisation, so the customer-context panel stays off.
func TestFindByIDWithDetailsLeavesOrganisationEmptyForInternalCreator(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	repo := NewItemRepository(tdb.GetDatabase())
	now := time.Now()

	var workspaceID int64
	if err := tdb.DB.QueryRow(`
		INSERT INTO workspaces (name, key, description, created_at, updated_at)
		VALUES ('Internal Only', 'INT', '', ?, ?)
		RETURNING id
	`, now, now).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	var itemID int64
	if err := tdb.DB.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, is_task, frac_index, path, created_at, updated_at)
		VALUES (?, 1, 'Internal task', 'Internal only', false, 'in0', '/1/', ?, ?)
		RETURNING id
	`, workspaceID, now, now).Scan(&itemID); err != nil {
		t.Fatalf("create item: %v", err)
	}

	item, err := repo.FindByIDWithDetails(int(itemID))
	if err != nil {
		t.Fatalf("FindByIDWithDetails: %v", err)
	}

	if item.CreatorCustomerOrganisationID != nil {
		t.Errorf("CreatorCustomerOrganisationID = %v, want nil", item.CreatorCustomerOrganisationID)
	}
	if item.CreatorCustomerOrganisationName != "" {
		t.Errorf("CreatorCustomerOrganisationName = %q, want empty", item.CreatorCustomerOrganisationName)
	}
}
