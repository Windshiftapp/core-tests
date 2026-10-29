//go:build test

package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// TestCleanupRejectsOverflowAge pins the WI-1557 fix: age filters beyond the
// documented bound are rejected with 400 before any arithmetic — at the
// time.Duration overflow boundary (106752 days) the cutoff would wrap into
// the future and broaden the irreversible erasure instead of narrowing it.
func TestCleanupRejectsOverflowAge(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO portal_customers (name, email, created_via, created_at) VALUES ('Overflow Subject', 'overflow-subject@test.local', ?, ?)`,
		models.CustomerCreatedViaEmailIntake, time.Now().Add(-30*24*time.Hour),
	); err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	actor := AuditActor{UserID: 1, Username: "admin"}
	for _, age := range []int{MaxBulkCleanupAgeDays + 1, 106752, 1 << 30} {
		_, err := CleanupAutoCreatedCustomers(db, actor, CustomerBulkCleanupInput{
			CreatedVia:    models.CustomerCreatedViaEmailIntake,
			OlderThanDays: age,
		})
		if err == nil {
			t.Fatalf("older_than_days=%d must be rejected", age)
		}
		var se *ServiceError
		if !errors.As(err, &se) || se.StatusCode != 400 {
			t.Fatalf("older_than_days=%d error = %v, want 400 ServiceError", age, err)
		}
	}

	// The boundary value itself is accepted, and the 30-day-old customer
	// survives a 100000-day window (the wrapped cutoff would have erased it).
	if _, err := CleanupAutoCreatedCustomers(db, actor, CustomerBulkCleanupInput{
		CreatedVia:    models.CustomerCreatedViaEmailIntake,
		OlderThanDays: MaxBulkCleanupAgeDays,
	}); err != nil {
		t.Fatalf("boundary age %d must be accepted: %v", MaxBulkCleanupAgeDays, err)
	}
	var erasedAt *time.Time
	if err := db.QueryRowContext(ctx,
		`SELECT erased_at FROM portal_customers WHERE email = ?`, "overflow-subject@test.local",
	).Scan(&erasedAt); err != nil {
		t.Fatalf("read customer: %v", err)
	}
	if erasedAt != nil {
		t.Fatalf("customer erased by overflowing age filter run: %v", *erasedAt)
	}
}

// TestCleanupReachesPastSkippedPage pins the WI-1556 fix: a run must advance
// past a full page (and any number) of excluded customers and drain the
// eligible ones behind them, without erasing ticket holders, and repeat calls
// must not resurrect or double-erase anything.
func TestCleanupReachesPastSkippedPage(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	ctx := context.Background()
	now := time.Now()
	old := now.Add(-90 * 24 * time.Hour)

	insertCustomer := func(email string) int64 {
		t.Helper()
		var id int64
		if err := db.QueryRowContext(ctx,
			`INSERT INTO portal_customers (name, email, created_via, created_at) VALUES (?, ?, ?, ?) RETURNING id`,
			"Intake "+email, email, models.CustomerCreatedViaEmailIntake, old,
		).Scan(&id); err != nil {
			t.Fatalf("seed customer %s: %v", email, err)
		}
		return id
	}
	insertItemWithRank := func(customerID int64, rank string) {
		t.Helper()
		var workspaceID int64
		if err := db.QueryRowContext(ctx, `SELECT id FROM workspaces LIMIT 1`).Scan(&workspaceID); err != nil {
			t.Fatalf("load workspace: %v", err)
		}
		var itemNumber int64
		if err := db.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(workspace_item_number), 0) + 1 FROM items WHERE workspace_id = ?`, workspaceID,
		).Scan(&itemNumber); err != nil {
			t.Fatalf("next item number: %v", err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO items (workspace_id, workspace_item_number, title, creator_portal_customer_id, frac_index, created_at) VALUES (?, ?, 'ticket', ?, ?, ?)`,
			workspaceID, itemNumber, customerID, rank, now,
		); err != nil {
			t.Fatalf("seed item: %v", err)
		}
	}

	// A full default page of ticket holders, then eligible customers behind
	// them — the WI-1556 reproduction shape.
	for i := 0; i < DefaultBulkCleanupBatchLimit; i++ {
		cid := insertCustomer(fmt.Sprintf("ticketed%03d@test.local", i))
		insertItemWithRank(cid, fmt.Sprintf("a-%06d", i))
	}
	eligible := make([]int64, 0, 3)
	for i := 0; i < 3; i++ {
		eligible = append(eligible, insertCustomer(fmt.Sprintf("clean%03d@test.local", i)))
	}

	actor := AuditActor{UserID: 1, Username: "admin"}
	result, err := CleanupAutoCreatedCustomers(db, actor, CustomerBulkCleanupInput{
		CreatedVia: models.CustomerCreatedViaEmailIntake,
	})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if len(result.Erased) != 3 {
		t.Fatalf("erased = %d, want the 3 eligible customers behind the skipped page", len(result.Erased))
	}
	erasedIDs := map[int64]bool{}
	for _, e := range result.Erased {
		erasedIDs[int64(e.ID)] = true
	}
	for _, id := range eligible {
		if !erasedIDs[id] {
			t.Errorf("eligible customer %d was not erased", id)
		}
	}
	if len(result.Skipped) != DefaultBulkCleanupBatchLimit {
		t.Fatalf("skipped = %d, want %d", len(result.Skipped), DefaultBulkCleanupBatchLimit)
	}
	for _, s := range result.Skipped {
		if s.Reason != "has_items" {
			t.Errorf("skip reason = %q, want has_items", s.Reason)
		}
	}
	if len(result.Failed) != 0 {
		t.Errorf("failed = %d, want 0", len(result.Failed))
	}

	// Repeat call: erased rows no longer match, ticket holders are skipped
	// again — the backlog is drained without re-erasing anything.
	repeat, err := CleanupAutoCreatedCustomers(db, actor, CustomerBulkCleanupInput{
		CreatedVia: models.CustomerCreatedViaEmailIntake,
	})
	if err != nil {
		t.Fatalf("repeat cleanup: %v", err)
	}
	if len(repeat.Erased) != 0 {
		t.Errorf("repeat run erased = %d, want 0", len(repeat.Erased))
	}
	if len(repeat.Skipped) != DefaultBulkCleanupBatchLimit {
		t.Errorf("repeat run skipped = %d, want %d", len(repeat.Skipped), DefaultBulkCleanupBatchLimit)
	}

	// Erased rows are pseudonymized; ticket holders keep their identity.
	for _, e := range result.Erased {
		var name string
		if err := db.QueryRowContext(ctx, `SELECT name FROM portal_customers WHERE id = ?`, e.ID).Scan(&name); err != nil {
			t.Fatalf("erased row missing: %v", err)
		}
		if !strings.HasPrefix(name, "deleted-customer-") {
			t.Errorf("erased customer %d name = %q, want pseudonym", e.ID, name)
		}
	}
	var intact int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM portal_customers WHERE created_via = ? AND name LIKE 'Intake %'`,
		models.CustomerCreatedViaEmailIntake,
	).Scan(&intact); err != nil {
		t.Fatalf("count ticket holders: %v", err)
	}
	if intact != DefaultBulkCleanupBatchLimit {
		t.Errorf("ticket holders with intact identity = %d, want %d", intact, DefaultBulkCleanupBatchLimit)
	}
}
