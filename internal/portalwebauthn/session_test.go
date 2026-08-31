//go:build test

package portalwebauthn

import (
	"testing"

	webauthnlib "github.com/go-webauthn/webauthn/webauthn"

	"windshift/internal/testutils"
)

func TestRegistrationSessionRequiresMatchingCustomerAndIsConsumedOnce(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	defer func() { _ = db.Close() }()

	var customerID int
	err := db.QueryRow(
		`INSERT INTO portal_customers (name, email) VALUES (?, ?) RETURNING id`,
		"Passkey Customer",
		"passkey-customer@example.com",
	).Scan(&customerID)
	if err != nil {
		t.Fatalf("insert portal customer: %v", err)
	}
	var otherCustomerID int
	err = db.QueryRow(
		`INSERT INTO portal_customers (name, email) VALUES (?, ?) RETURNING id`,
		"Other Customer",
		"other-customer@example.com",
	).Scan(&otherCustomerID)
	if err != nil {
		t.Fatalf("insert other portal customer: %v", err)
	}

	store := NewSessionStore(db)
	data := &webauthnlib.SessionData{Challenge: "portal-registration-challenge", UserID: []byte("1")}
	sessionID, err := store.SaveRegistrationSession(customerID, data)
	if err != nil {
		t.Fatalf("SaveRegistrationSession: %v", err)
	}

	if _, err := store.GetRegistrationSession(sessionID, otherCustomerID); err == nil {
		t.Fatal("different portal customer consumed registration challenge")
	}
	got, err := store.GetRegistrationSession(sessionID, customerID)
	if err != nil {
		t.Fatalf("GetRegistrationSession for owner: %v", err)
	}
	if got.Challenge != data.Challenge {
		t.Fatalf("challenge = %q, want %q", got.Challenge, data.Challenge)
	}
	if _, err := store.GetRegistrationSession(sessionID, customerID); err == nil {
		t.Fatal("portal registration challenge was reusable")
	}
}
