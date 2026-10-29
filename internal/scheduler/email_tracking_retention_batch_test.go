//go:build test

package scheduler_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"windshift/internal/scheduler"
)

func TestRetentionSweeperDeletesLargeBacklogInBatchesAndKeepsAnchors(t *testing.T) {
	db := newRetentionSweeperDB(t)
	ctx := context.Background()

	var channelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Bulk retention', 'email', 'inbound', 'enabled', '{"email_tracking_retention_days": 1}')
		RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	const oldRows = 2500
	old := time.Now().Add(-10 * 24 * time.Hour)
	recent := time.Now()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin seed transaction: %v", err)
	}
	for i := 0; i < oldRows; i++ {
		if _, err := tx.Exec(`
			INSERT INTO email_message_tracking
				(channel_id, message_id, dedup_key, from_email, direction, processed_at)
			VALUES (?, ?, ?, 'someone@example.com', 'inbound', ?)
		`, channelID, fmt.Sprintf("<old-%d@example.com>", i), fmt.Sprintf("old-%d", i), old); err != nil {
			_ = tx.Rollback()
			t.Fatalf("seed old row %d: %v", i, err)
		}
	}
	// A recent reply keeps its old parent as a thread anchor.
	if _, err := tx.Exec(`
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, in_reply_to, from_email, direction, processed_at)
		VALUES (?, '<anchor@example.com>', 'anchor', '', 'someone@example.com', 'inbound', ?)
	`, channelID, old); err != nil {
		_ = tx.Rollback()
		t.Fatalf("seed anchor: %v", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, in_reply_to, from_email, direction, processed_at)
		VALUES (?, '<reply@example.com>', 'reply', '<anchor@example.com>', 'someone@example.com', 'inbound', ?)
	`, channelID, recent); err != nil {
		_ = tx.Rollback()
		t.Fatalf("seed reply: %v", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, from_email, direction, processed_at)
		VALUES (?, '<recent@example.com>', 'recent', 'someone@example.com', 'inbound', ?)
	`, channelID, recent); err != nil {
		_ = tx.Rollback()
		t.Fatalf("seed recent row: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed transaction: %v", err)
	}

	sweeper := scheduler.NewEmailTrackingRetentionSweeper(db)
	deleted, err := sweeper.SweepNow(ctx)
	if err != nil {
		t.Fatalf("SweepNow: %v", err)
	}
	if deleted != oldRows {
		t.Fatalf("deleted rows = %d, want %d old rows", deleted, oldRows)
	}

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM email_message_tracking WHERE channel_id = ?`, channelID).Scan(&remaining); err != nil {
		t.Fatalf("count remaining rows: %v", err)
	}
	if remaining != 3 {
		t.Fatalf("remaining rows = %d, want anchor, reply, and recent row", remaining)
	}
	var anchorCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM email_message_tracking
		WHERE channel_id = ? AND message_id = '<anchor@example.com>'
	`, channelID).Scan(&anchorCount); err != nil {
		t.Fatalf("count anchor: %v", err)
	}
	if anchorCount != 1 {
		t.Fatalf("anchor rows = %d, want 1", anchorCount)
	}
}
