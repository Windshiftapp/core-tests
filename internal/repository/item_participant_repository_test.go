//go:build test

package repository

import (
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// participantFixture builds a workspace, an adding user, two items, and two
// portal customers so participant scope and isolation can be asserted.
type participantFixture struct {
	itemA      int
	itemB      int
	userID     int
	customerA  int
	customerB  int
}

func setupParticipantFixture(t *testing.T) (*ItemParticipantRepository, participantFixture) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	fixture := participantFixture{
		userID: testutils.InsertID(t, db,
			`INSERT INTO users (email, username, first_name, last_name) VALUES ('participant-adder@example.test', 'participant-adder', 'Pat', 'Adder')`),
	}
	workspaceID := testutils.InsertID(t, db,
		`INSERT INTO workspaces (name, key) VALUES ('Participant WS', 'PWS')`)
	fixture.itemA = testutils.InsertID(t, db,
		`INSERT INTO items (workspace_id, workspace_item_number, title, frac_index) VALUES (?, 1, 'Participant item A', ?)`,
		workspaceID, testutils.NextTestFracIndex())
	fixture.itemB = testutils.InsertID(t, db,
		`INSERT INTO items (workspace_id, workspace_item_number, title, frac_index) VALUES (?, 2, 'Participant item B', ?)`,
		workspaceID, testutils.NextTestFracIndex())
	fixture.customerA = testutils.InsertID(t, db,
		`INSERT INTO portal_customers (name, email, created_via) VALUES ('Alice External', 'alice@example.test', 'agent')`)
	fixture.customerB = testutils.InsertID(t, db,
		`INSERT INTO portal_customers (name, email, created_via) VALUES ('Bob External', 'bob@example.test', 'agent')`)

	return NewItemParticipantRepository(db), fixture
}

func TestItemParticipantAddIsIdempotentAndJoinsCustomer(t *testing.T) {
	repo, fixture := setupParticipantFixture(t)

	created, err := repo.Add(fixture.itemA, fixture.customerA, &fixture.userID)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !created {
		t.Fatalf("first Add created=false, want true")
	}

	created, err = repo.Add(fixture.itemA, fixture.customerA, &fixture.userID)
	if err != nil {
		t.Fatalf("Add duplicate: %v", err)
	}
	if created {
		t.Fatalf("duplicate Add created=true, want false")
	}

	participants, err := repo.ListByItem(fixture.itemA)
	if err != nil {
		t.Fatalf("ListByItem: %v", err)
	}
	if len(participants) != 1 {
		t.Fatalf("participants = %+v, want exactly one", participants)
	}
	got := participants[0]
	if got.ItemID != fixture.itemA || got.PortalCustomerID != fixture.customerA {
		t.Fatalf("participant identity = %+v", got)
	}
	if got.CustomerName != "Alice External" || got.CustomerEmail != "alice@example.test" {
		t.Fatalf("joined customer = %q / %q", got.CustomerName, got.CustomerEmail)
	}
	if got.AddedBy == nil || *got.AddedBy != fixture.userID {
		t.Fatalf("added_by = %v, want %d", got.AddedBy, fixture.userID)
	}
	if got.AddedByName != "Pat Adder" {
		t.Fatalf("added_by_name = %q, want %q", got.AddedByName, "Pat Adder")
	}
}

func TestItemParticipantScopeIsolation(t *testing.T) {
	repo, fixture := setupParticipantFixture(t)

	if _, err := repo.Add(fixture.itemA, fixture.customerA, &fixture.userID); err != nil {
		t.Fatalf("Add item A: %v", err)
	}
	if _, err := repo.Add(fixture.itemB, fixture.customerB, &fixture.userID); err != nil {
		t.Fatalf("Add item B: %v", err)
	}

	participantsA, err := repo.ListByItem(fixture.itemA)
	if err != nil {
		t.Fatalf("ListByItem A: %v", err)
	}
	if len(participantsA) != 1 || participantsA[0].PortalCustomerID != fixture.customerA {
		t.Fatalf("item A participants = %+v, want only customer %d", participantsA, fixture.customerA)
	}

	itemIDs, err := repo.ListItemIDsForCustomer(fixture.customerA)
	if err != nil {
		t.Fatalf("ListItemIDsForCustomer: %v", err)
	}
	if len(itemIDs) != 1 || itemIDs[0] != fixture.itemA {
		t.Fatalf("customer A items = %v, want [%d]", itemIDs, fixture.itemA)
	}

	customerIDs, err := repo.CustomerIDsForItem(fixture.itemA)
	if err != nil {
		t.Fatalf("CustomerIDsForItem: %v", err)
	}
	if len(customerIDs) != 1 || customerIDs[0] != fixture.customerA {
		t.Fatalf("item A customer ids = %v, want [%d]", customerIDs, fixture.customerA)
	}
}

func TestItemParticipantMembershipChecks(t *testing.T) {
	repo, fixture := setupParticipantFixture(t)

	if _, err := repo.Add(fixture.itemA, fixture.customerA, &fixture.userID); err != nil {
		t.Fatalf("Add: %v", err)
	}

	isParticipant, err := repo.IsParticipant(fixture.itemA, fixture.customerA)
	if err != nil {
		t.Fatalf("IsParticipant: %v", err)
	}
	if !isParticipant {
		t.Fatalf("IsParticipant(customer A) = false, want true")
	}

	isParticipant, err = repo.IsParticipant(fixture.itemA, fixture.customerB)
	if err != nil {
		t.Fatalf("IsParticipant other: %v", err)
	}
	if isParticipant {
		t.Fatalf("IsParticipant(customer B) = true, want false")
	}

	byEmail, err := repo.IsParticipantEmail(fixture.itemA, "  ALICE@example.test ")
	if err != nil {
		t.Fatalf("IsParticipantEmail: %v", err)
	}
	if !byEmail {
		t.Fatalf("IsParticipantEmail(alice) = false, want true (case/trim insensitive)")
	}

	byEmail, err = repo.IsParticipantEmail(fixture.itemA, "bob@example.test")
	if err != nil {
		t.Fatalf("IsParticipantEmail other: %v", err)
	}
	if byEmail {
		t.Fatalf("IsParticipantEmail(bob) = true, want false")
	}

	byEmail, err = repo.IsParticipantEmail(fixture.itemA, "")
	if err != nil {
		t.Fatalf("IsParticipantEmail empty: %v", err)
	}
	if byEmail {
		t.Fatalf("IsParticipantEmail(empty) = true, want false")
	}

	emails, err := repo.EmailsForItem(fixture.itemA)
	if err != nil {
		t.Fatalf("EmailsForItem: %v", err)
	}
	if len(emails) != 1 || emails[0] != "alice@example.test" {
		t.Fatalf("participant emails = %v, want [alice@example.test]", emails)
	}
}

func TestItemParticipantRemoveReportsRow(t *testing.T) {
	repo, fixture := setupParticipantFixture(t)

	if _, err := repo.Add(fixture.itemA, fixture.customerA, &fixture.userID); err != nil {
		t.Fatalf("Add: %v", err)
	}

	removed, err := repo.Remove(fixture.itemA, fixture.customerA)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !removed {
		t.Fatalf("Remove existing = false, want true")
	}

	removed, err = repo.Remove(fixture.itemA, fixture.customerA)
	if err != nil {
		t.Fatalf("Remove missing: %v", err)
	}
	if removed {
		t.Fatalf("Remove missing = true, want false")
	}

	participants, err := repo.ListByItem(fixture.itemA)
	if err != nil {
		t.Fatalf("ListByItem after remove: %v", err)
	}
	if len(participants) != 0 {
		t.Fatalf("participants after remove = %+v, want empty", participants)
	}
}

func TestItemParticipantDeleteForItemRemovesAll(t *testing.T) {
	repo, fixture := setupParticipantFixture(t)
	db := repo.db

	if _, err := repo.Add(fixture.itemA, fixture.customerA, &fixture.userID); err != nil {
		t.Fatalf("Add customer A: %v", err)
	}
	if _, err := repo.Add(fixture.itemA, fixture.customerB, &fixture.userID); err != nil {
		t.Fatalf("Add customer B: %v", err)
	}
	if _, err := repo.Add(fixture.itemB, fixture.customerA, &fixture.userID); err != nil {
		t.Fatalf("Add item B: %v", err)
	}

	if err := database.WithTx(db, func(tx database.Tx) error {
		return repo.DeleteForItem(tx, fixture.itemA)
	}); err != nil {
		t.Fatalf("DeleteForItem: %v", err)
	}

	participantsA, err := repo.ListByItem(fixture.itemA)
	if err != nil {
		t.Fatalf("ListByItem A: %v", err)
	}
	if len(participantsA) != 0 {
		t.Fatalf("item A participants after delete = %+v, want empty", participantsA)
	}

	participantsB, err := repo.ListByItem(fixture.itemB)
	if err != nil {
		t.Fatalf("ListByItem B: %v", err)
	}
	if len(participantsB) != 1 {
		t.Fatalf("item B participants after delete = %+v, want one", participantsB)
	}
}
