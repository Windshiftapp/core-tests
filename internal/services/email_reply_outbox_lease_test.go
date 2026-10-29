//go:build test

package services_test

import (
	"errors"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/repository"
	"windshift/internal/services"
)

// leaseOutboxRow simulates another instance's live delivery claim (WI-1572):
// next_attempt_at becomes the lease expiry and lease_owner names the holder.
func leaseOutboxRow(t *testing.T, db database.Database, commentID int, owner string, leaseFor time.Duration) {
	t.Helper()
	if _, err := db.Exec(`
		UPDATE email_reply_outbox
		SET lease_owner = ?, next_attempt_at = ?
		WHERE comment_id = ?
	`, owner, time.Now().Add(leaseFor).UTC(), commentID); err != nil {
		t.Fatalf("lease outbox row %d: %v", commentID, err)
	}
}

// TestEmailReplyService_RetryPendingReply_LiveLeaseConflicts proves a manual
// retry cannot steal another instance's live delivery lease: the retry
// reports a conflict, the row keeps its claim, and no duplicate mail goes out
// (WI-1572).
func TestEmailReplyService_RetryPendingReply_LiveLeaseConflicts(t *testing.T) {
	db := createCommentTestDB(t)
	env := setupEmailReplyTestEnv(t, db)
	insertTestComment(t, db, 9101, env.ItemID, env.UserID)
	insertPendingReply(t, db, env, 9101, 0, -time.Minute)
	leaseOutboxRow(t, db, 9101, "instance-b", 5*time.Minute)

	sender := &mockSMTPSender{configured: true}
	svc := services.NewEmailReplyService(db, sender)
	_, err := svc.RetryPendingReply(int(env.ChannelID), 9101)
	if !errors.Is(err, services.ErrEmailReplyInFlight) {
		t.Fatalf("retry err = %v, want ErrEmailReplyInFlight", err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("SMTP sends = %d, want 0 (the live lease must not be stolen)", len(sender.sent))
	}
	_, lastError, deliveredAt, discardedAt, _ := outboxRowState(t, db, 9101)
	if deliveredAt != nil || discardedAt != nil {
		t.Fatalf("row state changed on conflict: delivered=%v discarded=%v", deliveredAt, discardedAt)
	}
	if lastError != "" {
		t.Fatalf("conflict wrote last_error %q", lastError)
	}
}

// TestEmailReplyService_RetryPendingReply_AbandonedLeaseRecovers proves a
// retry succeeds once a foreign lease expires: genuinely abandoned work
// stays recoverable (WI-1572).
func TestEmailReplyService_RetryPendingReply_AbandonedLeaseRecovers(t *testing.T) {
	db := createCommentTestDB(t)
	env := setupEmailReplyTestEnv(t, db)
	insertTestComment(t, db, 9102, env.ItemID, env.UserID)
	insertPendingReply(t, db, env, 9102, 0, -time.Minute)
	leaseOutboxRow(t, db, 9102, "instance-b", -time.Minute) // expired lease

	sender := &mockSMTPSender{configured: true}
	svc := services.NewEmailReplyService(db, sender)
	delivered, err := svc.RetryPendingReply(int(env.ChannelID), 9102)
	if err != nil || !delivered {
		t.Fatalf("retry on expired lease = (%t, %v), want delivered", delivered, err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("SMTP sends = %d, want 1", len(sender.sent))
	}
	_, _, deliveredAt, _, _ := outboxRowState(t, db, 9102)
	if deliveredAt == nil {
		t.Fatal("row not marked delivered after recovering the abandoned lease")
	}
}

// TestChannelRepository_DiscardEmailReply_Outcomes pins the discard
// contract: a live delivery lease conflicts, a pending row discards, a
// delivered row refuses (WI-1573).
func TestChannelRepository_DiscardEmailReply_Outcomes(t *testing.T) {
	db := createCommentTestDB(t)
	env := setupEmailReplyTestEnv(t, db)
	repo := repository.NewChannelRepository(db)

	insertTestComment(t, db, 9103, env.ItemID, env.UserID)
	insertTestComment(t, db, 9104, env.ItemID, env.UserID)
	insertTestComment(t, db, 9105, env.ItemID, env.UserID)
	insertPendingReply(t, db, env, 9103, 0, -time.Minute) // pending
	insertPendingReply(t, db, env, 9104, 0, -time.Minute) // becomes in-flight
	insertPendingReply(t, db, env, 9105, 0, -time.Minute)
	if _, err := db.Exec(`UPDATE email_reply_outbox SET delivered_at = CURRENT_TIMESTAMP WHERE comment_id = 9105`); err != nil {
		t.Fatalf("mark row delivered: %v", err)
	}

	// In-flight: cancellation is no longer possible and is reported as such.
	leaseOutboxRow(t, db, 9104, "instance-b", 5*time.Minute)
	outcome, err := repo.DiscardEmailReply(t.Context(), int(env.ChannelID), 9104)
	if err != nil {
		t.Fatalf("discard in-flight: %v", err)
	}
	if outcome != repository.EmailReplySending {
		t.Fatalf("in-flight discard outcome = %d, want EmailReplySending", outcome)
	}
	_, _, deliveredAt, discardedAt, _ := outboxRowState(t, db, 9104)
	if discardedAt != nil || deliveredAt != nil {
		t.Fatalf("in-flight row mutated: delivered=%v discarded=%v", deliveredAt, discardedAt)
	}

	// Delivered rows refuse.
	outcome, err = repo.DiscardEmailReply(t.Context(), int(env.ChannelID), 9105)
	if err != nil || outcome != repository.EmailReplyNotDiscardable {
		t.Fatalf("delivered discard = (%d, %v), want NotDiscardable", outcome, err)
	}

	// A pending row discards.
	outcome, err = repo.DiscardEmailReply(t.Context(), int(env.ChannelID), 9103)
	if err != nil || outcome != repository.EmailReplyDiscarded {
		t.Fatalf("pending discard = (%d, %v), want Discarded", outcome, err)
	}
	if _, _, _, discardedAgain, _ := outboxRowState(t, db, 9103); discardedAgain == nil {
		t.Fatal("pending row not discarded")
	}

	// Once the in-flight lease expires, the row discards normally.
	leaseOutboxRow(t, db, 9104, "instance-b", -time.Minute)
	outcome, err = repo.DiscardEmailReply(t.Context(), int(env.ChannelID), 9104)
	if err != nil || outcome != repository.EmailReplyDiscarded {
		t.Fatalf("expired-lease discard = (%d, %v), want Discarded", outcome, err)
	}
}
