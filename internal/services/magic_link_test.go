package services

import (
	"errors"
	"path/filepath"
	"testing"

	"windshift/internal/database"
)

func TestFindOrCreatePortalCustomerGrantsChannelAccessIdempotently(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "magic-link-customer.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	var channelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction)
		VALUES ('Portal', 'portal', 'inbound') RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	service := NewMagicLinkService(db, nil, "")
	firstID, err := service.FindOrCreatePortalCustomer("  Customer@Example.com ", "Customer", channelID)
	if err != nil {
		t.Fatalf("first FindOrCreatePortalCustomer: %v", err)
	}
	secondID, err := service.FindOrCreatePortalCustomer("customer@example.com", "Ignored", channelID)
	if err != nil {
		t.Fatalf("second FindOrCreatePortalCustomer: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("duplicate lookup returned customer %d, want %d", secondID, firstID)
	}

	var storedEmail string
	if err := db.QueryRow(`SELECT email FROM portal_customers WHERE id = ?`, firstID).Scan(&storedEmail); err != nil {
		t.Fatalf("load customer: %v", err)
	}
	if storedEmail != "customer@example.com" {
		t.Fatalf("stored email = %q, want normalized address", storedEmail)
	}
	var grants int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM portal_customer_channels
		WHERE portal_customer_id = ? AND channel_id = ?
	`, firstID, channelID).Scan(&grants); err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if grants != 1 {
		t.Fatalf("channel grants = %d, want 1", grants)
	}
}

func TestGetPortalCustomerByEmailIsCaseInsensitive(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "magic-link-lookup.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	var channelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction)
		VALUES ('Portal', 'portal', 'inbound') RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	service := NewMagicLinkService(db, nil, "")
	customerID, err := service.FindOrCreatePortalCustomer("Customer@Example.com", "Customer", channelID)
	if err != nil {
		t.Fatalf("FindOrCreatePortalCustomer: %v", err)
	}

	// The stored address is normalized to lowercase; lookups with any casing
	// must still resolve the same customer and name.
	gotID, name, err := service.GetPortalCustomerByEmail("CUSTOMER@example.COM")
	if err != nil {
		t.Fatalf("GetPortalCustomerByEmail: %v", err)
	}
	if gotID != customerID || name != "Customer" {
		t.Fatalf("lookup returned (%d, %q), want (%d, %q)", gotID, name, customerID, "Customer")
	}

	if _, _, err := service.GetPortalCustomerByEmail("missing@example.com"); !errors.Is(err, ErrPortalCustomerNotFound) {
		t.Fatalf("missing email error = %v, want ErrPortalCustomerNotFound", err)
	}
}
