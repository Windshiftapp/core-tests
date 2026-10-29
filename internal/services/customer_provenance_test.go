//go:build test

package services

import (
	"context"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// TestMagicLinkFindOrCreateRecordsProvenance pins the WI-1553 magic-link
// creation path: customers auto-created by a magic-link sign-in request carry
// created_via='magic-link'.
func TestMagicLinkFindOrCreateRecordsProvenance(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	svc := NewMagicLinkService(db, nil, "")

	customerID, err := svc.FindOrCreatePortalCustomer("magicuser@test.local", "Magic User", 1)
	if err != nil {
		t.Fatalf("find or create: %v", err)
	}

	var createdVia string
	if err := db.QueryRowContext(context.Background(),
		`SELECT created_via FROM portal_customers WHERE id = ?`, customerID,
	).Scan(&createdVia); err != nil {
		t.Fatalf("read created_via: %v", err)
	}
	if createdVia != models.CustomerCreatedViaMagicLink {
		t.Fatalf("created_via = %q, want %q", createdVia, models.CustomerCreatedViaMagicLink)
	}

	// An existing customer keeps its provenance (no rewrite on find).
	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM portal_customers WHERE email = ? AND created_via = ?`, "magicuser@test.local", models.CustomerCreatedViaMagicLink,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("rows = %d, want exactly one with magic-link provenance", count)
	}
}

// TestPortalCustomerProvenanceDefaultPinsBackfill pins the backfill contract:
// rows created without an explicit provenance (the pre-WI-1553 shape) default
// to 'unknown'.
func TestPortalCustomerProvenanceDefaultPinsBackfill(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	var customerID int64
	if err := db.QueryRowContext(context.Background(),
		`INSERT INTO portal_customers (name, email) VALUES ('Legacy Row', 'legacy@test.local') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("insert legacy-shaped row: %v", err)
	}

	var createdVia string
	if err := db.QueryRowContext(context.Background(),
		`SELECT created_via FROM portal_customers WHERE id = ?`, customerID,
	).Scan(&createdVia); err != nil {
		t.Fatalf("read created_via: %v", err)
	}
	if createdVia != models.CustomerCreatedViaUnknown {
		t.Fatalf("created_via = %q, want %q (backfill default)", createdVia, models.CustomerCreatedViaUnknown)
	}
}
