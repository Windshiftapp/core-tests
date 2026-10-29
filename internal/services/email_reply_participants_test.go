//go:build test

package services_test

import (
	"testing"

	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// TestEmailReplyService_FansOutToParticipants verifies WI-1136: an outbound
// reply is enqueued and sent once per recipient — the creator plus every
// external request participant — each with its own Message-ID.
func TestEmailReplyService_FansOutToParticipants(t *testing.T) {
	db := createCommentTestDB(t)
	env := setupEmailReplyTestEnv(t, db)
	insertTrackingRecord(t, db, env.ChannelID, "<orig@example.com>", "Participant thread", env.ItemID)

	// Direct repository write is the documented exception for this unit: the
	// fan-out reads item_participants, and the HTTP add path is covered by the
	// participant HTTP tests.
	participantID := testutils.InsertID(t, db,
		`INSERT INTO portal_customers (name, email, created_via) VALUES ('Participant Two', 'participant@example.com', 'agent')`)
	if _, err := repository.NewItemParticipantRepository(db).Add(env.ItemID, participantID, nil); err != nil {
		t.Fatalf("add participant: %v", err)
	}

	insertTestComment(t, db, 500, env.ItemID, env.UserID)
	mock := &mockSMTPSender{configured: true}
	svc := services.NewEmailReplyService(db, mock)

	if err := svc.HandleCommentCreated(services.HandleCommentParams{
		CommentID: 500,
		ItemID:    env.ItemID,
		AuthorID:  env.UserID,
		Content:   "Update for everyone",
	}); err != nil {
		t.Fatalf("HandleCommentCreated: %v", err)
	}

	if len(mock.sent) != 2 {
		t.Fatalf("SMTP sends = %d, want 2 (creator + participant)", len(mock.sent))
	}
	messageIDs := map[string]bool{}
	toEmails := map[string]bool{}
	for _, sent := range mock.sent {
		toEmails[sent.ToEmail] = true
		if messageIDs[sent.MessageID] {
			t.Fatalf("duplicate Message-ID %q across recipients", sent.MessageID)
		}
		messageIDs[sent.MessageID] = true
	}
	if !toEmails["customer@example.com"] {
		t.Fatalf("creator missing from recipients: %v", toEmails)
	}
	if !toEmails["participant@example.com"] {
		t.Fatalf("participant missing from recipients: %v", toEmails)
	}

	// The outbox holds one durable row per recipient.
	var outboxCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM email_reply_outbox WHERE comment_id = 500`).Scan(&outboxCount); err != nil {
		t.Fatalf("count outbox rows: %v", err)
	}
	if outboxCount != 2 {
		t.Fatalf("outbox rows = %d, want 2", outboxCount)
	}

	// Replaying the same comment must not enqueue or send duplicates.
	if err := svc.HandleCommentCreated(services.HandleCommentParams{
		CommentID: 500,
		ItemID:    env.ItemID,
		AuthorID:  env.UserID,
		Content:   "Update for everyone",
	}); err != nil {
		t.Fatalf("HandleCommentCreated replay: %v", err)
	}
	if len(mock.sent) != 2 {
		t.Fatalf("SMTP sends after replay = %d, want still 2", len(mock.sent))
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM email_reply_outbox WHERE comment_id = 500`).Scan(&outboxCount); err != nil {
		t.Fatalf("count outbox rows after replay: %v", err)
	}
	if outboxCount != 2 {
		t.Fatalf("outbox rows after replay = %d, want still 2", outboxCount)
	}
}
