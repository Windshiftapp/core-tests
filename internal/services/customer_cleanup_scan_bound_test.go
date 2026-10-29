//go:build test

package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// TestCleanupScanBudgetBoundsWork pins the bulk-cleanup scan bound: a table of
// ticket holders larger than the scan budget must stop at the budget instead of
// walking every row with a per-customer transaction, and a run must report that
// it truncated.
func TestCleanupScanBudgetBoundsWork(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	ctx := context.Background()
	now := time.Now()
	old := now.Add(-90 * 24 * time.Hour)

	var workspaceID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM workspaces LIMIT 1`).Scan(&workspaceID); err != nil {
		t.Fatalf("load workspace: %v", err)
	}

	insertTicketHolder := func(i int) {
		t.Helper()
		var customerID int64
		if err := db.QueryRowContext(ctx,
			`INSERT INTO portal_customers (name, email, created_via, created_at) VALUES (?, ?, ?, ?) RETURNING id`,
			"Intake", fmt.Sprintf("ticketed-%05d@test.local", i), models.CustomerCreatedViaEmailIntake, old,
		).Scan(&customerID); err != nil {
			t.Fatalf("seed customer %d: %v", i, err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO items (workspace_id, workspace_item_number, title, creator_portal_customer_id, frac_index, created_at)
			 VALUES (?, ?, 'ticket', ?, ?, ?)`,
			workspaceID, i+1, customerID, fmt.Sprintf("a-%06d", i), now,
		); err != nil {
			t.Fatalf("seed item %d: %v", i, err)
		}
	}

	// More ticket holders than the scan budget. The eligible customer sits
	// beyond the budget so a bounded run must not reach it.
	for i := 0; i < MaxBulkCleanupScan; i++ {
		insertTicketHolder(i)
	}
	var eligibleID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO portal_customers (name, email, created_via, created_at) VALUES ('Clean', 'clean-after-budget@test.local', ?, ?) RETURNING id`,
		models.CustomerCreatedViaEmailIntake, old,
	).Scan(&eligibleID); err != nil {
		t.Fatalf("seed eligible customer: %v", err)
	}

	actor := AuditActor{UserID: 1, Username: "admin"}
	result, err := CleanupAutoCreatedCustomers(db, actor, CustomerBulkCleanupInput{
		CreatedVia: models.CustomerCreatedViaEmailIntake,
	})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if result.ScannedCount != MaxBulkCleanupScan {
		t.Fatalf("scanned = %d, want the %d scan budget", result.ScannedCount, MaxBulkCleanupScan)
	}
	if !result.Truncated {
		t.Fatal("truncated = false, want true when the scan budget is reached")
	}
	if len(result.Erased) != 0 {
		t.Fatalf("erased = %d, want 0 (every scanned candidate held a ticket)", len(result.Erased))
	}

	var erasedAt *time.Time
	if err := db.QueryRowContext(ctx, `SELECT erased_at FROM portal_customers WHERE id = ?`, eligibleID).Scan(&erasedAt); err != nil {
		t.Fatalf("read eligible customer: %v", err)
	}
	if erasedAt != nil {
		t.Fatalf("eligible customer beyond the scan budget was erased (%v)", *erasedAt)
	}
}
