//go:build test

package scheduler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"windshift/internal/blobstore"
	"windshift/internal/email"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/sso"
	"windshift/internal/testutils"
)

// recordingIMAPClient wraps the fake client and records which messages the
// scheduler marked read, deleted, or expunged, so tests can assert that
// rate-limited mail is left untouched in the mailbox.
type recordingIMAPClient struct {
	*fakeIMAPClient
	markedRead []uint32
	deleted    []uint32
	expunged   bool
}

func (c *recordingIMAPClient) MarkAsRead(uid uint32) error {
	c.markedRead = append(c.markedRead, uid)
	return nil
}

func (c *recordingIMAPClient) DeleteMessage(uid uint32) error {
	c.deleted = append(c.deleted, uid)
	return nil
}

func (c *recordingIMAPClient) Expunge() error {
	c.expunged = true
	return nil
}

func (c *recordingIMAPClient) saw(uid uint32, uids []uint32) bool {
	for _, v := range uids {
		if v == uid {
			return true
		}
	}
	return false
}

// setupRateLimitedChannel creates a channel with a per-sender cap of 1 and a
// mailbox holding two fresh messages from the same sender. It returns the
// scheduler, recording client, and channel fixture.
func setupRateLimitedChannel(t *testing.T) (ctx context.Context, es *EmailScheduler, client *recordingIMAPClient, channelID int, workspaceID int, config *models.ChannelConfig) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	ctx = context.Background()

	var itemTypeID int
	if err := db.QueryRowContext(ctx, `SELECT id FROM item_types WHERE is_default = true ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("load default item type: %v", err)
	}

	cap := 1
	channelConfig := models.ChannelConfig{
		EmailWorkspaceID:           data.WorkspaceID,
		EmailItemTypeID:            &itemTypeID,
		EmailMailbox:               "INBOX",
		EmailProcessingDisposition: models.EmailDispositionDelete,
		EmailRateLimitPerHour:      &cap,
	}
	configJSON, err := json.Marshal(channelConfig)
	if err != nil {
		t.Fatalf("marshal channel config: %v", err)
	}
	if err := db.QueryRowContext(ctx, `
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Flooded channel', 'email', 'inbound', 'enabled', ?)
		RETURNING id
	`, string(configJSON)).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := db.ExecWriteContext(ctx, `INSERT INTO email_channel_state (channel_id, last_uid) VALUES (?, 0)`, channelID); err != nil {
		t.Fatalf("insert channel state: %v", err)
	}

	message := func(id string, uid uint32) *email.FetchedMessage {
		return &email.FetchedMessage{
			UID: uid,
			Envelope: &imap.Envelope{
				Date: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Subject: "Flood " + id,
				MessageID: "<" + id + "@example.com>", From: []imap.Address{{Mailbox: "customer", Host: "example.com"}},
			},
			Raw: []byte("From: customer@example.com\r\nSubject: Flood " + id + "\r\nMessage-ID: <" + id + "@example.com>\r\n\r\nbody " + id + "\r\n"),
		}
	}
	client = &recordingIMAPClient{fakeIMAPClient: &fakeIMAPClient{
		uidValidity: 42,
		messages:    []*email.FetchedMessage{message("flood-a", 1), message("flood-b", 2)},
	}}

	enc := sso.NewSecretEncryption("test-server-secret-with-sufficient-length-for-derivation")
	es = NewEmailScheduler(db, email.NewCredentialManager(db, enc), blobstore.NewLocal(t.TempDir()))
	es.SetCommentService(services.NewCommentService(db))
	es.providerForChannel = func(context.Context, int) (email.Provider, *models.ChannelConfig, error) {
		return &fakeProvider{client: client}, &channelConfig, nil
	}
	return ctx, es, client, channelID, data.WorkspaceID, &channelConfig
}

// TestEmailScheduler_RateLimitedMailStaysInMailboxAndAdvancesWatermark
// verifies the flood contract end to end: the second message from a capped
// sender is declined, its tracking row is marked for requeue, the watermark
// still advances past it so the channel cannot wedge, and the mail is neither
// marked read nor deleted nor expunged.
func TestEmailScheduler_RateLimitedMailStaysInMailboxAndAdvancesWatermark(t *testing.T) {
	ctx, es, client, channelID, _, _ := setupRateLimitedChannel(t)
	db := es.db
	ch := channelInfo{ID: channelID, Name: "Flooded channel"}

	if ok := es.processChannel(ctx, ch); !ok {
		t.Fatal("rate limiting is a policy outcome, not a channel failure")
	}

	var itemCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE channel_id = ?`, channelID).Scan(&itemCount); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if itemCount != 1 {
		t.Fatalf("items = %d, want 1", itemCount)
	}

	state, err := es.getOrCreateChannelState(ctx, channelID)
	if err != nil {
		t.Fatalf("read channel state: %v", err)
	}
	if state.LastUID != 2 {
		t.Fatalf("last_uid = %d, want 2 (watermark must advance past rate-limited mail)", state.LastUID)
	}
	if state.ErrorCount != 0 || state.LastError != "" {
		t.Fatalf("channel flagged unhealthy: errors=%d last_error=%q, want clean", state.ErrorCount, state.LastError)
	}

	var rateLimitedRows int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM email_message_tracking
		WHERE channel_id = ? AND rate_limited_at IS NOT NULL AND item_id IS NULL
	`, channelID).Scan(&rateLimitedRows); err != nil {
		t.Fatalf("count rate-limited rows: %v", err)
	}
	if rateLimitedRows != 1 {
		t.Fatalf("rate-limited rows = %d, want 1", rateLimitedRows)
	}

	var uid, validity int
	if err := db.QueryRowContext(ctx, `
		SELECT uid, uid_validity FROM email_message_tracking
		WHERE channel_id = ? AND rate_limited_at IS NOT NULL
	`, channelID).Scan(&uid, &validity); err != nil {
		t.Fatalf("read rate-limited coordinates: %v", err)
	}
	if uid != 2 || validity != 42 {
		t.Fatalf("rate-limited uid/validity = %d/%d, want 2/42", uid, validity)
	}

	// UID 1 was processed and deleted; UID 2 was declined and must be
	// untouched. The delete disposition supersedes mark-read, so the processed
	// message is never also flagged \Seen. Expunge only removes the processed
	// message.
	if len(client.markedRead) != 0 {
		t.Fatalf("marked-read UIDs = %v, want none (delete supersedes mark-read)", client.markedRead)
	}
	if !client.saw(1, client.deleted) || client.saw(2, client.deleted) {
		t.Fatalf("deleted UIDs = %v, want [1] only", client.deleted)
	}
	if !client.expunged {
		t.Fatal("expunge not called although a processed message was flagged deleted")
	}
}

// TestEmailScheduler_MarkReadDispositionLeavesMailInMailbox verifies the
// mark-read disposition marks processed mail and never deletes or expunges it.
func TestEmailScheduler_MarkReadDispositionLeavesMailInMailbox(t *testing.T) {
	ctx, es, client, channelID, _, config := setupRateLimitedChannel(t)
	// The helper seeds a delete disposition; switch to mark-read on the same
	// captured config the provider returns.
	*config = models.ChannelConfig{
		EmailWorkspaceID:           config.EmailWorkspaceID,
		EmailItemTypeID:            config.EmailItemTypeID,
		EmailMailbox:               "INBOX",
		EmailProcessingDisposition: models.EmailDispositionMarkRead,
		EmailRateLimitPerHour:      config.EmailRateLimitPerHour,
	}

	ch := channelInfo{ID: channelID, Name: "Flooded channel"}
	if ok := es.processChannel(ctx, ch); !ok {
		t.Fatal("mark-read disposition is not a channel failure")
	}

	if !client.saw(1, client.markedRead) || client.saw(2, client.markedRead) {
		t.Fatalf("marked-read UIDs = %v, want [1] only", client.markedRead)
	}
	if len(client.deleted) != 0 {
		t.Fatalf("deleted UIDs = %v, want none", client.deleted)
	}
	if client.expunged {
		t.Fatal("expunge must not run when nothing was deleted")
	}
}

// TestEmailScheduler_RequeueRateLimitedRewindsWatermark verifies the operator
// recovery path: the earliest rate-limited UID in the current epoch rewinds
// the watermark and clears the poison tracker; rows from older epochs are
// ignored and a channel with nothing declined reports not-found.
func TestEmailScheduler_RequeueRateLimitedRewindsWatermark(t *testing.T) {
	ctx, es, _, channelID, _, _ := setupRateLimitedChannel(t)
	db := es.db
	repo := repository.NewChannelRepository(db)

	if ok := es.processChannel(ctx, channelInfo{ID: channelID, Name: "Flooded channel"}); !ok {
		t.Fatal("processChannel failed")
	}

	// A rate-limited row from an ancient epoch must not influence recovery.
	if _, err := db.ExecWriteContext(ctx, `
		INSERT INTO email_message_tracking
			(channel_id, message_id, dedup_key, from_email, direction, uid, uid_validity, rate_limited_at, processed_at)
		VALUES (?, '<old@example.com>', 'old-epoch-key', 'customer@example.com', 'inbound', 1, 7, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, channelID); err != nil {
		t.Fatalf("seed stale-epoch rate-limited row: %v", err)
	}

	earliest, found, err := repo.GetEarliestRateLimitedUID(ctx, channelID, 42)
	if err != nil {
		t.Fatalf("GetEarliestRateLimitedUID: %v", err)
	}
	if !found || earliest != 2 {
		t.Fatalf("earliest rate-limited = (%d, %v), want (2, true)", earliest, found)
	}

	// A poison tracker left behind must not block the requeued poll.
	if _, err := db.ExecWriteContext(ctx, `
		UPDATE email_channel_state
		SET failed_message_uid = 2, failed_message_uid_validity = 42, failed_message_count = 3
		WHERE channel_id = ?
	`, channelID); err != nil {
		t.Fatalf("seed poison tracker: %v", err)
	}

	if err := repo.ResetEmailWatermarkToUID(ctx, channelID, earliest-1); err != nil {
		t.Fatalf("ResetEmailWatermarkToUID: %v", err)
	}

	state, err := es.getOrCreateChannelState(ctx, channelID)
	if err != nil {
		t.Fatalf("read channel state: %v", err)
	}
	if state.LastUID != 1 {
		t.Fatalf("last_uid = %d, want 1", state.LastUID)
	}
	if state.FailedMessageUID != 0 || state.FailedMessageUIDValidity != 0 || state.FailedMessageCount != 0 {
		t.Fatalf("poison tracker = %d/%d/%d, want cleared", state.FailedMessageUID, state.FailedMessageUIDValidity, state.FailedMessageCount)
	}

	// A channel with no rate-limited mail reports nothing to requeue.
	if _, found, err := repo.GetEarliestRateLimitedUID(ctx, channelID, 999); err != nil || found {
		t.Fatalf("unknown epoch: found=%v err=%v, want false/nil", found, err)
	}
}

// TestEmailScheduler_RepliesDoNotConsumeNewTicketQuota verifies WI-1571: the
// per-sender flood cap counts only successful new conversations, so ordinary
// back-and-forth replies never prevent a customer from opening a new ticket.
func TestEmailScheduler_RepliesDoNotConsumeNewTicketQuota(t *testing.T) {
	ctx, es, client, channelID, _, config := setupRateLimitedChannel(t)
	db := es.db
	*config.EmailRateLimitPerHour = 2

	rawMessage := func(id string, uid uint32, inReplyTo string) *email.FetchedMessage {
		headers := "From: customer@example.com\r\nSubject: Quota " + id + "\r\nMessage-ID: <" + id + "@example.com>\r\n"
		if inReplyTo != "" {
			headers += "In-Reply-To: <" + inReplyTo + "@example.com>\r\n"
		}
		return &email.FetchedMessage{
			UID: uid,
			Envelope: &imap.Envelope{
				Date:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				Subject:   "Quota " + id,
				MessageID: "<" + id + "@example.com>",
				From:      []imap.Address{{Mailbox: "customer", Host: "example.com"}},
			},
			Raw: []byte(headers + "\r\nbody " + id + "\r\n"),
		}
	}
	// New conversation, a reply to it, then a second new conversation.
	client.messages = []*email.FetchedMessage{
		rawMessage("quota-a", 1, ""),
		rawMessage("quota-reply", 2, "quota-a"),
		rawMessage("quota-b", 3, ""),
	}

	if ok := es.processChannel(ctx, channelInfo{ID: channelID, Name: "Flooded channel"}); !ok {
		t.Fatal("processChannel failed")
	}

	var items, comments, rateLimited int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE channel_id = ?`, channelID).Scan(&items); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM comments c JOIN items i ON i.id = c.item_id WHERE i.channel_id = ?
	`, channelID).Scan(&comments); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM email_message_tracking WHERE channel_id = ? AND rate_limited_at IS NOT NULL
	`, channelID).Scan(&rateLimited); err != nil {
		t.Fatalf("count rate-limited rows: %v", err)
	}

	if items != 2 {
		t.Fatalf("items = %d, want 2 new conversations", items)
	}
	if comments != 1 {
		t.Fatalf("comments = %d, want the reply appended", comments)
	}
	if rateLimited != 0 {
		t.Fatalf("rate-limited rows = %d, want 0 (the reply must not consume the quota)", rateLimited)
	}
}

// TestEmailScheduler_RequeueReprocessesRateLimitedMail verifies the operator
// requeue is deterministic: resetting the preclaim window lets the very next
// poll reclaim and reprocess the declined message instead of deferring behind
// the stale-claim lease, so a sender who is back under the cap gets a ticket.
func TestEmailScheduler_RequeueReprocessesRateLimitedMail(t *testing.T) {
	ctx, es, _, channelID, _, config := setupRateLimitedChannel(t)
	db := es.db
	repo := repository.NewChannelRepository(db)

	if ok := es.processChannel(ctx, channelInfo{ID: channelID, Name: "Flooded channel"}); !ok {
		t.Fatal("initial poll failed")
	}

	earliest, found, err := repo.GetEarliestRateLimitedUID(ctx, channelID, 42)
	if err != nil || !found {
		t.Fatalf("GetEarliestRateLimitedUID = (%d, %v, %v), want found", earliest, found, err)
	}
	if err := repo.ResetEmailWatermarkToUID(ctx, channelID, earliest-1); err != nil {
		t.Fatalf("ResetEmailWatermarkToUID: %v", err)
	}
	requeued, err := repo.RequeueRateLimitedClaims(ctx, channelID, 42)
	if err != nil {
		t.Fatalf("RequeueRateLimitedClaims: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued claims = %d, want 1", requeued)
	}

	// Raise the cap so the requeued message is no longer declined.
	*config.EmailRateLimitPerHour = 2
	if ok := es.processChannel(ctx, channelInfo{ID: channelID, Name: "Flooded channel"}); !ok {
		t.Fatal("requeued poll failed")
	}

	var items int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE channel_id = ?`, channelID).Scan(&items); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if items != 2 {
		t.Fatalf("items after requeue = %d, want 2 (requeued message must be reprocessed)", items)
	}
	var stillLimited int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM email_message_tracking
		WHERE channel_id = ? AND rate_limited_at IS NOT NULL
	`, channelID).Scan(&stillLimited); err != nil {
		t.Fatalf("count rate-limited rows: %v", err)
	}
	if stillLimited != 0 {
		t.Fatalf("rate-limited rows after reprocessing = %d, want 0", stillLimited)
	}
}
