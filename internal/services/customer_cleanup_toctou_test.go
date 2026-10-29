//go:build test

package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

// attachSessionForCleanup inserts one live session for the customer and
// returns the raw token.
func attachSessionForCleanup(t *testing.T, db database.Database, customerID int64) string {
	t.Helper()
	var channelID int64
	if err := db.QueryRowContext(context.Background(),
		`INSERT INTO channels (name, type, direction, status) VALUES ('toctou-portal', 'portal', 'inbound', 'enabled') RETURNING id`,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	token := "toctou-session-token"
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO portal_customer_sessions (portal_customer_id, session_token, channel_id, expires_at, ip_address, user_agent, is_active) VALUES (?, ?, ?, ?, '10.0.0.1', 'ua', ?)`,
		customerID, token, channelID, time.Now().Add(24*time.Hour), true,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return token
}

// insertCleanupItem creates a minimal item and returns its id.
func insertCleanupItem(t *testing.T, db database.Database, customerID *int64) int64 {
	t.Helper()
	ctx := context.Background()
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
	rank := fmt.Sprintf("t-%d", itemNumber)
	if customerID != nil {
		var id int64
		if err := db.QueryRowContext(ctx,
			`INSERT INTO items (workspace_id, workspace_item_number, title, creator_portal_customer_id, frac_index) VALUES (?, ?, 'ticket', ?, ?) RETURNING id`,
			workspaceID, itemNumber, *customerID, rank,
		).Scan(&id); err != nil {
			t.Fatalf("seed item: %v", err)
		}
		return id
	}
	var id int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO items (workspace_id, workspace_item_number, title, frac_index) VALUES (?, ?, 'ticket', ?) RETURNING id`,
		workspaceID, itemNumber, rank,
	).Scan(&id); err != nil {
		t.Fatalf("seed item: %v", err)
	}
	return id
}

// seedAttachmentForCleanup inserts one attachment uploaded by the customer.
func seedAttachmentForCleanup(t *testing.T, db database.Database, customerID int64) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO attachments (filename, original_filename, file_path, mime_type, file_size, uploaded_by_portal_customer_id) VALUES ('a.png', 'a.png', '/tmp/a.png', 'image/png', 10, ?)`,
		customerID,
	); err != nil {
		t.Fatalf("seed attachment: %v", err)
	}
}

// TestCleanupSkipsNewlyTicketedCustomers pins the WI-1555 fix: a ticket that
// lands between candidate selection and the erasure transaction excludes the
// customer — identity and auth state survive and the report records a skip.
func TestCleanupSkipsNewlyTicketedCustomers(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	ctx := context.Background()
	old := time.Now().Add(-90 * 24 * time.Hour)

	insertCustomer := func(email string) int64 {
		t.Helper()
		var id int64
		if err := db.QueryRowContext(ctx,
			`INSERT INTO portal_customers (name, email, created_via, created_at) VALUES (?, ?, ?, ?) RETURNING id`,
			"Raced "+email, email, models.CustomerCreatedViaEmailIntake, old,
		).Scan(&id); err != nil {
			t.Fatalf("seed customer: %v", err)
		}
		return id
	}

	// Selection happens first (the same query the run uses), then the
	// concurrent tickets commit, then the cleanup runs.
	racedID := insertCustomer("raced-items@test.local")
	sessionToken := attachSessionForCleanup(t, db, racedID)
	racedItemID := insertCleanupItem(t, db, &racedID)

	commentOnlyID := insertCustomer("raced-comments@test.local")
	commentItemID := insertCleanupItem(t, db, nil)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO comments (item_id, portal_customer_id, content) VALUES (?, ?, 'late comment')`,
		commentItemID, commentOnlyID,
	); err != nil {
		t.Fatalf("seed comment: %v", err)
	}

	attachmentOnlyID := insertCustomer("raced-attachments@test.local")
	seedAttachmentForCleanup(t, db, attachmentOnlyID)

	actor := AuditActor{UserID: 1, Username: "admin"}
	result, err := CleanupAutoCreatedCustomers(db, actor, CustomerBulkCleanupInput{
		CreatedVia: models.CustomerCreatedViaEmailIntake,
	})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if len(result.Erased) != 0 {
		t.Fatalf("erased = %d, want 0 (every candidate gained ticket content)", len(result.Erased))
	}
	skippedReasons := map[int64]string{}
	for _, s := range result.Skipped {
		skippedReasons[int64(s.ID)] = s.Reason
	}
	if skippedReasons[racedID] != "has_items" {
		t.Errorf("raced-items customer skip reason = %q, want has_items", skippedReasons[racedID])
	}
	if skippedReasons[commentOnlyID] != "has_comments" {
		t.Errorf("comment-only customer skip reason = %q, want has_comments", skippedReasons[commentOnlyID])
	}
	if skippedReasons[attachmentOnlyID] != "has_attachments" {
		t.Errorf("attachment-only customer skip reason = %q, want has_attachments", skippedReasons[attachmentOnlyID])
	}
	if racedItemID == 0 || sessionToken == "" {
		t.Fatal("fixture setup incomplete")
	}

	// Identity and auth state survive: no pseudonymization, no erased_at,
	// the live session is untouched.
	for _, id := range []int64{racedID, commentOnlyID, attachmentOnlyID} {
		var name, email string
		var erasedAt *time.Time
		if err := db.QueryRowContext(ctx,
			`SELECT name, email, erased_at FROM portal_customers WHERE id = ?`, id,
		).Scan(&name, &email, &erasedAt); err != nil {
			t.Fatalf("read customer %d: %v", id, err)
		}
		if erasedAt != nil {
			t.Errorf("customer %d was erased; want skipped", id)
		}
		if strings.HasPrefix(name, "deleted-customer-") || strings.Contains(email, "@erased.invalid") {
			t.Errorf("customer %d was pseudonymized: name=%q email=%q", id, name, email)
		}
	}
	var sessions int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM portal_customer_sessions WHERE portal_customer_id = ?`, racedID,
	).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 1 {
		t.Errorf("raced customer sessions = %d, want 1 (auth state must survive)", sessions)
	}
}

// TestExplicitErasureStillCoversTicketHolders pins the boundary between the
// two erasure entry points: the DSAR path intentionally erases ticket
// holders, while the cleanup guard refuses them.
func TestExplicitErasureStillCoversTicketHolders(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	ctx := context.Background()
	var customerID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO portal_customers (name, email, created_via) VALUES ('DSAR Subject', 'dsar-ticketed@test.local', ?) RETURNING id`,
		models.CustomerCreatedViaEmailIntake,
	).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	insertCleanupItem(t, db, &customerID)

	actor := AuditActor{UserID: 1, Username: "admin"}

	// The cleanup guard refuses ticket holders.
	if _, err := EraseCustomerWithOptions(db, int(customerID), actor, CustomerErasureInput{
		RequestedBy: "bulk-cleanup",
	}, EraseOptions{RefuseWhenTicketed: true}); err == nil {
		t.Fatal("cleanup-guarded erasure of a ticket holder must be refused")
	} else {
		var ticketed *CustomerTicketedError
		if !errors.As(err, &ticketed) {
			t.Fatalf("error = %v, want CustomerTicketedError", err)
		}
		if ticketed.Reason != "has_items" {
			t.Errorf("reason = %q, want has_items", ticketed.Reason)
		}
	}

	// No side effects from the refused attempt.
	var evidenceRows int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM customer_erasure_records WHERE portal_customer_id = ?`, customerID,
	).Scan(&evidenceRows); err != nil {
		t.Fatalf("count erasure records: %v", err)
	}
	if evidenceRows != 0 {
		t.Errorf("erasure records after refused attempt = %d, want 0", evidenceRows)
	}

	// The explicit DSAR path erases ticket holders by design.
	evidence, err := EraseCustomer(db, int(customerID), actor, CustomerErasureInput{
		RequestedBy: "dsar intake",
	})
	if err != nil {
		t.Fatalf("explicit DSAR erasure: %v", err)
	}
	if evidence.CustomerID != int(customerID) {
		t.Fatalf("evidence customer = %d, want %d", evidence.CustomerID, customerID)
	}
	var erasedAt *time.Time
	if err := db.QueryRowContext(ctx,
		`SELECT erased_at FROM portal_customers WHERE id = ?`, customerID,
	).Scan(&erasedAt); err != nil {
		t.Fatalf("read customer: %v", err)
	}
	if erasedAt == nil {
		t.Fatal("explicit DSAR erasure did not stamp erased_at")
	}
}
