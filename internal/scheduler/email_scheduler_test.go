//go:build test

package scheduler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"windshift/internal/blobstore"
	"windshift/internal/database"
	"windshift/internal/email"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/sso"
	"windshift/internal/testutils"
)

func TestEmailSchedulerDefersUIDUntilTrackingClaimRecovers(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	ctx := context.Background()
	var itemTypeID int
	if err := db.QueryRowContext(ctx, `SELECT id FROM item_types WHERE is_default = true ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("load default item type: %v", err)
	}

	configJSON, err := json.Marshal(models.ChannelConfig{EmailWorkspaceID: data.WorkspaceID, EmailItemTypeID: &itemTypeID})
	if err != nil {
		t.Fatalf("marshal channel config: %v", err)
	}
	var channelID int
	if err := db.QueryRowContext(ctx, `
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Claim recovery', 'email', 'inbound', 'enabled', ?)
		RETURNING id
	`, string(configJSON)).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := db.ExecWriteContext(ctx, `INSERT INTO email_channel_state (channel_id, last_uid) VALUES (?, 0)`, channelID); err != nil {
		t.Fatalf("insert channel state: %v", err)
	}
	const messageID = "claim-recovery@example.com"
	if _, err := db.ExecWriteContext(ctx, `
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, from_email, direction, processed_at)
		VALUES (?, ?, ?, 'customer@example.com', 'inbound', CURRENT_TIMESTAMP)
	`, channelID, messageID, messageID); err != nil {
		t.Fatalf("insert unfinished tracking claim: %v", err)
	}

	message := &email.FetchedMessage{
		UID: 1,
		Envelope: &imap.Envelope{
			Date: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Subject: "Need help",
			MessageID: "<" + messageID + ">", From: []imap.Address{{Mailbox: "customer", Host: "example.com"}},
		},
		Raw: []byte("From: customer@example.com\r\nSubject: Need help\r\nMessage-ID: <" + messageID + ">\r\n\r\nPlease help\r\n"),
	}
	enc := sso.NewSecretEncryption("test-server-secret-with-sufficient-length-for-derivation")
	es := NewEmailScheduler(db, email.NewCredentialManager(db, enc), blobstore.NewLocal(t.TempDir()))
	es.SetCommentService(services.NewCommentService(db))
	es.providerForChannel = func(context.Context, int) (email.Provider, *models.ChannelConfig, error) {
		return &fakeProvider{client: &fakeIMAPClient{uidValidity: 7, messages: []*email.FetchedMessage{message}}},
			&models.ChannelConfig{EmailWorkspaceID: data.WorkspaceID, EmailItemTypeID: &itemTypeID, EmailMailbox: "INBOX"}, nil
	}
	channel := channelInfo{ID: channelID, Name: "Claim recovery", Config: string(configJSON)}

	if ok := es.processChannel(ctx, channel); !ok {
		t.Fatal("live tracking claim should defer without marking the channel unhealthy")
	}
	state, err := es.getOrCreateChannelState(ctx, channelID)
	if err != nil {
		t.Fatalf("read deferred state: %v", err)
	}
	if state.LastUID != 0 || state.FailedMessageCount != 0 {
		t.Fatalf("deferred state = last_uid:%d failed_count:%d, want 0/0", state.LastUID, state.FailedMessageCount)
	}
	var itemCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE channel_id = ?`, channelID).Scan(&itemCount); err != nil {
		t.Fatalf("count deferred items: %v", err)
	}
	if itemCount != 0 {
		t.Fatalf("items before stale recovery = %d, want 0", itemCount)
	}

	if _, err := db.ExecWriteContext(ctx, `
		UPDATE email_message_tracking SET processed_at = ?
		WHERE channel_id = ? AND dedup_key = ?
	`, time.Now().Add(-6*time.Minute), channelID, messageID); err != nil {
		t.Fatalf("age tracking claim: %v", err)
	}
	if ok := es.processChannel(ctx, channel); !ok {
		t.Fatal("stale tracking claim did not recover")
	}
	state, err = es.getOrCreateChannelState(ctx, channelID)
	if err != nil {
		t.Fatalf("read recovered state: %v", err)
	}
	if state.LastUID != 1 {
		t.Fatalf("recovered last_uid = %d, want 1", state.LastUID)
	}
	var completedTracking int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM email_message_tracking
		WHERE channel_id = ? AND dedup_key = ? AND item_id IS NOT NULL
	`, channelID, messageID).Scan(&completedTracking); err != nil {
		t.Fatalf("count completed tracking: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE channel_id = ?`, channelID).Scan(&itemCount); err != nil {
		t.Fatalf("count recovered items: %v", err)
	}
	if itemCount != 1 || completedTracking != 1 {
		t.Fatalf("recovery counts = items:%d completed_tracking:%d, want 1/1", itemCount, completedTracking)
	}

	if ok := es.processChannel(ctx, channel); !ok {
		t.Fatal("completed deduplication poll failed")
	}
	var finalItemCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE channel_id = ?`, channelID).Scan(&finalItemCount); err != nil {
		t.Fatalf("count final items: %v", err)
	}
	if finalItemCount != 1 {
		t.Fatalf("completed deduplication created %d items, want 1", finalItemCount)
	}
}

// fakeIMAPClient is an in-memory email.IMAPClient that serves a fixed set of
// messages (those with UID greater than the requested watermark). It lets the
// scheduler be driven without a real IMAP server — the production connect path
// is TLS-only and SSRF-guarded, so it can't reach an in-process server.
type fakeIMAPClient struct {
	uidValidity uint32
	messages    []*email.FetchedMessage
}

func (c *fakeIMAPClient) SelectMailbox(string) (*imap.SelectData, error) {
	return &imap.SelectData{UIDValidity: c.uidValidity}, nil
}

func (c *fakeIMAPClient) FetchMessages(sinceUID uint32, _ int) ([]*email.FetchedMessage, error) {
	var out []*email.FetchedMessage
	for _, m := range c.messages {
		if m.UID > sinceUID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (c *fakeIMAPClient) MarkAsRead(uint32) error    { return nil }
func (c *fakeIMAPClient) DeleteMessage(uint32) error { return nil }
func (c *fakeIMAPClient) Expunge() error             { return nil }
func (c *fakeIMAPClient) Close() error               { return nil }

// fakeProvider hands back a fixed fakeIMAPClient from Connect.
type fakeProvider struct{ client email.IMAPClient }

func (p *fakeProvider) GetType() string                                             { return "fake" }
func (p *fakeProvider) GetIMAPServer(*models.ChannelConfig) (string, int)           { return "fake", 0 }
func (p *fakeProvider) TestConnection(context.Context, *models.ChannelConfig) error { return nil }
func (p *fakeProvider) Connect(context.Context, *models.ChannelConfig) (email.IMAPClient, error) {
	return p.client, nil
}

// TestEmailScheduler_PoisonMessageDroppedAfterMaxAttempts verifies the
// head-of-line-blocking guard: a message that fails to process on every poll
// (here, because the channel has no workspace configured) holds the UID
// watermark back for maxDeliveryAttempts-1 polls, then is dropped — the
// watermark advances past it, error_count resets, and the channel records why.
func TestEmailScheduler_PoisonMessageDroppedAfterMaxAttempts(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	db := tdb.Database
	ctx := context.Background()

	// A channel row must exist for the email_channel_state FK; its stored
	// config is irrelevant here because providerForChannel is overridden.
	var channelID int
	if err := db.QueryRowContext(ctx, `
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Support', 'email', 'inbound', 'enabled', '{}')
		RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	// The intake targets a workspace that does not exist, so processing fails
	// deterministically and the message looks like poison. The watermark is
	// per intake.
	var intakeID int
	if err := db.QueryRowContext(ctx, `
		INSERT INTO intakes (mailbox_id, folder, workspace_id, status)
		VALUES (?, 'INBOX', 0, 'enabled')
		RETURNING id
	`, channelID).Scan(&intakeID); err != nil {
		t.Fatalf("insert intake: %v", err)
	}

	const poisonUID = uint32(1)
	poison := &email.FetchedMessage{
		UID: poisonUID,
		Envelope: &imap.Envelope{
			Date:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Subject:   "poison",
			MessageID: "poison-1@example.com",
			From:      []imap.Address{{Mailbox: "customer", Host: "example.com"}},
		},
		Raw: []byte("From: customer@example.com\r\nSubject: poison\r\n" +
			"Message-ID: <poison-1@example.com>\r\n\r\nhello\r\n"),
	}

	enc := sso.NewSecretEncryption("test-server-secret-with-sufficient-length-for-derivation")
	es := NewEmailScheduler(db, email.NewCredentialManager(db, enc), blobstore.NewLocal(t.TempDir()))

	// Inject the fake provider. The returned config has no workspace, so the
	// real processor fails ProcessEmail on every poll (releasing its dedup
	// claim each time), which is exactly the stuck-message condition.
	es.providerForChannel = func(context.Context, int) (email.Provider, *models.ChannelConfig, error) {
		return &fakeProvider{client: &fakeIMAPClient{uidValidity: 1, messages: []*email.FetchedMessage{poison}}},
			&models.ChannelConfig{EmailMailbox: "INBOX"}, nil
	}

	ch := channelInfo{ID: channelID, Name: "Support"}

	// Polls 1..maxDeliveryAttempts-1: the message keeps failing, error_count
	// climbs, and the watermark is held back so it's retried.
	for i := 1; i < maxDeliveryAttempts; i++ {
		if ok := es.processChannel(ctx, ch); ok {
			t.Fatalf("poll %d: expected processChannel to report failure", i)
		}
		state, err := es.getOrCreateChannelState(ctx, channelID)
		if err != nil {
			t.Fatalf("poll %d: read state: %v", i, err)
		}
		if state.ErrorCount != i {
			t.Fatalf("poll %d: error_count = %d, want %d", i, state.ErrorCount, i)
		}
		if uid := intakeStateUID(t, db, intakeID); uid != 0 {
			t.Fatalf("poll %d: watermark advanced early to %d", i, uid)
		}
	}

	// Poll maxDeliveryAttempts: the message is treated as poison and dropped.
	if ok := es.processChannel(ctx, ch); ok {
		t.Fatalf("drop poll: expected processChannel to report failure")
	}
	if uid := intakeStateUID(t, db, intakeID); uid != int(poisonUID) {
		t.Fatalf("watermark did not advance past poison: last_uid = %d, want %d", uid, poisonUID)
	}
	state, err := es.getOrCreateChannelState(ctx, channelID)
	if err != nil {
		t.Fatalf("read state after drop: %v", err)
	}
	if !strings.Contains(state.LastError, "dropped poison message") {
		t.Fatalf("last_error does not record the drop: %q", state.LastError)
	}

	// Next poll: the watermark is now past the poison message, so there's
	// nothing to fetch and the channel is unwedged.
	if ok := es.processChannel(ctx, ch); !ok {
		t.Fatalf("post-drop poll: channel still failing; expected it to be unwedged")
	}
}

// intakeStateUID reads the per-folder watermark for an intake.
func intakeStateUID(t *testing.T, db database.Database, intakeID int) int {
	t.Helper()
	var uid int
	if err := db.QueryRow(`SELECT last_uid FROM email_intake_state WHERE intake_id = ?`, intakeID).Scan(&uid); err != nil {
		t.Fatalf("read intake state: %v", err)
	}
	return uid
}
