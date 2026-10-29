//go:build test

package repository

import (
	"testing"

	"windshift/internal/testutils"
)

// TestListOpenCandidatesInWorkspaceReturnsRequesterID verifies the AI
// find-similar candidate set carries the requester, so the merge dialog can
// warn when a suggested duplicate belongs to a different customer.
func TestListOpenCandidatesInWorkspaceReturnsRequesterID(t *testing.T) {
	db := newItemListTestDB(t, "open-candidates")

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

	insertItem := func(number int, title string, requester *int) int {
		t.Helper()
		var id int
		if err := db.QueryRow(`
			INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index, creator_portal_customer_id)
			VALUES (?, ?, ?, 'candidate', ?, ?) RETURNING id
		`, workspaceID, number, title, testutils.NextTestFracIndex(), requester).Scan(&id); err != nil {
			t.Fatalf("insert item %q: %v", title, err)
		}
		return id
	}
	current := insertItem(1, "Current", nil)
	insertItem(2, "Customer request", &customerID)
	insertItem(3, "Internal request", nil)

	candidates, err := NewItemRepository(db).ListOpenCandidatesInWorkspace(workspaceID, current, 100)
	if err != nil {
		t.Fatalf("list open candidates: %v", err)
	}
	byTitle := map[string]CandidateItem{}
	for _, candidate := range candidates {
		byTitle[candidate.Title] = candidate
	}
	if _, ok := byTitle["Current"]; ok {
		t.Fatal("excluded item appeared in candidates")
	}
	customer, ok := byTitle["Customer request"]
	if !ok {
		t.Fatalf("customer candidate missing; got %v", byTitle)
	}
	if customer.CreatorPortalCustomerID == nil || *customer.CreatorPortalCustomerID != customerID {
		t.Errorf("customer candidate requester = %v, want %d", customer.CreatorPortalCustomerID, customerID)
	}
	internal := byTitle["Internal request"]
	if internal.CreatorPortalCustomerID != nil {
		t.Errorf("internal candidate requester = %d, want nil", *internal.CreatorPortalCustomerID)
	}
}
