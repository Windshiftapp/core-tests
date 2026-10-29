package repository

import (
	"testing"

	"windshift/internal/testutils"
)

func TestSearchLinkableItemsReturnsCanonicalKeyAndTypePresentation(t *testing.T) {
	db := newItemListTestDB(t, "linkable-item-presentation")

	var workspaceID int
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Windshift', 'WI', '', true, false)
		RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}

	var itemTypeID int
	if err := db.QueryRow(`
		INSERT INTO item_types (name, icon, color)
		VALUES ('Link Picker Bug', 'Bug', '#e5484d')
		RETURNING id
	`).Scan(&itemTypeID); err != nil {
		t.Fatalf("insert item type: %v", err)
	}

	if _, err := db.ExecWrite(`
		INSERT INTO items (
			workspace_id, workspace_item_number, item_type_id, title, description, frac_index
		) VALUES (?, 657, ?, 'Picker result', 'Canonical key fixture', ?)
	`, workspaceID, itemTypeID, testutils.NextTestFracIndex()); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	items, err := NewItemRepository(db).SearchLinkableItems(
		"Picker",
		[]int{workspaceID},
		nil,
		10,
	)
	if err != nil {
		t.Fatalf("search linkable items: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("result count = %d, want 1", len(items))
	}

	item := items[0]
	if item.WorkspaceKey != "WI" {
		t.Errorf("workspace key = %q, want WI", item.WorkspaceKey)
	}
	if item.WorkspaceItemNumber == nil || *item.WorkspaceItemNumber != 657 {
		t.Errorf("workspace item number = %v, want 657", item.WorkspaceItemNumber)
	}
	if item.ItemTypeIcon != "Bug" {
		t.Errorf("item type icon = %q, want Bug", item.ItemTypeIcon)
	}
	if item.ItemTypeColor != "#e5484d" {
		t.Errorf("item type color = %q, want #e5484d", item.ItemTypeColor)
	}
}

// TestSearchLinkableItemsReturnsRequesterID verifies the merge dialog can tell
// whose ticket a duplicate is before folding it in: customer-facing items carry
// their requester, internal items stay nil.
func TestSearchLinkableItemsReturnsRequesterID(t *testing.T) {
	db := newItemListTestDB(t, "linkable-requester")

	var workspaceID int
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Windshift', 'WI', '', true, false)
		RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	var customerID int
	if err := db.QueryRow(`
		INSERT INTO portal_customers (name, email) VALUES ('Pat', 'pat@example.test') RETURNING id
	`).Scan(&customerID); err != nil {
		t.Fatalf("insert portal customer: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index, creator_portal_customer_id)
		VALUES (?, 1, 'Customer request', 'request', ?, ?)
	`, workspaceID, testutils.NextTestFracIndex(), customerID); err != nil {
		t.Fatalf("insert customer item: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index)
		VALUES (?, 2, 'Internal request', 'request', ?)
	`, workspaceID, testutils.NextTestFracIndex()); err != nil {
		t.Fatalf("insert internal item: %v", err)
	}

	items, err := NewItemRepository(db).SearchLinkableItems("request", []int{workspaceID}, nil, 10)
	if err != nil {
		t.Fatalf("search linkable items: %v", err)
	}
	requesters := map[string]*int{}
	for _, item := range items {
		requesters[item.Title] = item.CreatorPortalCustomerID
	}
	if got := requesters["Customer request"]; got == nil || *got != customerID {
		t.Errorf("customer item requester = %v, want %d", got, customerID)
	}
	if got := requesters["Internal request"]; got != nil {
		t.Errorf("internal item requester = %d, want nil", *got)
	}
}
