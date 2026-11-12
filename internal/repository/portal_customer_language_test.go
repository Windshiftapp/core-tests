//go:build test

package repository

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/testutils"
)

// TestPortalCustomerLanguagePreferences covers the preference lifecycle:
// first-contact detection writes once, a chosen value is never overwritten by
// later detection, explicit updates win, and lookups are case-insensitive.
func TestPortalCustomerLanguagePreferences(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()
	ctx := context.Background()

	customerID := testutils.InsertID(t, db,
		`INSERT INTO portal_customers (name, email) VALUES (?, ?)`,
		"Alex", "alex@example.com",
	)

	repo := NewPortalCustomerRepository(db)

	// First-contact detection stores the language.
	written, err := repo.SetLanguageIfUnset(customerID, "de")
	if err != nil {
		t.Fatalf("SetLanguageIfUnset: %v", err)
	}
	if !written {
		t.Fatal("first detection did not write the language")
	}
	if got, _ := repo.Language(ctx, customerID); got != "de" {
		t.Fatalf("Language = %q, want de", got)
	}

	// A later login must not overwrite a value that is already set.
	written, err = repo.SetLanguageIfUnset(customerID, "fr")
	if err != nil {
		t.Fatalf("SetLanguageIfUnset (second): %v", err)
	}
	if written {
		t.Fatal("second detection overwrote an existing language")
	}
	if got, _ := repo.Language(ctx, customerID); got != "de" {
		t.Fatalf("Language after second detection = %q, want de", got)
	}

	// The explicit toggle always wins.
	if err := repo.UpdateRegionalSettings(customerID, "es", "Europe/Berlin"); err != nil {
		t.Fatalf("UpdateRegionalSettings: %v", err)
	}
	if got, _ := repo.Language(ctx, customerID); got != "es" {
		t.Fatalf("Language after update = %q, want es", got)
	}

	// Lookup by email is case-insensitive.
	if got, err := repo.LanguageByEmail(ctx, "ALEX@example.com"); err != nil || got != "es" {
		t.Fatalf("LanguageByEmail = (%q, %v), want (es, nil)", got, err)
	}
	if got, err := repo.LanguageByEmail(ctx, "nobody@example.com"); err != nil || got != "" {
		t.Fatalf("LanguageByEmail(unknown) = (%q, %v), want (\"\", nil)", got, err)
	}

	// Missing rows surface as ErrNotFound for id-based reads/writes.
	if _, err := repo.Language(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Language(missing) error = %v, want ErrNotFound", err)
	}
	if err := repo.UpdateRegionalSettings(999999, "de", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateRegionalSettings(missing) error = %v, want ErrNotFound", err)
	}
}

// TestPortalCustomerSetLanguageIfUnsetEmptyIsNoop ensures an unmatched browser
// header (empty locale) never clears or writes the column.
func TestPortalCustomerSetLanguageIfUnsetEmptyIsNoop(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	customerID := testutils.InsertID(t, db,
		`INSERT INTO portal_customers (name, email) VALUES (?, ?)`,
		"Alex", "alex@example.com",
	)

	repo := NewPortalCustomerRepository(db)
	written, err := repo.SetLanguageIfUnset(customerID, "")
	if err != nil {
		t.Fatalf("SetLanguageIfUnset(empty): %v", err)
	}
	if written {
		t.Fatal("empty locale was written")
	}
	if got, _ := repo.Language(context.Background(), customerID); got != "" {
		t.Fatalf("Language = %q, want empty", got)
	}
}
