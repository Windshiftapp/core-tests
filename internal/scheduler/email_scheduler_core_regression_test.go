package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
)

func TestNextFailedMessageAttemptIsScopedToUIDAndValidity(t *testing.T) {
	state := &models.EmailChannelState{
		FailedMessageUID:         42,
		FailedMessageUIDValidity: 7,
		FailedMessageCount:       3,
		ErrorCount:               99, // Channel health must not affect poison retries.
	}

	uid, validity, count := nextFailedMessageAttempt(state, 42, 7)
	if uid != 42 || validity != 7 || count != 4 {
		t.Fatalf("same message attempt = (%d, %d, %d), want (42, 7, 4)", uid, validity, count)
	}

	_, _, count = nextFailedMessageAttempt(state, 43, 7)
	if count != 1 {
		t.Fatalf("different UID inherited %d attempts, want 1", count)
	}

	_, _, count = nextFailedMessageAttempt(state, 42, 8)
	if count != 1 {
		t.Fatalf("new UIDVALIDITY epoch inherited %d attempts, want 1", count)
	}
}

func TestEmailProcessingLeasePreventsOverlappingPolls(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "email-processing-lease.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	var channelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction)
		VALUES ('Inbound', 'email', 'inbound') RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	first := &EmailScheduler{db: db}
	second := &EmailScheduler{db: db}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	owner, acquired, err := first.acquireProcessingLease(ctx, channelID)
	if err != nil || !acquired {
		t.Fatalf("first acquire = (%q, %v, %v), want acquired", owner, acquired, err)
	}
	if _, acquired, err = second.acquireProcessingLease(ctx, channelID); err != nil || acquired {
		t.Fatalf("overlapping acquire = (%v, %v), want (false, nil)", acquired, err)
	}

	first.releaseProcessingLease(ctx, channelID, owner)
	owner, acquired, err = second.acquireProcessingLease(ctx, channelID)
	if err != nil || !acquired {
		t.Fatalf("acquire after release = (%q, %v, %v), want acquired", owner, acquired, err)
	}
	second.releaseProcessingLease(ctx, channelID, owner)

	if _, err := db.ExecWrite(`
		INSERT INTO email_processing_leases(channel_id, owner_token, expires_at)
		VALUES (?, 'crashed-worker', ?)
	`, channelID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("insert expired lease: %v", err)
	}
	owner, acquired, err = first.acquireProcessingLease(ctx, channelID)
	if err != nil || !acquired {
		t.Fatalf("reclaim expired lease = (%q, %v, %v), want acquired", owner, acquired, err)
	}
	first.releaseProcessingLease(ctx, channelID, owner)
}

func TestEmptyPollPersistsChangedUIDValidity(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "email-empty-poll.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	var channelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction)
		VALUES ('Inbound', 'email', 'inbound') RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO email_channel_state (
			channel_id, last_uid, uid_validity, error_count, last_error,
			failed_message_uid, failed_message_uid_validity, failed_message_count
		) VALUES (?, 987, 12, 4, 'old failure', 988, 12, 2)
	`, channelID); err != nil {
		t.Fatalf("insert state: %v", err)
	}

	scheduler := &EmailScheduler{db: db}
	scheduler.updateLastChecked(context.Background(), channelID, 13)

	var lastUID, errorCount, failedUID, failedCount int
	var validity uint32
	var lastError *string
	if err := db.QueryRow(`
		SELECT last_uid, uid_validity, error_count, last_error,
		       failed_message_uid, failed_message_count
		FROM email_channel_state WHERE channel_id = ?
	`, channelID).Scan(&lastUID, &validity, &errorCount, &lastError, &failedUID, &failedCount); err != nil {
		t.Fatalf("load state: %v", err)
	}
	if lastUID != 0 || validity != 13 {
		t.Fatalf("cursor after epoch change = (uid %d, validity %d), want (0, 13)", lastUID, validity)
	}
	if errorCount != 0 || lastError != nil || failedUID != 0 || failedCount != 0 {
		t.Fatalf("clean poll did not clear failure state: count=%d error=%v failed_uid=%d failed_count=%d",
			errorCount, lastError, failedUID, failedCount)
	}
}
