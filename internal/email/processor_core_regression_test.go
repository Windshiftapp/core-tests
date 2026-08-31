package email

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

func newProcessorRegressionTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "email-processor.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	if err := db.Initialize(); err != nil {
		_ = db.Close()
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestProcessEmailRejectsInvalidSenderBeforeClaim(t *testing.T) {
	processor := &Processor{}
	_, err := processor.ProcessEmail(context.Background(), &ParsedEmail{
		From: EmailAddress{Address: "not-an-address"},
	}, 1, 1, nil)
	if err == nil {
		t.Fatal("invalid sender unexpectedly reached processing")
	}
}

func TestProcessEmailRejectsMissingInputWithoutPanic(t *testing.T) {
	processor := &Processor{}
	if _, err := processor.ProcessEmail(context.Background(), nil, 1, 1, nil); err == nil {
		t.Fatal("nil email unexpectedly reached processing")
	}
	if _, err := processor.ProcessEmail(context.Background(), &ParsedEmail{
		From: EmailAddress{Address: "sender@example.com"},
	}, 1, 1, nil); err == nil {
		t.Fatal("nil channel config unexpectedly reached processing")
	}
}

func TestTrackingPreclaimRecoversOnlyStaleIncompleteClaims(t *testing.T) {
	db := newProcessorRegressionTestDB(t)
	var channelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction)
		VALUES ('Inbound', 'email', 'inbound') RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	processor := NewProcessor(db, "")
	message := &ParsedEmail{
		MessageID: "<claim@example.com>",
		From:      EmailAddress{Address: "sender@example.com"},
		Subject:   "Claim",
	}
	ctx := context.Background()

	claimed, err := processor.preclaimTracking(ctx, message, channelID, message.MessageID)
	if err != nil || !claimed {
		t.Fatalf("first preclaim = (%v, %v), want (true, nil)", claimed, err)
	}
	claimed, err = processor.preclaimTracking(ctx, message, channelID, message.MessageID)
	if err != nil || claimed {
		t.Fatalf("fresh duplicate preclaim = (%v, %v), want (false, nil)", claimed, err)
	}

	if _, err := db.ExecWrite(`
		UPDATE email_message_tracking SET processed_at = ?
		WHERE channel_id = ? AND dedup_key = ?
	`, time.Now().Add(-trackingClaimStaleAfter-time.Minute), channelID, message.MessageID); err != nil {
		t.Fatalf("age incomplete claim: %v", err)
	}
	claimed, err = processor.preclaimTracking(ctx, message, channelID, message.MessageID)
	if err != nil || !claimed {
		t.Fatalf("stale incomplete preclaim = (%v, %v), want (true, nil)", claimed, err)
	}

	var workspaceID, itemID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Test', 'TST') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, frac_index)
		VALUES (?, 1, 'Claimed item', ?) RETURNING id
	`, workspaceID, testutils.NextTestFracIndex()).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	if _, err := db.ExecWrite(`
		UPDATE email_message_tracking SET item_id = ?, processed_at = ?
		WHERE channel_id = ? AND dedup_key = ?
	`, itemID, time.Now().Add(-trackingClaimStaleAfter-time.Minute), channelID, message.MessageID); err != nil {
		t.Fatalf("complete claim: %v", err)
	}
	claimed, err = processor.preclaimTracking(ctx, message, channelID, message.MessageID)
	if err != nil || claimed {
		t.Fatalf("completed preclaim = (%v, %v), want (false, nil)", claimed, err)
	}
}

func TestSenderThreadParticipationIsScopedToEmailChannel(t *testing.T) {
	db := newProcessorRegressionTestDB(t)
	insertChannel := func(name string) int {
		t.Helper()
		var id int
		if err := db.QueryRow(`
			INSERT INTO channels (name, type, direction)
			VALUES (?, 'email', 'inbound') RETURNING id
		`, name).Scan(&id); err != nil {
			t.Fatalf("insert channel: %v", err)
		}
		return id
	}
	firstChannelID := insertChannel("First mailbox")
	secondChannelID := insertChannel("Second mailbox")

	var workspaceID, itemID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Thread', 'THR') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, channel_id, frac_index)
		VALUES (?, 1, 'Thread item', ?, ?) RETURNING id
	`, workspaceID, firstChannelID, testutils.NextTestFracIndex()).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, from_email, item_id)
		VALUES (?, '<other-channel@example.com>', '<other-channel@example.com>', 'sender@example.com', ?)
	`, secondChannelID, itemID); err != nil {
		t.Fatalf("insert other-channel participant: %v", err)
	}

	processor := NewProcessor(db, "")
	if processor.senderIsThreadParticipant(context.Background(), itemID, firstChannelID, "sender@example.com") {
		t.Fatal("participation in a different email channel authorized this thread")
	}
	if _, err := db.ExecWrite(`
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, from_email, item_id)
		VALUES (?, '<same-channel@example.com>', '<same-channel@example.com>', 'sender@example.com', ?)
	`, firstChannelID, itemID); err != nil {
		t.Fatalf("insert same-channel participant: %v", err)
	}
	if !processor.senderIsThreadParticipant(context.Background(), itemID, firstChannelID, "sender@example.com") {
		t.Fatal("same-channel participant was not authorized")
	}
}

func TestFindParentItemAcceptsLegacyBareMessageID(t *testing.T) {
	db := newProcessorRegressionTestDB(t)
	var workspaceID, channelID, itemID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Legacy thread', 'LTH') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO channels (name, type, direction) VALUES ('Legacy mailbox', 'email', 'inbound') RETURNING id`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, channel_id, frac_index)
		VALUES (?, 1, 'Legacy thread item', ?, ?) RETURNING id
	`, workspaceID, channelID, testutils.NextTestFracIndex()).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, from_email, item_id)
		VALUES (?, 'legacy@example.com', 'legacy@example.com', 'sender@example.com', ?)
	`, channelID, itemID); err != nil {
		t.Fatalf("insert legacy tracking: %v", err)
	}

	parent := NewProcessor(db, "").findParentItem(context.Background(), channelID, &ParsedEmail{
		InReplyTo: "<legacy@example.com>",
		From:      EmailAddress{Address: "sender@example.com"},
	})
	if parent == nil || *parent != itemID {
		t.Fatalf("legacy bare Message-ID parent = %v, want %d", parent, itemID)
	}
}
