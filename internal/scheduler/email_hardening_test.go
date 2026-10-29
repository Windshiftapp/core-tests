//go:build test

package scheduler

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"windshift/internal/blobstore"
	"windshift/internal/database"
	"windshift/internal/email"
	"windshift/internal/models"
	"windshift/internal/sso"
	"windshift/internal/testutils"
)

type hardeningIMAPClient struct {
	mu          sync.Mutex
	uidValidity uint32
	uidNext     uint32
	messages    []*email.FetchedMessage
	marked      []uint32
	deleted     []uint32
	batchSizes  []int
}

func (c *hardeningIMAPClient) SelectMailbox(string) (*imap.SelectData, error) {
	return &imap.SelectData{UIDValidity: c.uidValidity, UIDNext: imap.UID(c.uidNext)}, nil
}

func (c *hardeningIMAPClient) FetchMessages(sinceUID uint32, batchSize int) ([]*email.FetchedMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.batchSizes = append(c.batchSizes, batchSize)
	var out []*email.FetchedMessage
	for _, m := range c.messages {
		if m.UID > sinceUID {
			out = append(out, m)
		}
	}
	if batchSize > 0 && len(out) > batchSize {
		out = out[:batchSize]
	}
	return out, nil
}

func (c *hardeningIMAPClient) MarkAsRead(uid uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.marked = append(c.marked, uid)
	return nil
}

func (c *hardeningIMAPClient) DeleteMessage(uid uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleted = append(c.deleted, uid)
	return nil
}

func (c *hardeningIMAPClient) Expunge() error { return nil }
func (c *hardeningIMAPClient) Close() error   { return nil }

func hardeningMessage(uid uint32, messageID, subject string) *email.FetchedMessage {
	return &email.FetchedMessage{
		UID: uid,
		Envelope: &imap.Envelope{
			Date:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Subject:   subject,
			MessageID: "<" + messageID + ">",
			From:      []imap.Address{{Mailbox: "customer", Host: "example.com"}},
		},
		Raw: []byte("From: customer@example.com\r\nSubject: " + subject +
			"\r\nMessage-ID: <" + messageID + ">\r\n\r\nPlease help\r\n"),
	}
}

type hardeningFixture struct {
	db        database.Database
	scheduler *EmailScheduler
	channelID int
	intakeID  int
	client    *hardeningIMAPClient
	channel   channelInfo
}

func newHardeningFixture(t *testing.T, importExisting bool, disposition string) *hardeningFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	ctx := context.Background()

	var itemTypeID int
	if err := db.QueryRowContext(ctx, `SELECT id FROM item_types WHERE is_default = true ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("load default item type: %v", err)
	}
	configJSON, err := json.Marshal(models.ChannelConfig{
		EmailWorkspaceID:           data.WorkspaceID,
		EmailItemTypeID:            &itemTypeID,
		EmailMailbox:               "INBOX",
		EmailProcessingDisposition: disposition,
	})
	if err != nil {
		t.Fatalf("marshal channel config: %v", err)
	}
	var channelID int
	if err := db.QueryRowContext(ctx, `
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Hardening', 'email', 'inbound', 'enabled', ?)
		RETURNING id
	`, string(configJSON)).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	var intakeID int
	if err := db.QueryRowContext(ctx, `
		INSERT INTO intakes (
			mailbox_id, folder, workspace_id, item_type_id, processing_disposition,
			import_existing_mail, status
		) VALUES (?, 'INBOX', ?, ?, ?, ?, 'enabled')
		RETURNING id
	`, channelID, data.WorkspaceID, itemTypeID, disposition, importExisting).Scan(&intakeID); err != nil {
		t.Fatalf("insert intake: %v", err)
	}

	client := &hardeningIMAPClient{uidValidity: 7, uidNext: 4}
	enc := sso.NewSecretEncryption("test-server-secret-with-sufficient-length-for-derivation")
	es := NewEmailScheduler(db, email.NewCredentialManager(db, enc), blobstore.NewLocal(t.TempDir()))
	es.providerForChannel = func(context.Context, int) (email.Provider, *models.ChannelConfig, error) {
		return &fakeProvider{client: client}, &models.ChannelConfig{
			EmailWorkspaceID:           data.WorkspaceID,
			EmailItemTypeID:            &itemTypeID,
			EmailMailbox:               "INBOX",
			EmailProcessingDisposition: disposition,
		}, nil
	}
	return &hardeningFixture{
		db:        db,
		scheduler: es,
		channelID: channelID,
		intakeID:  intakeID,
		client:    client,
		channel:   channelInfo{ID: channelID, Name: "Hardening", Config: string(configJSON)},
	}
}

func (f *hardeningFixture) itemCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM items WHERE channel_id = ?`, f.channelID).Scan(&count); err != nil {
		t.Fatalf("count items: %v", err)
	}
	return count
}

func (f *hardeningFixture) intakeState(t *testing.T) (lastUID int, validity uint32) {
	t.Helper()
	if err := f.db.QueryRow(`SELECT last_uid, uid_validity FROM email_intake_state WHERE intake_id = ?`, f.intakeID).Scan(&lastUID, &validity); err != nil {
		t.Fatalf("read intake state: %v", err)
	}
	return lastUID, validity
}

func TestEmailIntakeStartsAtUIDNextForANewMailbox(t *testing.T) {
	f := newHardeningFixture(t, false, models.EmailDispositionMarkRead)
	f.client.messages = []*email.FetchedMessage{
		hardeningMessage(1, "old-1@example.com", "old 1"),
		hardeningMessage(2, "old-2@example.com", "old 2"),
		hardeningMessage(3, "old-3@example.com", "old 3"),
	}

	if ok := f.scheduler.processChannel(context.Background(), f.channel); !ok {
		t.Fatal("first poll failed")
	}
	if got := f.itemCount(t); got != 0 {
		t.Fatalf("first poll created %d items, want 0 existing messages imported", got)
	}
	if lastUID, validity := f.intakeState(t); lastUID != 3 || validity != 7 {
		t.Fatalf("watermark after first poll = %d/%d, want 3/7", lastUID, validity)
	}

	// New mail must still be picked up after the history is skipped.
	f.client.messages = append(f.client.messages, hardeningMessage(4, "new-4@example.com", "new 4"))
	if ok := f.scheduler.processChannel(context.Background(), f.channel); !ok {
		t.Fatal("second poll failed")
	}
	if got := f.itemCount(t); got != 1 {
		t.Fatalf("second poll created %d items, want 1 new message", got)
	}
	if lastUID, _ := f.intakeState(t); lastUID != 4 {
		t.Fatalf("watermark after new mail = %d, want 4", lastUID)
	}
}

func TestEmailIntakeImportsExistingMailWhenOptedIn(t *testing.T) {
	f := newHardeningFixture(t, true, "")
	f.client.messages = []*email.FetchedMessage{
		hardeningMessage(1, "old-1@example.com", "old 1"),
		hardeningMessage(2, "old-2@example.com", "old 2"),
		hardeningMessage(3, "old-3@example.com", "old 3"),
	}

	if ok := f.scheduler.processChannel(context.Background(), f.channel); !ok {
		t.Fatal("first poll failed")
	}
	if got := f.itemCount(t); got != 3 {
		t.Fatalf("import poll created %d items, want 3", got)
	}
	if lastUID, _ := f.intakeState(t); lastUID != 3 {
		t.Fatalf("watermark after import = %d, want 3", lastUID)
	}
}

func TestEmailIntakeAppliesDispositionToAlreadyProcessedMail(t *testing.T) {
	f := newHardeningFixture(t, false, models.EmailDispositionDelete)
	const messageID = "already-processed@example.com"
	f.client.messages = []*email.FetchedMessage{hardeningMessage(1, messageID, "already processed")}
	if _, err := f.db.ExecWrite(`
		INSERT INTO email_intake_state (intake_id, last_uid, uid_validity)
		VALUES (?, 0, 7)
	`, f.intakeID); err != nil {
		t.Fatalf("insert intake state: %v", err)
	}
	if _, err := f.db.ExecWrite(`
		INSERT INTO email_message_tracking (
			channel_id, intake_id, message_id, dedup_key, from_email, direction, processed_at, completed_at
		) VALUES (?, ?, ?, ?, 'customer@example.com', 'inbound', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, f.channelID, f.intakeID, messageID, messageID); err != nil {
		t.Fatalf("insert completed tracking row: %v", err)
	}

	if ok := f.scheduler.processChannel(context.Background(), f.channel); !ok {
		t.Fatal("poll failed")
	}
	f.client.mu.Lock()
	deleted := append([]uint32(nil), f.client.deleted...)
	f.client.mu.Unlock()
	if len(deleted) != 1 || deleted[0] != 1 {
		t.Fatalf("deleted UIDs = %v, want [1]", deleted)
	}
	if lastUID, _ := f.intakeState(t); lastUID != 1 {
		t.Fatalf("watermark = %d, want 1", lastUID)
	}
}

func TestEmailIntakePersistsWatermarkWithCanceledContext(t *testing.T) {
	f := newHardeningFixture(t, false, "")
	if _, err := f.db.ExecWrite(`
		INSERT INTO email_intake_state (intake_id, last_uid, uid_validity)
		VALUES (?, 0, 0)
	`, f.intakeID); err != nil {
		t.Fatalf("insert intake state: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.scheduler.updateIntakeState(ctx, f.intakeID, 42, 9, 0, 0, 0)
	if lastUID, validity := f.intakeState(t); lastUID != 42 || validity != 9 {
		t.Fatalf("watermark after canceled persist = %d/%d, want 42/9", lastUID, validity)
	}
}

func TestEmailSchedulerUsesConfiguredBatchSize(t *testing.T) {
	f := newHardeningFixture(t, true, "")
	f.client.messages = []*email.FetchedMessage{
		hardeningMessage(1, "one@example.com", "one"),
		hardeningMessage(2, "two@example.com", "two"),
		hardeningMessage(3, "three@example.com", "three"),
	}
	f.scheduler.SetBatchSize(2)
	if ok := f.scheduler.processChannel(context.Background(), f.channel); !ok {
		t.Fatal("poll failed")
	}
	f.client.mu.Lock()
	sizes := append([]int(nil), f.client.batchSizes...)
	f.client.mu.Unlock()
	if len(sizes) == 0 || sizes[0] != 2 {
		t.Fatalf("fetch batch sizes = %v, want first 2", sizes)
	}
}

type blockingProvider struct {
	release <-chan struct{}
	client  email.IMAPClient
}

func (p *blockingProvider) GetType() string                                   { return "blocking" }
func (p *blockingProvider) GetIMAPServer(*models.ChannelConfig) (string, int) { return "blocking", 0 }
func (p *blockingProvider) TestConnection(context.Context, *models.ChannelConfig) error {
	return nil
}
func (p *blockingProvider) Connect(ctx context.Context, _ *models.ChannelConfig) (email.IMAPClient, error) {
	select {
	case <-p.release:
		return p.client, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestEmailSchedulerPollsMailboxesConcurrently(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	ctx := context.Background()
	var itemTypeID int
	if err := db.QueryRowContext(ctx, `SELECT id FROM item_types WHERE is_default = true ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("load default item type: %v", err)
	}
	configJSON, err := json.Marshal(models.ChannelConfig{EmailWorkspaceID: data.WorkspaceID, EmailItemTypeID: &itemTypeID, EmailMailbox: "INBOX"})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	insertChannel := func(name string) int {
		var id int
		if err := db.QueryRowContext(ctx, `
			INSERT INTO channels (name, type, direction, status, config)
			VALUES (?, 'email', 'inbound', 'enabled', ?) RETURNING id
		`, name, string(configJSON)).Scan(&id); err != nil {
			t.Fatalf("insert channel %s: %v", name, err)
		}
		if _, err := db.ExecWriteContext(ctx, `
			INSERT INTO intakes (mailbox_id, folder, workspace_id, item_type_id, import_existing_mail, status)
			VALUES (?, 'INBOX', ?, ?, true, 'enabled')
		`, id, data.WorkspaceID, itemTypeID); err != nil {
			t.Fatalf("insert intake for %s: %v", name, err)
		}
		return id
	}
	slowChannelID := insertChannel("Slow")
	fastChannelID := insertChannel("Fast")

	release := make(chan struct{})
	fastClient := &hardeningIMAPClient{uidValidity: 3, uidNext: 2, messages: []*email.FetchedMessage{hardeningMessage(1, "fast@example.com", "fast")}}
	enc := sso.NewSecretEncryption("test-server-secret-with-sufficient-length-for-derivation")
	es := NewEmailScheduler(db, email.NewCredentialManager(db, enc), blobstore.NewLocal(t.TempDir()))
	es.SetPollConcurrency(2)
	es.providerForChannel = func(_ context.Context, channelID int) (email.Provider, *models.ChannelConfig, error) {
		cfg := &models.ChannelConfig{EmailWorkspaceID: data.WorkspaceID, EmailItemTypeID: &itemTypeID, EmailMailbox: "INBOX"}
		if channelID == slowChannelID {
			return &blockingProvider{release: release, client: fastClient}, cfg, nil
		}
		return &fakeProvider{client: fastClient}, cfg, nil
	}

	done := make(chan struct{})
	go func() {
		es.processEmailChannels()
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE channel_id = ?`, fastChannelID).Scan(&count); err != nil {
			t.Fatalf("count fast items: %v", err)
		}
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			<-done
			t.Fatal("fast mailbox was blocked by the slow mailbox")
		}
		<-tick.C
	}

	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not finish after releasing the slow mailbox")
	}
}
