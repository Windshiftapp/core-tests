//go:build test

package services_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/repository"
	"windshift/internal/services"
)

// insertPendingReply seeds a pending (undelivered) outbox row for a comment.
// The row is inserted directly because the queue under test is only populated
// by the SMTP delivery path itself.
func insertPendingReply(t *testing.T, db database.Database, env emailReplyTestEnv, commentID, attemptCount int, nextAttemptIn time.Duration) {
	t.Helper()
	messageID := fmt.Sprintf("<ws-comment-%d@test.com>", commentID)
	_, err := db.Exec(`
		INSERT INTO email_reply_outbox (
			comment_id, channel_id, item_id, to_email, to_name, subject,
			html_body, text_body, message_id, in_reply_to, references_json,
			from_email, from_name, attempt_count, next_attempt_at, created_at, updated_at
		) VALUES (?, ?, ?, 'customer@example.com', 'Customer One', 'Re: Help',
			'<p>body</p>', 'body', ?, '<first@example.com>', '[]',
			'noreply@test.com', 'Test App', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT (comment_id, to_email) WHERE comment_id IS NOT NULL DO UPDATE SET
			attempt_count = excluded.attempt_count,
			next_attempt_at = excluded.next_attempt_at,
			delivered_at = NULL,
			discarded_at = NULL,
			last_error = NULL
	`, commentID, env.ChannelID, env.ItemID, messageID, attemptCount,
		time.Now().Add(nextAttemptIn).UTC())
	if err != nil {
		t.Fatalf("seed outbox row "+strconv.Itoa(commentID)+": %v", err)
	}
}

// outboxRowState reads back the delivery-relevant columns of one queue row.
func outboxRowState(t *testing.T, db database.Database, commentID int) (attemptCount int, lastError string, deliveredAt, discardedAt *time.Time, nextAttemptAt time.Time) {
	t.Helper()
	var lastErrorPtr *string
	if err := db.QueryRow(`
		SELECT attempt_count, last_error, delivered_at, discarded_at, next_attempt_at
		FROM email_reply_outbox WHERE comment_id = ?
	`, commentID).Scan(&attemptCount, &lastErrorPtr, &deliveredAt, &discardedAt, &nextAttemptAt); err != nil {
		t.Fatalf("load outbox row "+strconv.Itoa(commentID)+": %v", err)
	}
	if lastErrorPtr != nil {
		lastError = *lastErrorPtr
	}
	return attemptCount, lastError, deliveredAt, discardedAt, nextAttemptAt
}

// TestEmailReplyService_RetryPendingReply_DeliversNow verifies an operator
// retry delivers immediately even while the row sits in a backoff lease, and
// marks the row delivered with its outbound threading record.
func TestEmailReplyService_RetryPendingReply_DeliversNow(t *testing.T) {
	db := createCommentTestDB(t)
	env := setupEmailReplyTestEnv(t, db)
	insertTestComment(t, db, 9001, env.ItemID, env.UserID)
	insertPendingReply(t, db, env, 9001, 3, 30*time.Minute) // stuck in backoff

	sender := &mockSMTPSender{configured: true}
	svc := services.NewEmailReplyService(db, sender)

	delivered, err := svc.RetryPendingReply(int(env.ChannelID), 9001)
	if err != nil {
		t.Fatalf("RetryPendingReply: %v", err)
	}
	if !delivered {
		t.Fatal("delivered = false, want true")
	}
	if len(sender.sent) != 1 {
		t.Fatalf("SMTP sends = %d, want 1", len(sender.sent))
	}

	_, lastError, deliveredAt, discardedAt, _ := outboxRowState(t, db, 9001)
	if deliveredAt == nil {
		t.Fatal("row not marked delivered after successful retry")
	}
	if discardedAt != nil {
		t.Fatal("row unexpectedly discarded")
	}
	if lastError != "" {
		t.Fatalf("last_error = %q, want empty", lastError)
	}

	// Delivered Message-ID must be tracked so customer replies keep threading.
	var tracked int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM email_message_tracking
		WHERE channel_id = ? AND item_id = ? AND direction = 'outbound' AND comment_id = ?
	`, env.ChannelID, env.ItemID, 9001).Scan(&tracked); err != nil {
		t.Fatalf("count outbound tracking: %v", err)
	}
	if tracked != 1 {
		t.Fatalf("outbound tracking rows = %d, want 1", tracked)
	}
}

// TestEmailReplyService_RetryPendingReply_RecordsFailure verifies a failed
// explicit retry is booked like a scheduler attempt: counted, stored, and
// returned to the backoff schedule instead of being silently dropped.
func TestEmailReplyService_RetryPendingReply_RecordsFailure(t *testing.T) {
	db := createCommentTestDB(t)
	env := setupEmailReplyTestEnv(t, db)
	insertTestComment(t, db, 9002, env.ItemID, env.UserID)
	insertPendingReply(t, db, env, 9002, 1, 30*time.Minute)

	sender := &mockSMTPSender{configured: true, sendErr: errors.New("smtp relay down")}
	svc := services.NewEmailReplyService(db, sender)

	delivered, err := svc.RetryPendingReply(int(env.ChannelID), 9002)
	if delivered {
		t.Fatal("delivered = true, want false")
	}
	if err == nil {
		t.Fatal("expected send error to propagate")
	}

	attemptCount, lastError, deliveredAt, _, nextAttemptAt := outboxRowState(t, db, 9002)
	if attemptCount != 2 {
		t.Fatalf("attempt_count = %d, want 2", attemptCount)
	}
	if lastError == "" {
		t.Fatal("last_error not recorded")
	}
	if deliveredAt != nil {
		t.Fatal("row marked delivered despite send failure")
	}
	if !nextAttemptAt.After(time.Now()) {
		t.Fatalf("next_attempt_at = %v, want a future backoff", nextAttemptAt)
	}
}

// TestEmailReplyService_RetryPendingReply_NotRetryable pins the conflict
// contract: delivered and discarded rows refuse retries, unknown rows or
// channels report not-found.
func TestEmailReplyService_RetryPendingReply_NotRetryable(t *testing.T) {
	db := createCommentTestDB(t)
	env := setupEmailReplyTestEnv(t, db)
	insertTestComment(t, db, 9003, env.ItemID, env.UserID)
	insertTestComment(t, db, 9004, env.ItemID, env.UserID)
	insertPendingReply(t, db, env, 9003, 0, 0)
	insertPendingReply(t, db, env, 9004, 0, 0)
	svc := services.NewEmailReplyService(db, &mockSMTPSender{configured: true})
	channelID := int(env.ChannelID)

	t.Run("delivered row conflicts", func(t *testing.T) {
		if _, err := db.Exec(`UPDATE email_reply_outbox SET delivered_at = CURRENT_TIMESTAMP WHERE comment_id = 9003`); err != nil {
			t.Fatalf("mark delivered: %v", err)
		}
		if _, err := svc.RetryPendingReply(channelID, 9003); !errors.Is(err, services.ErrEmailReplyNotRetryable) {
			t.Fatalf("err = %v, want ErrEmailReplyNotRetryable", err)
		}
	})

	t.Run("discarded row conflicts", func(t *testing.T) {
		if _, err := db.Exec(`UPDATE email_reply_outbox SET discarded_at = CURRENT_TIMESTAMP WHERE comment_id = 9004`); err != nil {
			t.Fatalf("mark discarded: %v", err)
		}
		if _, err := svc.RetryPendingReply(channelID, 9004); !errors.Is(err, services.ErrEmailReplyNotRetryable) {
			t.Fatalf("err = %v, want ErrEmailReplyNotRetryable", err)
		}
	})

	t.Run("unknown comment not found", func(t *testing.T) {
		if _, err := svc.RetryPendingReply(channelID, 999999); !errors.Is(err, repository.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("row from another channel not found", func(t *testing.T) {
		if _, err := svc.RetryPendingReply(channelID+1000, 9003); !errors.Is(err, repository.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// TestEmailReplyService_RetryPendingReply_SMTPNotConfigured verifies retry
// fails fast with the sentinel when no outbound SMTP is configured.
func TestEmailReplyService_RetryPendingReply_SMTPNotConfigured(t *testing.T) {
	db := createCommentTestDB(t)
	env := setupEmailReplyTestEnv(t, db)
	insertTestComment(t, db, 9005, env.ItemID, env.UserID)
	insertPendingReply(t, db, env, 9005, 0, 0)

	svc := services.NewEmailReplyService(db, &mockSMTPSender{configured: false})

	if _, err := svc.RetryPendingReply(int(env.ChannelID), 9005); !errors.Is(err, services.ErrSMTPNotConfigured) {
		t.Fatalf("err = %v, want ErrSMTPNotConfigured", err)
	}
	_, _, deliveredAt, _, _ := outboxRowState(t, db, 9005)
	if deliveredAt != nil {
		t.Fatal("row delivered without SMTP")
	}
}

// TestEmailReplyService_SkipsDiscardedReplies verifies a discarded row never
// reaches SMTP again, even when its backoff has already elapsed.
func TestEmailReplyService_SkipsDiscardedReplies(t *testing.T) {
	db := createCommentTestDB(t)
	env := setupEmailReplyTestEnv(t, db)
	insertTestComment(t, db, 9006, env.ItemID, env.UserID)
	insertPendingReply(t, db, env, 9006, 0, -time.Minute) // due now

	repo := repository.NewChannelRepository(db)
	discarded, err := repo.DiscardEmailReply(t.Context(), int(env.ChannelID), 9006)
	if err != nil || discarded != repository.EmailReplyDiscarded {
		t.Fatalf("DiscardEmailReply = (%v, %v), want (Discarded, nil)", discarded, err)
	}

	sender := &mockSMTPSender{configured: true}
	svc := services.NewEmailReplyService(db, sender)
	delivered, err := svc.ProcessPendingReplies(context.Background(), 10)
	if err != nil {
		t.Fatalf("ProcessPendingReplies: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("delivered = %d, want 0", delivered)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("SMTP sends = %d, want 0 for discarded reply", len(sender.sent))
	}
	_, _, deliveredAt, _, _ := outboxRowState(t, db, 9006)
	if deliveredAt != nil {
		t.Fatal("discarded row was delivered")
	}
}

// TestChannelRepository_DiscardEmailReply pins the discard state machine:
// pending rows discard once, delivered rows never, repeats are no-ops.
func TestChannelRepository_DiscardEmailReply(t *testing.T) {
	db := createCommentTestDB(t)
	env := setupEmailReplyTestEnv(t, db)
	insertTestComment(t, db, 9007, env.ItemID, env.UserID)
	insertTestComment(t, db, 9008, env.ItemID, env.UserID)
	insertPendingReply(t, db, env, 9007, 0, 0)
	insertPendingReply(t, db, env, 9008, 0, 0)
	if _, err := db.Exec(`UPDATE email_reply_outbox SET delivered_at = CURRENT_TIMESTAMP WHERE comment_id = 9008`); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}

	repo := repository.NewChannelRepository(db)
	ctx := t.Context()

	discarded, err := repo.DiscardEmailReply(ctx, int(env.ChannelID), 9007)
	if err != nil || discarded != repository.EmailReplyDiscarded {
		t.Fatalf("pending discard = (%v, %v), want (Discarded, nil)", discarded, err)
	}
	_, _, _, discardedAt, _ := outboxRowState(t, db, 9007)
	if discardedAt == nil {
		t.Fatal("pending row not marked discarded")
	}

	if again, err := repo.DiscardEmailReply(ctx, int(env.ChannelID), 9007); err != nil || again != repository.EmailReplyNotDiscardable {
		t.Fatalf("repeat discard = (%v, %v), want (NotDiscardable, nil)", again, err)
	}

	if deliveredRow, err := repo.DiscardEmailReply(ctx, int(env.ChannelID), 9008); err != nil || deliveredRow != repository.EmailReplyNotDiscardable {
		t.Fatalf("delivered discard = (%v, %v), want (NotDiscardable, nil)", deliveredRow, err)
	}
}

// TestChannelRepository_DiscardPendingEmailReplies pins the bulk cancel: every
// pending row on the channel (replies and intake receipts alike) is discarded
// in one statement, while delivered, in-flight, and other-channel rows keep
// their state.
func TestChannelRepository_DiscardPendingEmailReplies(t *testing.T) {
	db := createCommentTestDB(t)
	env := setupEmailReplyTestEnv(t, db)
	for _, commentID := range []int{9101, 9102, 9103, 9104} {
		insertTestComment(t, db, commentID, env.ItemID, env.UserID)
		insertPendingReply(t, db, env, commentID, 0, 0)
	}
	receiptID := insertPendingReceipt(t, db, env)

	// A delivered row and a leased (in-flight) row must survive the bulk cancel.
	if _, err := db.Exec(`UPDATE email_reply_outbox SET delivered_at = CURRENT_TIMESTAMP WHERE comment_id = 9103`); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	if _, err := db.Exec(`UPDATE email_reply_outbox SET lease_owner = 'worker-a', next_attempt_at = ? WHERE comment_id = 9104`, time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatalf("lease in-flight reply: %v", err)
	}

	// A pending row owned by another channel is not the target's to cancel.
	var otherChannelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status)
		VALUES ('Other outbox', 'email', 'inbound', 'enabled')
		RETURNING id
	`).Scan(&otherChannelID); err != nil {
		t.Fatalf("seed other channel: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO email_reply_outbox (
			comment_id, channel_id, item_id, to_email, subject, html_body, text_body, message_id, from_email
		) VALUES (9101, ?, ?, 'other@example.com', 'Re: Other', 'body', 'body', '<other@test>', 'noreply@test.com')
	`, otherChannelID, env.ItemID); err != nil {
		t.Fatalf("seed other-channel reply: %v", err)
	}

	repo := repository.NewChannelRepository(db)
	discarded, err := repo.DiscardPendingEmailReplies(t.Context(), int(env.ChannelID))
	if err != nil {
		t.Fatalf("DiscardPendingEmailReplies: %v", err)
	}
	if discarded != 3 {
		t.Fatalf("discarded = %d, want 3", discarded)
	}

	for _, commentID := range []int{9101, 9102} {
		if _, _, _, discardedAt, _ := outboxRowState(t, db, commentID); discardedAt == nil {
			t.Fatalf("comment %d not discarded", commentID)
		}
	}
	var receiptDiscarded *time.Time
	if err := db.QueryRow(`SELECT discarded_at FROM email_reply_outbox WHERE id = ?`, receiptID).Scan(&receiptDiscarded); err != nil || receiptDiscarded == nil {
		t.Fatalf("receipt discarded_at = %v (err %v), want set", receiptDiscarded, err)
	}

	_, _, deliveredAt, deliveredDiscarded, _ := outboxRowState(t, db, 9103)
	if deliveredAt == nil || deliveredDiscarded != nil {
		t.Fatalf("delivered row = delivered %v discarded %v, want delivered and not discarded", deliveredAt, deliveredDiscarded)
	}
	var leaseOwner *string
	var inFlightDiscarded *time.Time
	if err := db.QueryRow(`SELECT lease_owner, discarded_at FROM email_reply_outbox WHERE comment_id = 9104`).Scan(&leaseOwner, &inFlightDiscarded); err != nil {
		t.Fatalf("load in-flight row: %v", err)
	}
	if leaseOwner == nil || *leaseOwner != "worker-a" || inFlightDiscarded != nil {
		t.Fatalf("in-flight row = lease %v discarded %v, want worker-a lease and not discarded", leaseOwner, inFlightDiscarded)
	}

	var otherDiscarded *time.Time
	if err := db.QueryRow(`SELECT discarded_at FROM email_reply_outbox WHERE channel_id = ?`, otherChannelID).Scan(&otherDiscarded); err != nil || otherDiscarded != nil {
		t.Fatalf("other-channel discarded_at = %v (err %v), want NULL", otherDiscarded, err)
	}

	if again, err := repo.DiscardPendingEmailReplies(t.Context(), int(env.ChannelID)); err != nil || again != 0 {
		t.Fatalf("repeat discard = (%d, %v), want (0, nil)", again, err)
	}
}

// insertPendingReceipt seeds an intake_confirmation outbox row. Receipts carry
// no comment_id, so the outbox row id is their only handle. The row is inserted
// directly because only the inbound-mail pipeline enqueues receipts.
func insertPendingReceipt(t *testing.T, db database.Database, env emailReplyTestEnv) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`
		INSERT INTO email_reply_outbox (
			kind, comment_id, channel_id, item_id, to_email, to_name, subject,
			html_body, text_body, message_id, in_reply_to, references_json,
			from_email, from_name, attempt_count, next_attempt_at, created_at, updated_at
		) VALUES ('intake_confirmation', NULL, ?, ?, 'customer@example.com', 'Customer One', 'We received your request',
			'<p>body</p>', 'body', ?, '', '[]',
			'noreply@test.com', 'Test App', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`, env.ChannelID, env.ItemID, fmt.Sprintf("<ws-confirm-%d@test.com>", env.ItemID)).Scan(&id); err != nil {
		t.Fatalf("seed intake receipt: %v", err)
	}
	return id
}

// TestChannelRepository_DiscardEmailReplyByID pins the row-scoped discard: an
// intake receipt (no comment_id) cancels once, never across channels, and the
// delivered and in-flight states keep their exact refusals.
func TestChannelRepository_DiscardEmailReplyByID(t *testing.T) {
	db := createCommentTestDB(t)
	env := setupEmailReplyTestEnv(t, db)
	repo := repository.NewChannelRepository(db)
	ctx := t.Context()

	receiptID := insertPendingReceipt(t, db, env)

	if outcome, err := repo.DiscardEmailReplyByID(ctx, int(env.ChannelID), receiptID); err != nil || outcome != repository.EmailReplyDiscarded {
		t.Fatalf("pending receipt discard = (%v, %v), want (Discarded, nil)", outcome, err)
	}
	var discardedAt *time.Time
	if err := db.QueryRow(`SELECT discarded_at FROM email_reply_outbox WHERE id = ?`, receiptID).Scan(&discardedAt); err != nil || discardedAt == nil {
		t.Fatalf("receipt discarded_at = %v (err %v), want set", discardedAt, err)
	}

	if again, err := repo.DiscardEmailReplyByID(ctx, int(env.ChannelID), receiptID); err != nil || again != repository.EmailReplyNotDiscardable {
		t.Fatalf("repeat receipt discard = (%v, %v), want (NotDiscardable, nil)", again, err)
	}

	// A channel that does not own the row must not be able to cancel it.
	if wrong, err := repo.DiscardEmailReplyByID(ctx, int(env.ChannelID)+999, receiptID); err != nil || wrong != repository.EmailReplyNotDiscardable {
		t.Fatalf("cross-channel receipt discard = (%v, %v), want (NotDiscardable, nil)", wrong, err)
	}

	if _, err := db.Exec(`UPDATE email_reply_outbox SET discarded_at = NULL, delivered_at = CURRENT_TIMESTAMP WHERE id = ?`, receiptID); err != nil {
		t.Fatalf("mark receipt delivered: %v", err)
	}
	if delivered, err := repo.DiscardEmailReplyByID(ctx, int(env.ChannelID), receiptID); err != nil || delivered != repository.EmailReplyNotDiscardable {
		t.Fatalf("delivered receipt discard = (%v, %v), want (NotDiscardable, nil)", delivered, err)
	}

	// A live lease reports Sending instead of a false cancellation.
	if _, err := db.Exec(`UPDATE email_reply_outbox SET delivered_at = NULL, lease_owner = 'worker-a', next_attempt_at = ? WHERE id = ?`, time.Now().Add(time.Hour).UTC(), receiptID); err != nil {
		t.Fatalf("lease receipt: %v", err)
	}
	if sending, err := repo.DiscardEmailReplyByID(ctx, int(env.ChannelID), receiptID); err != nil || sending != repository.EmailReplySending {
		t.Fatalf("in-flight receipt discard = (%v, %v), want (Sending, nil)", sending, err)
	}
}
