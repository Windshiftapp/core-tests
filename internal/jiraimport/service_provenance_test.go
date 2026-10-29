//go:build test

package jiraimport

import (
	"context"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/testutils"
)

// TestEnsurePortalCustomerRecordsImportProvenance pins the WI-1558 fix:
// Jira Service Management import creates portal customers with
// created_via='ticket-import', and reusing an existing customer preserves
// whatever provenance it already had.
func TestEnsurePortalCustomerRecordsImportProvenance(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	svc := New(db, blobstore.Disabled())

	created, err := svc.EnsurePortalCustomer("Imported User", "jira-imported@test.local", 0)
	if err != nil {
		t.Fatalf("EnsurePortalCustomer: %v", err)
	}
	if !created.Created {
		t.Fatalf("customer.Created = false, want true for a new import customer")
	}

	var provenance string
	if err := db.QueryRowContext(context.Background(),
		`SELECT created_via FROM portal_customers WHERE id = ?`, created.ID,
	).Scan(&provenance); err != nil {
		t.Fatalf("read created_via: %v", err)
	}
	if provenance != "ticket-import" {
		t.Fatalf("created_via = %q, want ticket-import", provenance)
	}

	// Reuse: an existing customer is found, not recreated, and its
	// provenance is preserved.
	reused, err := svc.EnsurePortalCustomer("Imported User", "jira-imported@test.local", 0)
	if err != nil {
		t.Fatalf("second EnsurePortalCustomer: %v", err)
	}
	if reused.Created {
		t.Fatal("second call created a duplicate customer")
	}
	if reused.ID != created.ID {
		t.Fatalf("second call returned customer %d, want existing %d", reused.ID, created.ID)
	}
	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM portal_customers WHERE email = ?`, "jira-imported@test.local",
	).Scan(&count); err != nil {
		t.Fatalf("count customers: %v", err)
	}
	if count != 1 {
		t.Fatalf("customers with the email = %d, want 1", count)
	}

	// An existing customer with different provenance keeps it on reuse.
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO portal_customers (name, email, created_via) VALUES ('Agent Created', 'jira-agent@test.local', 'agent')`,
	); err != nil {
		t.Fatalf("seed agent customer: %v", err)
	}
	agentReused, err := svc.EnsurePortalCustomer("Agent Created", "jira-agent@test.local", 0)
	if err != nil {
		t.Fatalf("EnsurePortalCustomer for agent customer: %v", err)
	}
	if agentReused.Created {
		t.Fatal("existing agent customer must be reused, not recreated")
	}
	if err := db.QueryRowContext(context.Background(),
		`SELECT created_via FROM portal_customers WHERE email = ?`, "jira-agent@test.local",
	).Scan(&provenance); err != nil {
		t.Fatalf("read agent provenance: %v", err)
	}
	if provenance != "agent" {
		t.Fatalf("agent customer provenance = %q, want unchanged 'agent'", provenance)
	}
}
