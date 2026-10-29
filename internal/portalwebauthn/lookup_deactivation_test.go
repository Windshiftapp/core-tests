//go:build test

package portalwebauthn

import (
	"context"
	"testing"
	"time"

	"windshift/internal/testutils"
)

// TestGetCustomerSurfacesDeactivationState pins the WI-1554 passkey-path
// contract: the login resolver reads deactivated_at from GetCustomer, so a
// deactivated customer's passkey login fails resolution.
func TestGetCustomerSurfacesDeactivationState(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	ctx := context.Background()

	var customerID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO portal_customers (name, email) VALUES ('Passkey Subject', 'pk@test.local') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	store := NewPortalLookupStore(db)

	// Active customer: no deactivation state.
	customer, err := store.GetCustomer(int(customerID))
	if err != nil {
		t.Fatalf("GetCustomer: %v", err)
	}
	if customer.DeactivatedAt != nil {
		t.Fatalf("active customer carries deactivated_at = %v", customer.DeactivatedAt)
	}

	// Deactivated customer: the state is surfaced.
	deactivatedAt := time.Now()
	if _, err := db.ExecContext(ctx,
		`UPDATE portal_customers SET deactivated_at = ? WHERE id = ?`, deactivatedAt, customerID); err != nil {
		t.Fatalf("deactivate customer: %v", err)
	}
	customer, err = store.GetCustomer(int(customerID))
	if err != nil {
		t.Fatalf("GetCustomer after deactivation: %v", err)
	}
	if customer.DeactivatedAt == nil {
		t.Fatal("deactivated customer lost deactivated_at in lookup")
	}
}
