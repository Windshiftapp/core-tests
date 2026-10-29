//go:build test

package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"windshift/internal/testutils"
)

// TestPortalSessionManagerRefusesDeactivatedCustomers pins the WI-1554
// session-layer contract: validation refuses an otherwise-live session the
// moment the customer carries deactivated_at, and session creation is refused
// on every auth path until reactivation.
func TestPortalSessionManagerRefusesDeactivatedCustomers(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	ctx := context.Background()

	var customerID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO portal_customers (name, email) VALUES ('Deactivation Subject', 'deact@test.local') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	var channelID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO channels (name, type, direction, status) VALUES ('deact-portal', 'portal', 'inbound', 'enabled') RETURNING id`,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	sm := NewPortalSessionManager(db, false, false, nil, "test-secret", "strict")

	// A live customer mints and validates sessions normally.
	session, err := sm.CreatePortalSession(int(customerID), int(channelID), "127.0.0.1", "ua")
	if err != nil {
		t.Fatalf("create session for active customer: %v", err)
	}
	if _, err := sm.ValidatePortalSession(session.Token, "127.0.0.1"); err != nil {
		t.Fatalf("validate session for active customer: %v", err)
	}

	// Deactivate out-of-band (as the admin endpoint would, minus the row
	// deletes it also performs).
	if _, err := db.ExecContext(ctx,
		`UPDATE portal_customers SET deactivated_at = ? WHERE id = ?`, time.Now(), customerID); err != nil {
		t.Fatalf("deactivate customer: %v", err)
	}

	// Existing session validation now refuses.
	if _, err := sm.ValidatePortalSession(session.Token, "127.0.0.1"); !errors.Is(err, ErrPortalSessionNotFound) {
		t.Fatalf("validate session for deactivated customer error = %v, want ErrPortalSessionNotFound", err)
	}

	// Session creation is refused on every auth path (magic-link redemption,
	// passkey login, ...).
	if _, err := sm.CreatePortalSession(int(customerID), int(channelID), "127.0.0.1", "ua"); !errors.Is(err, ErrPortalCustomerDeactivated) {
		t.Fatalf("create session for deactivated customer error = %v, want ErrPortalCustomerDeactivated", err)
	}

	// Reactivation restores session creation.
	if _, err := db.ExecContext(ctx,
		`UPDATE portal_customers SET deactivated_at = NULL WHERE id = ?`, customerID); err != nil {
		t.Fatalf("reactivate customer: %v", err)
	}
	if _, err := sm.CreatePortalSession(int(customerID), int(channelID), "127.0.0.1", "ua"); err != nil {
		t.Fatalf("create session after reactivation: %v", err)
	}
}
