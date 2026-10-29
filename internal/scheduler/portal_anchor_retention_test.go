//go:build test

package scheduler_test

import (
	"context"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/emailutil"
	"windshift/internal/scheduler"
	"windshift/internal/testutils"
)

func newRetentionSweeperDB(t *testing.T) database.Database {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	return tdb.GetDatabase()
}

func seedSweptChannel(t *testing.T, db database.Database, name, channelType string) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status)
		VALUES (?, ?, 'inbound', 'enabled') RETURNING id
	`, name, channelType).Scan(&id); err != nil {
		t.Fatalf("seed channel %s: %v", name, err)
	}
	return id
}

type sweepSeed struct {
	messageID string
	direction string
	commentID *int
	ageDays   int
}

func insertSweepRow(t *testing.T, db database.Database, channelID, itemID int, seed sweepSeed) {
	t.Helper()
	var commentID any
	if seed.commentID != nil {
		// Satisfy the comments FK like the production reply path would.
		if _, err := db.ExecWrite(`
			INSERT INTO comments (id, item_id, content)
			VALUES (?, ?, 'agent reply')
			ON CONFLICT(id) DO NOTHING
		`, *seed.commentID, itemID); err != nil {
			t.Fatalf("seed comment %d: %v", *seed.commentID, err)
		}
		commentID = *seed.commentID
	}
	processedAt := time.Now().Add(-time.Duration(seed.ageDays) * 24 * time.Hour)
	if _, err := db.ExecWrite(`
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, from_email, item_id, comment_id, direction, processed_at)
		VALUES (?, ?, ?, 'someone@example.com', NULL, ?, ?, ?)
	`, channelID, seed.messageID, seed.messageID, commentID, seed.direction, processedAt); err != nil {
		t.Fatalf("insert sweep row %s: %v", seed.messageID, err)
	}
}

func survivingRowIDs(t *testing.T, db database.Database, channelID int) map[int]bool {
	t.Helper()
	rows, err := db.Query(`SELECT item_id FROM email_message_tracking WHERE channel_id = ?`, channelID)
	if err != nil {
		t.Fatalf("query surviving rows: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int]bool{}
	for rows.Next() {
		var itemID *int
		if err := rows.Scan(&itemID); err != nil {
			t.Fatalf("scan surviving row: %v", err)
		}
		if itemID != nil {
			out[*itemID] = true
		}
	}
	return out
}

// TestRetentionSweeperSparesPortalAnchors verifies the WI-1546 retention
// rules: the sweeper trims portal channels (instance-default retention), and
// a portal ticket's synthetic anchor — an outbound row with no comment — is
// never swept even when old and unreferenced, so an old open ticket keeps its
// thread root. Old comment-bound outbound records are swept normally.
func TestRetentionSweeperSparesPortalAnchors(t *testing.T) {
	db := newRetentionSweeperDB(t)
	ctx := context.Background()

	portalChannelID := seedSweptChannel(t, db, "Portal", "portal")
	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Retention', 'RET') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	var itemID int
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, frac_index)
		VALUES (?, 990, 'Old portal ticket', '0.5') RETURNING id
	`, workspaceID).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	anchorID := emailutil.AnchorMessageID(int64(itemID), "windshift.local")
	// The anchor: old, unreferenced, outbound, no comment.
	insertSweepRow(t, db, portalChannelID, itemID, sweepSeed{
		messageID: anchorID, direction: "outbound", ageDays: 400,
	})
	// An old outbound notification record (comment-bound): swept by age.
	oldNotice := sweepSeed{
		messageID: "<ws-comment-901@windshift.local>",
		direction: "outbound", commentID: intPtr(901), ageDays: 400,
	}
	insertSweepRow(t, db, portalChannelID, itemID, oldNotice)
	// A recent outbound notification record: kept by age.
	insertSweepRow(t, db, portalChannelID, itemID, sweepSeed{
		messageID: "<ws-comment-902@windshift.local>",
		direction: "outbound", commentID: intPtr(902), ageDays: 1,
	})

	// An old email-channel anchor-like row (comment-bound inbound) is swept
	// by age as before — portal-anchor protection does not leak to email
	// channels.
	emailChannelID := seedSweptChannel(t, db, "Mailbox", "email")
	insertSweepRow(t, db, emailChannelID, itemID, sweepSeed{
		messageID: "<old-inbound@example.com>", direction: "inbound",
		commentID: intPtr(903), ageDays: 400,
	})

	sweeper := scheduler.NewEmailTrackingRetentionSweeper(db)
	if _, err := sweeper.SweepNow(ctx); err != nil {
		t.Fatalf("SweepNow: %v", err)
	}

	// The portal channel keeps the anchor and the recent notice; the old
	// comment-bound notice and the email-channel row were swept.
	var messageID string
	if err := db.QueryRow(`
		SELECT message_id FROM email_message_tracking
		WHERE channel_id = ? AND message_id = ?
	`, portalChannelID, "<ws-comment-901@windshift.local>").Scan(&messageID); err == nil {
		t.Fatal("old comment-bound portal notice survived the sweep")
	}
	if err := db.QueryRow(`
		SELECT message_id FROM email_message_tracking WHERE channel_id = ?
	`, emailChannelID).Scan(&messageID); err == nil {
		t.Fatal("old email-channel row survived the sweep")
	}
}

// TestRetentionSweeperKeepsDeletedItemTombstones verifies that a completed row
// whose item was deleted (completed_at set, item_id/comment_id NULL) survives
// the sweep. It is the only record preventing the message from being recreated
// if the mailbox is refetched, so retention must not remove it. Live completed
// rows are still trimmed by age.
func TestRetentionSweeperKeepsDeletedItemTombstones(t *testing.T) {
	db := newRetentionSweeperDB(t)
	ctx := context.Background()
	channelID := seedSweptChannel(t, db, "Mailbox", "email")
	var workspaceID, itemID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Tombstone', 'TMB') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, frac_index)
		VALUES (?, 991, 'Live item', '0.5') RETURNING id
	`, workspaceID).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	old := time.Now().Add(-400 * 24 * time.Hour)

	// Deleted-item tombstone: completed, no item/comment reference.
	if _, err := db.ExecWrite(`
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, from_email, item_id, comment_id, direction, completed_at, processed_at)
		VALUES (?, '<deleted-ticket@example.com>', '<deleted-ticket@example.com>', 'someone@example.com', NULL, NULL, 'inbound', ?, ?)
	`, channelID, old, old); err != nil {
		t.Fatalf("insert tombstone: %v", err)
	}
	// Live completed row: still swept by age.
	if _, err := db.ExecWrite(`
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, from_email, item_id, comment_id, direction, completed_at, processed_at)
		VALUES (?, '<live-ticket@example.com>', '<live-ticket@example.com>', 'someone@example.com', ?, NULL, 'inbound', ?, ?)
	`, channelID, itemID, old, old); err != nil {
		t.Fatalf("insert live row: %v", err)
	}

	sweeper := scheduler.NewEmailTrackingRetentionSweeper(db)
	if _, err := sweeper.SweepNow(ctx); err != nil {
		t.Fatalf("SweepNow: %v", err)
	}

	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM email_message_tracking
		WHERE channel_id = ? AND dedup_key = '<deleted-ticket@example.com>'
	`, channelID).Scan(&count); err != nil {
		t.Fatalf("count tombstone: %v", err)
	}
	if count != 1 {
		t.Fatalf("deleted-item tombstone count = %d, want 1", count)
	}
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM email_message_tracking
		WHERE channel_id = ? AND dedup_key = '<live-ticket@example.com>'
	`, channelID).Scan(&count); err != nil {
		t.Fatalf("count live row: %v", err)
	}
	if count != 0 {
		t.Fatalf("live completed row count = %d, want 0", count)
	}
}

func intPtr(v int) *int { return &v }
