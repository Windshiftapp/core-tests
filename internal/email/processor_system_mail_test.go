//go:build test

package email_test

import (
	"context"
	"testing"

	"windshift/internal/email"
)

type systemMailTrackingRow struct {
	ItemID        *int
	CommentID     *int
	SkippedReason *string
	CompletedAt   *string
}

func getSystemMailTrackingRow(t *testing.T, env *processorTestEnv, dedupKey string) systemMailTrackingRow {
	t.Helper()
	var row systemMailTrackingRow
	err := env.db.QueryRow(`
		SELECT item_id, comment_id, skipped_reason, completed_at
		FROM email_message_tracking
		WHERE channel_id = ? AND dedup_key = ?
	`, env.channelID, dedupKey).Scan(&row.ItemID, &row.CommentID, &row.SkippedReason, &row.CompletedAt)
	if err != nil {
		t.Fatalf("load tracking row %s: %v", dedupKey, err)
	}
	return row
}

func countPortalCustomers(t *testing.T, env *processorTestEnv) int {
	t.Helper()
	var n int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM portal_customers`).Scan(&n); err != nil {
		t.Fatalf("count portal customers: %v", err)
	}
	return n
}

// A delivery failure notification must not become a ticket, must not create a
// portal customer, and must be recorded on the tracking row for diagnostics.
func TestProcessor_SkipsDeliveryFailureAndRecordsDiagnostic(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	ctx := context.Background()

	message := newTestEmail(
		"<bounce-1@example.com>",
		"Undeliverable: Windshift — You have 9 new notifications",
		"Delivery has failed to these recipients:\n\nstefan@example.com\nRemote server returned '550 5.7.509 Access denied'",
	)
	message.From = email.EmailAddress{Name: "MAILER-DAEMON", Address: "postmaster@outlook.com"}

	result, err := env.processor.ProcessEmail(ctx, message, env.channelID, 0, env.config)
	if err != nil {
		t.Fatalf("process bounce: %v", err)
	}
	if result.Action != email.ActionSkipped {
		t.Fatalf("action = %s, want %s", result.Action, email.ActionSkipped)
	}
	if result.SkipReason != string(email.SystemMailDeliveryFailure) {
		t.Fatalf("skip reason = %q, want %q", result.SkipReason, email.SystemMailDeliveryFailure)
	}
	if result.ItemID != nil || result.CommentID != nil {
		t.Fatalf("expected no item/comment, got item=%v comment=%v", result.ItemID, result.CommentID)
	}
	if items := countItems(t, env); items != 0 {
		t.Fatalf("items created = %d, want 0", items)
	}
	if customers := countPortalCustomers(t, env); customers != 0 {
		t.Fatalf("portal customers created = %d, want 0", customers)
	}

	row := getSystemMailTrackingRow(t, env, "bounce-1@example.com")
	if row.ItemID != nil || row.CommentID != nil {
		t.Fatalf("tracking row refs = item %v comment %v, want NULL", row.ItemID, row.CommentID)
	}
	if row.SkippedReason == nil || *row.SkippedReason != string(email.SystemMailDeliveryFailure) {
		t.Fatalf("skipped_reason = %v, want %q", row.SkippedReason, email.SystemMailDeliveryFailure)
	}
	if row.CompletedAt == nil {
		t.Fatal("completed_at is NULL; a redelivery would be reprocessed")
	}

	// Redelivery stays deduplicated and creates nothing.
	again, err := env.processor.ProcessEmail(ctx, message, env.channelID, 0, env.config)
	if err != nil {
		t.Fatalf("redeliver bounce: %v", err)
	}
	if again.Action != email.ActionAlreadyExists {
		t.Fatalf("redelivery action = %s, want %s", again.Action, email.ActionAlreadyExists)
	}
	if items := countItems(t, env); items != 0 {
		t.Fatalf("items after redelivery = %d, want 0", items)
	}
}

// Auto-replies are skipped too, and never appended to an existing thread.
func TestProcessor_SkipsAutoReplyWithoutComment(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	ctx := context.Background()

	original, err := env.processor.ProcessEmail(
		ctx, newTestEmail("<thread-root@example.com>", "Need help", "Please help"), env.channelID, 0, env.config)
	if err != nil {
		t.Fatalf("process original: %v", err)
	}
	if original.Action != email.ActionItemCreated {
		t.Fatalf("original action = %s, want %s", original.Action, email.ActionItemCreated)
	}

	autoReply := newTestReplyEmail("<ooo-1@example.com>", "<thread-root@example.com>", "I am out of office until Monday.")
	autoReply.Subject = "Automatic reply: Need help"
	autoReply.RawHeaders = map[string][]string{"Auto-Submitted": {"auto-replied"}}

	result, err := env.processor.ProcessEmail(ctx, autoReply, env.channelID, 0, env.config)
	if err != nil {
		t.Fatalf("process auto-reply: %v", err)
	}
	if result.Action != email.ActionSkipped {
		t.Fatalf("action = %s, want %s", result.Action, email.ActionSkipped)
	}
	if result.SkipReason != string(email.SystemMailAutoReply) {
		t.Fatalf("skip reason = %q, want %q", result.SkipReason, email.SystemMailAutoReply)
	}
	if result.CommentID != nil {
		t.Fatalf("auto-reply created comment %d, want none", *result.CommentID)
	}

	var comments int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM comments WHERE item_id = ?`, *original.ItemID).Scan(&comments); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if comments != 0 {
		t.Fatalf("comments on thread = %d, want 0", comments)
	}

	row := getSystemMailTrackingRow(t, env, "ooo-1@example.com")
	if row.SkippedReason == nil || *row.SkippedReason != string(email.SystemMailAutoReply) {
		t.Fatalf("skipped_reason = %v, want %q", row.SkippedReason, email.SystemMailAutoReply)
	}
}

// Normal customer mail still becomes a ticket.
func TestProcessor_StillCreatesItemForCustomerMail(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	ctx := context.Background()

	result, err := env.processor.ProcessEmail(
		ctx, newTestEmail("<normal-1@example.com>", "Need help with billing", "Please check my invoice"), env.channelID, 0, env.config)
	if err != nil {
		t.Fatalf("process normal mail: %v", err)
	}
	if result.Action != email.ActionItemCreated {
		t.Fatalf("action = %s, want %s", result.Action, email.ActionItemCreated)
	}
	if items := countItems(t, env); items != 1 {
		t.Fatalf("items = %d, want 1", items)
	}

	row := getSystemMailTrackingRow(t, env, "normal-1@example.com")
	if row.SkippedReason != nil {
		t.Fatalf("skipped_reason = %v, want NULL for customer mail", *row.SkippedReason)
	}
}
