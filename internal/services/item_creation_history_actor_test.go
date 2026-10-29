//go:build test

package services_test

import (
	"database/sql"
	"strings"
	"testing"

	"windshift/internal/services"
	"windshift/internal/testutils"
)

// A portal submission carries no internal creator, only a portal customer.
// Its creation history must still be written and attributed to that customer;
// otherwise the item's History tab is empty even though the item was created.
func TestCreateItem_RecordsPortalCustomerCreationHistory(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	var customerID int
	if err := tdb.QueryRow(`
		INSERT INTO portal_customers (name, email) VALUES ('Portal Requester', ?) RETURNING id
	`, "portal-"+strings.ReplaceAll(t.Name(), "/", "_")+"@example.com").Scan(&customerID); err != nil {
		t.Fatalf("insert portal customer: %v", err)
	}

	itemID, err := services.CreateItem(tdb.GetDatabase(), services.ItemCreationParams{
		WorkspaceID:             data.WorkspaceID,
		Title:                   "Portal request",
		Description:             "Submitted through the portal",
		StatusID:                &data.StatusID,
		PriorityID:              &data.PriorityID,
		CreatorPortalCustomerID: &customerID,
	})
	if err != nil {
		t.Fatalf("CreateItem() error = %v", err)
	}

	rows, err := tdb.Query(`
		SELECT field_name, new_value, actor_kind, user_id, actor_portal_customer_id
		FROM item_history
		WHERE item_id = ?
		ORDER BY id
	`, itemID)
	if err != nil {
		t.Fatalf("query creation history: %v", err)
	}
	defer rows.Close()

	creationRows := 0
	foundTitle := false
	for rows.Next() {
		var field, newValue, actorKind string
		var userID, actorCustomerID sql.NullInt64
		if err := rows.Scan(&field, &newValue, &actorKind, &userID, &actorCustomerID); err != nil {
			t.Fatalf("scan creation history: %v", err)
		}
		creationRows++
		if actorKind != "portal_customer" {
			t.Fatalf("history %s actor_kind = %q, want portal_customer", field, actorKind)
		}
		if userID.Valid {
			t.Fatalf("history %s user_id = %d, want NULL for a portal customer", field, userID.Int64)
		}
		if !actorCustomerID.Valid || int(actorCustomerID.Int64) != customerID {
			t.Fatalf("history %s actor_portal_customer_id = %v, want %d", field, actorCustomerID, customerID)
		}
		if field == "title" {
			foundTitle = newValue == "Portal request"
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate creation history: %v", err)
	}
	if creationRows == 0 || !foundTitle {
		t.Fatalf("creation history rows = %d, title recorded = %t", creationRows, foundTitle)
	}
}

// A creation with no actor still gets a system-attributed history feed, so no
// created item is silently missing from the History tab.
func TestCreateItem_RecordsSystemCreationHistory(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	itemID, err := services.CreateItem(tdb.GetDatabase(), services.ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "System item",
		StatusID:    &data.StatusID,
		PriorityID:  &data.PriorityID,
	})
	if err != nil {
		t.Fatalf("CreateItem() error = %v", err)
	}

	var actorKind string
	var userID, actorCustomerID sql.NullInt64
	if err := tdb.QueryRow(`
		SELECT actor_kind, user_id, actor_portal_customer_id
		FROM item_history
		WHERE item_id = ? AND field_name = 'title'
	`, itemID).Scan(&actorKind, &userID, &actorCustomerID); err != nil {
		t.Fatalf("query title history: %v", err)
	}
	if actorKind != "system" {
		t.Fatalf("actor_kind = %q, want system", actorKind)
	}
	if userID.Valid || actorCustomerID.Valid {
		t.Fatalf("system history references = user:%v customer:%v, want none", userID, actorCustomerID)
	}
}

// When an internal user creates on a customer's behalf (a ticket split), the
// acting user is the history actor, not the customer on the new ticket.
func TestCreateItem_ActingUserWinsOverPortalCustomer(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	var customerID int
	if err := tdb.QueryRow(`
		INSERT INTO portal_customers (name, email) VALUES ('Split Requester', ?) RETURNING id
	`, "split-"+strings.ReplaceAll(t.Name(), "/", "_")+"@example.com").Scan(&customerID); err != nil {
		t.Fatalf("insert portal customer: %v", err)
	}

	itemID, err := services.CreateItem(tdb.GetDatabase(), services.ItemCreationParams{
		WorkspaceID:             data.WorkspaceID,
		Title:                   "Split child",
		StatusID:                &data.StatusID,
		PriorityID:              &data.PriorityID,
		CreatorID:               &data.UserID,
		CreatorPortalCustomerID: &customerID,
	})
	if err != nil {
		t.Fatalf("CreateItem() error = %v", err)
	}

	var actorKind string
	var userID, actorCustomerID sql.NullInt64
	if err := tdb.QueryRow(`
		SELECT actor_kind, user_id, actor_portal_customer_id
		FROM item_history
		WHERE item_id = ? AND field_name = 'title'
	`, itemID).Scan(&actorKind, &userID, &actorCustomerID); err != nil {
		t.Fatalf("query title history: %v", err)
	}
	if actorKind != "user" {
		t.Fatalf("actor_kind = %q, want user", actorKind)
	}
	if !userID.Valid || int(userID.Int64) != data.UserID {
		t.Fatalf("user_id = %v, want %d", userID, data.UserID)
	}
	if actorCustomerID.Valid {
		t.Fatalf("actor_portal_customer_id = %d, want NULL", actorCustomerID.Int64)
	}
}

// The internal-user creation path must keep attributing history to the user
// after the portal-customer branch was added.
func TestCreateItem_RecordsUserCreationHistory(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	itemID, err := services.CreateItem(tdb.GetDatabase(), services.ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Internal item",
		StatusID:    &data.StatusID,
		PriorityID:  &data.PriorityID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("CreateItem() error = %v", err)
	}

	var actorKind string
	var userID, actorCustomerID sql.NullInt64
	if err := tdb.QueryRow(`
		SELECT actor_kind, user_id, actor_portal_customer_id
		FROM item_history
		WHERE item_id = ? AND field_name = 'title'
	`, itemID).Scan(&actorKind, &userID, &actorCustomerID); err != nil {
		t.Fatalf("query title history: %v", err)
	}
	if actorKind != "user" {
		t.Fatalf("actor_kind = %q, want user", actorKind)
	}
	if !userID.Valid || int(userID.Int64) != data.UserID {
		t.Fatalf("user_id = %v, want %d", userID, data.UserID)
	}
	if actorCustomerID.Valid {
		t.Fatalf("actor_portal_customer_id = %d, want NULL", actorCustomerID.Int64)
	}
}
