package email

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"windshift/internal/blobstore"
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
	processor := NewProcessor(db, blobstore.Disabled())
	message := &ParsedEmail{
		MessageID: "<claim@example.com>",
		From:      EmailAddress{Address: "sender@example.com"},
		Subject:   "Claim",
	}
	ctx := context.Background()

	claim, err := processor.preclaimTracking(ctx, message, channelID, nil, message.MessageID, 0)
	if err != nil || claim != trackingClaimAcquired {
		t.Fatalf("first preclaim = (%v, %v), want (acquired, nil)", claim, err)
	}
	claim, err = processor.preclaimTracking(ctx, message, channelID, nil, message.MessageID, 0)
	if err != nil || claim != trackingClaimInProgress {
		t.Fatalf("fresh duplicate preclaim = (%v, %v), want (in progress, nil)", claim, err)
	}

	if _, err := db.ExecWrite(`
		UPDATE email_message_tracking SET processed_at = ?
		WHERE channel_id = ? AND dedup_key = ?
	`, time.Now().Add(-trackingClaimStaleAfter-time.Minute), channelID, message.MessageID); err != nil {
		t.Fatalf("age incomplete claim: %v", err)
	}
	claim, err = processor.preclaimTracking(ctx, message, channelID, nil, message.MessageID, 0)
	if err != nil || claim != trackingClaimAcquired {
		t.Fatalf("stale incomplete preclaim = (%v, %v), want (acquired, nil)", claim, err)
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
	claim, err = processor.preclaimTracking(ctx, message, channelID, nil, message.MessageID, 0)
	if err != nil || claim != trackingClaimCompleted {
		t.Fatalf("completed preclaim = (%v, %v), want (completed, nil)", claim, err)
	}

	// Deleting the created item nulls item_id via ON DELETE SET NULL. The
	// completed_at marker must keep the row deduplicated so the email is not
	// reprocessed into a fresh item.
	if _, err := db.ExecWrite(`
		UPDATE email_message_tracking SET item_id = NULL, completed_at = CURRENT_TIMESTAMP
		WHERE channel_id = ? AND dedup_key = ?
	`, channelID, message.MessageID); err != nil {
		t.Fatalf("simulate item deletion: %v", err)
	}
	claim, err = processor.preclaimTracking(ctx, message, channelID, nil, message.MessageID, 0)
	if err != nil || claim != trackingClaimCompleted {
		t.Fatalf("preclaim after item deletion = (%v, %v), want (completed, nil)", claim, err)
	}
}

// TestSenderThreadParticipationIsItemScoped pins the WI-1546 contract: the
// thread belongs to the item, not to a channel, so a prior participant on the
// item's thread authorizes regardless of which channel their messages used,
// while a participant on some other item's thread does not.
func TestSenderThreadParticipationIsItemScoped(t *testing.T) {
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

	var workspaceID, itemID, otherItemID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Thread', 'THR') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, channel_id, frac_index)
		VALUES (?, 1, 'Thread item', ?, ?) RETURNING id
	`, workspaceID, firstChannelID, testutils.NextTestFracIndex()).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, channel_id, frac_index)
		VALUES (?, 2, 'Other item', ?, ?) RETURNING id
	`, workspaceID, secondChannelID, testutils.NextTestFracIndex()).Scan(&otherItemID); err != nil {
		t.Fatalf("insert other item: %v", err)
	}
	// Sender last participated on a different email channel than the item's.
	if _, err := db.ExecWrite(`
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, from_email, item_id)
		VALUES (?, '<other-channel@example.com>', '<other-channel@example.com>', 'sender@example.com', ?)
	`, secondChannelID, itemID); err != nil {
		t.Fatalf("insert cross-channel participant: %v", err)
	}

	processor := NewProcessor(db, blobstore.Disabled())
	if !processor.senderIsThreadParticipant(context.Background(), itemID, "sender@example.com") {
		t.Fatal("participant on the item's thread (other channel) was not authorized")
	}
	// Participation on a different item authorizes nothing on this one.
	if processor.senderIsThreadParticipant(context.Background(), otherItemID, "sender@example.com") {
		t.Fatal("participation on another item's thread authorized this item")
	}
	// Unknown senders stay rejected.
	if processor.senderIsThreadParticipant(context.Background(), itemID, "stranger@example.com") {
		t.Fatal("unknown sender was authorized")
	}
}

// TestSenderThreadParticipationAcceptsExternalParticipant proves WI-1136: an
// explicitly added external participant can reply by email to the item's
// thread, and only to that item.
func TestSenderThreadParticipationAcceptsExternalParticipant(t *testing.T) {
	db := newProcessorRegressionTestDB(t)
	var workspaceID, itemID, otherItemID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Participants', 'PRT') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, frac_index)
		VALUES (?, 1, 'Participant item', ?) RETURNING id
	`, workspaceID, testutils.NextTestFracIndex()).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, frac_index)
		VALUES (?, 2, 'Other item', ?) RETURNING id
	`, workspaceID, testutils.NextTestFracIndex()).Scan(&otherItemID); err != nil {
		t.Fatalf("insert other item: %v", err)
	}
	var customerID int
	if err := db.QueryRow(`
		INSERT INTO portal_customers (name, email, created_via)
		VALUES ('Participant', 'participant@example.com', 'agent') RETURNING id
	`).Scan(&customerID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	if _, err := db.ExecWrite(`INSERT INTO item_participants (item_id, portal_customer_id) VALUES (?, ?)`, itemID, customerID); err != nil {
		t.Fatalf("insert participant: %v", err)
	}

	processor := NewProcessor(db, blobstore.Disabled())
	if !processor.senderIsThreadParticipant(context.Background(), itemID, "PARTICIPANT@example.com") {
		t.Fatal("external participant was not authorized to reply")
	}
	if processor.senderIsThreadParticipant(context.Background(), otherItemID, "participant@example.com") {
		t.Fatal("participant on another item authorized this item")
	}
	if processor.senderIsThreadParticipant(context.Background(), itemID, "stranger@example.com") {
		t.Fatal("unknown sender was authorized")
	}
}

// TestSenderThreadParticipationRevokedByParticipantRemoval is the WI-1715
// regression: an accepted reply, then an explicit participant removal, must not
// leave the removed sender able to reply onto the original ticket through their
// old thread history. The creator and a re-added participant keep access.
func TestSenderThreadParticipationRevokedByParticipantRemoval(t *testing.T) {
	db := newProcessorRegressionTestDB(t)
	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Removal', 'RMV') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	var channelID int
	if err := db.QueryRow(`INSERT INTO channels (name, type, direction) VALUES ('Mailbox', 'email', 'inbound') RETURNING id`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	var customerID int
	if err := db.QueryRow(`INSERT INTO portal_customers (name, email, created_via) VALUES ('Participant', 'participant@example.com', 'agent') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	var itemID int
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, channel_id, frac_index)
		VALUES (?, 1, 'Removal item', ?, ?) RETURNING id
	`, workspaceID, channelID, testutils.NextTestFracIndex()).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	// The participant already replied once, so thread history would normally
	// authorize their next reply.
	if _, err := db.ExecWrite(`
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, from_email, item_id, direction)
		VALUES (?, '<participant@example.com>', '<participant@example.com>', 'participant@example.com', ?, 'inbound')
	`, channelID, itemID); err != nil {
		t.Fatalf("insert participant tracking: %v", err)
	}

	addParticipant := func() {
		t.Helper()
		if _, err := db.ExecWrite(`
			INSERT INTO item_participants (item_id, portal_customer_id)
			VALUES (?, ?)
			ON CONFLICT (item_id, portal_customer_id) DO NOTHING
		`, itemID, customerID); err != nil {
			t.Fatalf("add participant: %v", err)
		}
	}
	removeParticipant := func() {
		t.Helper()
		if _, err := db.ExecWrite(`DELETE FROM item_participants WHERE item_id = ? AND portal_customer_id = ?`, itemID, customerID); err != nil {
			t.Fatalf("remove participant: %v", err)
		}
		// Mirrors the tombstone ItemParticipantService.Remove writes.
		if _, err := db.ExecWrite(`
			INSERT INTO item_history (item_id, field_name, old_value, changed_at)
			VALUES (?, 'participant_removed', ?, CURRENT_TIMESTAMP)
		`, itemID, "participant@example.com"); err != nil {
			t.Fatalf("record removal history: %v", err)
		}
	}

	ctx := context.Background()
	addParticipant()
	processor := NewProcessor(db, blobstore.Disabled())
	if !processor.senderIsThreadParticipant(ctx, itemID, "participant@example.com") {
		t.Fatal("active participant was not authorized")
	}

	removeParticipant()
	if processor.senderIsThreadParticipant(ctx, itemID, "participant@example.com") {
		t.Fatal("removed participant retained thread access")
	}

	// Re-adding restores access.
	addParticipant()
	if !processor.senderIsThreadParticipant(ctx, itemID, "participant@example.com") {
		t.Fatal("re-added participant lost access")
	}

	// The creator is not a participant; a participant removal must not affect the
	// creator's own reply access.
	var creatorCustomerID int
	if err := db.QueryRow(`INSERT INTO portal_customers (name, email, created_via) VALUES ('Creator', 'creator@example.com', 'agent') RETURNING id`).Scan(&creatorCustomerID); err != nil {
		t.Fatalf("insert creator: %v", err)
	}
	if _, err := db.ExecWrite(`UPDATE items SET creator_portal_customer_id = ? WHERE id = ?`, creatorCustomerID, itemID); err != nil {
		t.Fatalf("set creator: %v", err)
	}
	if !processor.senderIsThreadParticipant(ctx, itemID, "creator@example.com") {
		t.Fatal("creator lost reply access")
	}
}

// TestFindParentItemRoutesPortalReplyAcrossChannels is the WI-1546 loop: a
// portal ticket's synthetic anchor was minted on the portal channel; the
// customer replies by email to the notification, so the message arrives on the
// email intake channel quoting the anchor. Matching must find the portal item,
// and the creator must be recognized without any prior tracked message.
func TestFindParentItemRoutesPortalReplyAcrossChannels(t *testing.T) {
	db := newProcessorRegressionTestDB(t)
	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Portal loop', 'PLP') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	var portalChannelID, emailChannelID int
	if err := db.QueryRow(`INSERT INTO channels (name, type, direction) VALUES ('Portal', 'portal', 'inbound') RETURNING id`).Scan(&portalChannelID); err != nil {
		t.Fatalf("insert portal channel: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO channels (name, type, direction) VALUES ('Mailbox', 'email', 'inbound') RETURNING id`).Scan(&emailChannelID); err != nil {
		t.Fatalf("insert email channel: %v", err)
	}
	var customerID int
	if err := db.QueryRow(`INSERT INTO portal_customers (name, email) VALUES ('Customer', 'customer@example.com') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	var itemID int
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, channel_id, creator_portal_customer_id, frac_index)
		VALUES (?, 1, 'Portal request', ?, ?, ?) RETURNING id
	`, workspaceID, portalChannelID, customerID, testutils.NextTestFracIndex()).Scan(&itemID); err != nil {
		t.Fatalf("insert portal item: %v", err)
	}
	// The synthetic thread anchor minted at submission (slice 1).
	anchorID := fmt.Sprintf("<ws-item-%d@windshift.local>", itemID)
	if _, err := db.ExecWrite(`
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, from_email, item_id, direction)
		VALUES (?, ?, ?, '', ?, 'outbound')
	`, portalChannelID, anchorID, anchorID, itemID); err != nil {
		t.Fatalf("insert anchor: %v", err)
	}

	processor := NewProcessor(db, blobstore.Disabled())
	parent := processor.findParentItem(context.Background(), &ParsedEmail{
		InReplyTo: anchorID,
		From:      EmailAddress{Address: "customer@example.com"},
	})
	if parent == nil || *parent != itemID {
		t.Fatalf("portal reply parent = %v, want %d", parent, itemID)
	}

	// A stranger quoting the leaked anchor must not land on the ticket.
	if strangerParent := processor.findParentItem(context.Background(), &ParsedEmail{
		InReplyTo: anchorID,
		From:      EmailAddress{Address: "stranger@example.com"},
	}); strangerParent != nil {
		t.Fatalf("stranger reply matched item %d; hijack guard failed", *strangerParent)
	}

	// A participant of the email conversation (an outbound notice recipient is
	// recorded under their from_email) may also append.
	if _, err := db.ExecWrite(`
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, from_email, item_id, direction)
		VALUES (?, '<notice@example.com>', '<notice@example.com>', 'customer@example.com', ?, 'outbound')
	`, emailChannelID, itemID); err != nil {
		t.Fatalf("insert participant row: %v", err)
	}
	if !processor.senderIsThreadParticipant(context.Background(), itemID, "customer@example.com") {
		t.Fatal("thread participant lost authorization after channel change")
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

	parent := NewProcessor(db, blobstore.Disabled()).findParentItem(context.Background(), &ParsedEmail{
		InReplyTo: "<legacy@example.com>",
		From:      EmailAddress{Address: "sender@example.com"},
	})
	if parent == nil || *parent != itemID {
		t.Fatalf("legacy bare Message-ID parent = %v, want %d", parent, itemID)
	}
}
