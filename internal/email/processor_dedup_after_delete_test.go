//go:build test

package email_test

import (
	"context"
	"testing"
	"time"

	"windshift/internal/email"
	"windshift/internal/services"
)

// Deleting an item created from inbound email must not reopen the message for
// reprocessing. The tracking row's item_id is nulled by ON DELETE SET NULL, but
// completed_at preserves the dedup contract until the channel is deleted and
// cascades the row.
func TestProcessor_DeletedItemIsNotRecreatedFromEmail(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	ctx := context.Background()
	message := newTestEmail("<delete-me@example.com>", "Delete me", "body")

	first, err := env.processor.ProcessEmail(ctx, message, env.channelID, 0, env.config)
	if err != nil {
		t.Fatalf("first process: %v", err)
	}
	if first.Action != email.ActionItemCreated || first.ItemID == nil {
		t.Fatalf("first action = %s (item %v), want %s with an item", first.Action, first.ItemID, email.ActionItemCreated)
	}

	// Delete through the production path so the real FK SET NULL behavior runs.
	if _, err := services.NewItemCRUDService(env.db).Delete(*first.ItemID); err != nil {
		t.Fatalf("delete item %d: %v", *first.ItemID, err)
	}

	// The item FK nulls the tracking row's item_id. Assert that trigger
	// condition so a change in cascade behavior cannot make this pass vacuously.
	var itemID *int
	if err := env.db.QueryRow(`
		SELECT item_id FROM email_message_tracking
		WHERE channel_id = ? AND dedup_key = ?
	`, env.channelID, "delete-me@example.com").Scan(&itemID); err != nil {
		t.Fatalf("load tracking row: %v", err)
	}
	if itemID != nil {
		t.Fatalf("tracking item_id = %d, want NULL after item deletion", *itemID)
	}

	// Age the row past the stale-claim window. Without completed_at the row
	// would now look like an abandoned claim and be reclaimed into a new item.
	if _, err := env.db.ExecWrite(`
		UPDATE email_message_tracking SET processed_at = ?
		WHERE channel_id = ? AND dedup_key = ?
	`, time.Now().Add(-time.Hour), env.channelID, "delete-me@example.com"); err != nil {
		t.Fatalf("age tracking row: %v", err)
	}

	again, err := env.processor.ProcessEmail(ctx, message, env.channelID, 0, env.config)
	if err != nil {
		t.Fatalf("reprocess: %v", err)
	}
	if again.Action != email.ActionAlreadyExists {
		t.Fatalf("reprocess action = %s, want %s", again.Action, email.ActionAlreadyExists)
	}
	if again.ItemID != nil {
		t.Fatalf("reprocess created item %d, want none", *again.ItemID)
	}

	var itemCount int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM items WHERE workspace_id = ?`, env.workspaceID).Scan(&itemCount); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if itemCount != 0 {
		t.Fatalf("item count = %d, want 0 after delete + reprocess", itemCount)
	}
}

// Rows created before completed_at existed have no tombstone. Deleting the item
// must stamp completed_at before the FK nulls item_id, otherwise the row looks
// like an abandoned preclaim and the message recreates the ticket on the next
// refetch. This is the production upgrade path, not a fresh-install one.
func TestProcessor_LegacyRowWithoutCompletedAt_DeleteStampsTombstone(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	ctx := context.Background()
	message := newTestEmail("<legacy-delete@example.com>", "Legacy delete", "body")

	first, err := env.processor.ProcessEmail(ctx, message, env.channelID, 0, env.config)
	if err != nil {
		t.Fatalf("first process: %v", err)
	}
	if first.Action != email.ActionItemCreated || first.ItemID == nil {
		t.Fatalf("first action = %s (item %v), want %s with an item", first.Action, first.ItemID, email.ActionItemCreated)
	}

	// Simulate a row finalized before the completed_at column existed.
	if _, err := env.db.ExecWrite(`
		UPDATE email_message_tracking SET completed_at = NULL
		WHERE channel_id = ? AND dedup_key = ?
	`, env.channelID, "legacy-delete@example.com"); err != nil {
		t.Fatalf("clear completed_at: %v", err)
	}

	if _, err := services.NewItemCRUDService(env.db).Delete(*first.ItemID); err != nil {
		t.Fatalf("delete item %d: %v", *first.ItemID, err)
	}

	// Deleting the item must have stamped the tombstone before the FK nulled
	// the reference, so the row no longer looks reclaimable.
	var completedAt *time.Time
	if err := env.db.QueryRow(`
		SELECT completed_at FROM email_message_tracking
		WHERE channel_id = ? AND dedup_key = ?
	`, env.channelID, "legacy-delete@example.com").Scan(&completedAt); err != nil {
		t.Fatalf("load tracking row: %v", err)
	}
	if completedAt == nil {
		t.Fatal("completed_at = NULL after item deletion, want a tombstone")
	}

	if _, err := env.db.ExecWrite(`
		UPDATE email_message_tracking SET processed_at = ?
		WHERE channel_id = ? AND dedup_key = ?
	`, time.Now().Add(-time.Hour), env.channelID, "legacy-delete@example.com"); err != nil {
		t.Fatalf("age tracking row: %v", err)
	}

	again, err := env.processor.ProcessEmail(ctx, message, env.channelID, 0, env.config)
	if err != nil {
		t.Fatalf("reprocess: %v", err)
	}
	if again.Action != email.ActionAlreadyExists {
		t.Fatalf("reprocess action = %s, want %s", again.Action, email.ActionAlreadyExists)
	}

	var itemCount int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM items WHERE workspace_id = ?`, env.workspaceID).Scan(&itemCount); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if itemCount != 0 {
		t.Fatalf("item count = %d, want 0 after delete + reprocess", itemCount)
	}
}

// A reply email stores its created comment on the tracking row. Deleting the
// item cascades the comment and nulls comment_id, so the delete must stamp that
// row through the comment's item too, or the reply recreates a comment.
func TestProcessor_DeletedItemStampsReplyTrackingRow(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	ctx := context.Background()

	root, err := env.processor.ProcessEmail(ctx, newTestEmail("<reply-root@example.com>", "Root", "body"), env.channelID, 0, env.config)
	if err != nil {
		t.Fatalf("process root: %v", err)
	}
	if root.ItemID == nil {
		t.Fatalf("root action = %s, want an item", root.Action)
	}

	reply, err := env.processor.ProcessEmail(ctx, newTestReplyEmail("<reply-1@example.com>", "<reply-root@example.com>", "reply body"), env.channelID, 0, env.config)
	if err != nil {
		t.Fatalf("process reply: %v", err)
	}
	if reply.Action != email.ActionCommentAdded || reply.CommentID == nil {
		t.Fatalf("reply action = %s (comment %v), want %s with a comment", reply.Action, reply.CommentID, email.ActionCommentAdded)
	}

	if _, err := services.NewItemCRUDService(env.db).Delete(*root.ItemID); err != nil {
		t.Fatalf("delete item %d: %v", *root.ItemID, err)
	}

	var commentID *int
	var completedAt *time.Time
	if err := env.db.QueryRow(`
		SELECT comment_id, completed_at FROM email_message_tracking
		WHERE channel_id = ? AND dedup_key = ?
	`, env.channelID, "reply-1@example.com").Scan(&commentID, &completedAt); err != nil {
		t.Fatalf("load reply tracking row: %v", err)
	}
	if commentID != nil {
		t.Fatalf("reply comment_id = %d, want NULL after item deletion", *commentID)
	}
	if completedAt == nil {
		t.Fatal("reply completed_at = NULL after item deletion, want a tombstone")
	}
}

// Deleting the channel cascades the tracking rows, so a recreated mailbox may
// process the same message again. This pins the "channel removal is the only
// reset" half of the contract.
func TestProcessor_ChannelDeletionClearsEmailDedup(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	ctx := context.Background()
	message := newTestEmail("<channel-reset@example.com>", "Channel reset", "body")

	first, err := env.processor.ProcessEmail(ctx, message, env.channelID, 0, env.config)
	if err != nil {
		t.Fatalf("first process: %v", err)
	}
	if first.Action != email.ActionItemCreated {
		t.Fatalf("first action = %s, want %s", first.Action, email.ActionItemCreated)
	}

	if _, err := env.db.ExecWrite(`DELETE FROM channels WHERE id = ?`, env.channelID); err != nil {
		t.Fatalf("delete channel: %v", err)
	}

	var remaining int
	if err := env.db.QueryRow(`
		SELECT COUNT(*) FROM email_message_tracking WHERE channel_id = ?
	`, env.channelID).Scan(&remaining); err != nil {
		t.Fatalf("count tracking rows: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("tracking rows after channel deletion = %d, want 0", remaining)
	}
}
