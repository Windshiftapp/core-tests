//go:build test

package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"windshift/internal/blobstore"
	"windshift/internal/email"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/sso"
	"windshift/internal/testutils"
)

// folderIMAPClient serves a different message set per selected folder so a
// multi-intake mailbox can be exercised.
type folderIMAPClient struct {
	uidValidity uint32
	byFolder    map[string][]*email.FetchedMessage
	selected    string
	markedRead  []uint32
	deleted     []uint32
}

func (c *folderIMAPClient) SelectMailbox(name string) (*imap.SelectData, error) {
	c.selected = name
	return &imap.SelectData{UIDValidity: c.uidValidity}, nil
}

func (c *folderIMAPClient) FetchMessages(sinceUID uint32, _ int) ([]*email.FetchedMessage, error) {
	var out []*email.FetchedMessage
	for _, m := range c.byFolder[c.selected] {
		if m.UID > sinceUID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (c *folderIMAPClient) MarkAsRead(uid uint32) error {
	c.markedRead = append(c.markedRead, uid)
	return nil
}
func (c *folderIMAPClient) DeleteMessage(uid uint32) error {
	c.deleted = append(c.deleted, uid)
	return nil
}
func (c *folderIMAPClient) Expunge() error { return nil }
func (c *folderIMAPClient) Close() error   { return nil }

func intakeMessage(folder, id string, uid uint32) *email.FetchedMessage {
	return &email.FetchedMessage{
		UID: uid,
		Envelope: &imap.Envelope{
			Date:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Subject:   folder + " " + id,
			MessageID: "<" + id + "@example.com>",
			From:      []imap.Address{{Mailbox: "customer", Host: "example.com"}},
		},
		Raw: []byte("From: customer@example.com\r\nSubject: " + folder + "\r\nMessage-ID: <" + id + "@example.com>\r\n\r\nbody\r\n"),
	}
}

// A mailbox feeds two intakes on distinct folders. Each folder creates its own
// item, records its own intake_id, and advances its own watermark.
func TestEmailScheduler_MultiIntakeMailboxPollsEachFolderIndependently(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	ctx := context.Background()

	var itemTypeID int
	if err := db.QueryRowContext(ctx, `SELECT id FROM item_types WHERE is_default = true ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("load default item type: %v", err)
	}

	var channelID int
	if err := db.QueryRowContext(ctx, `
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Shared mailbox', 'email', 'inbound', 'enabled', '{}')
		RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("insert mailbox channel: %v", err)
	}

	insertIntake := func(folder string) int {
		t.Helper()
		var id int
		if err := db.QueryRowContext(ctx, `
			INSERT INTO intakes (mailbox_id, folder, workspace_id, item_type_id, status)
			VALUES (?, ?, ?, ?, 'enabled')
			RETURNING id
		`, channelID, folder, data.WorkspaceID, itemTypeID).Scan(&id); err != nil {
			t.Fatalf("insert intake %s: %v", folder, err)
		}
		return id
	}
	inboxIntakeID := insertIntake("INBOX")
	supportIntakeID := insertIntake("Support")

	// Same (mailbox, folder) must not be claimed twice.
	if _, err := db.ExecWriteContext(ctx, `
		INSERT INTO intakes (mailbox_id, folder, workspace_id, status)
		VALUES (?, 'INBOX', ?, 'enabled')
	`, channelID, data.WorkspaceID); err == nil {
		t.Fatal("expected a unique (mailbox, folder) violation, got nil")
	}

	client := &folderIMAPClient{
		uidValidity: 5,
		byFolder: map[string][]*email.FetchedMessage{
			"INBOX":   {intakeMessage("INBOX", "inbox-1", 1)},
			"Support": {intakeMessage("Support", "support-1", 1)},
		},
	}
	enc := sso.NewSecretEncryption("test-server-secret-with-sufficient-length-for-derivation")
	es := NewEmailScheduler(db, email.NewCredentialManager(db, enc), blobstore.NewLocal(t.TempDir()))
	es.SetCommentService(services.NewCommentService(db))
	es.providerForChannel = func(context.Context, int) (email.Provider, *models.ChannelConfig, error) {
		return &fakeProvider{client: client}, &models.ChannelConfig{EmailMailbox: "INBOX"}, nil
	}

	if ok := es.processChannel(ctx, channelInfo{ID: channelID, Name: "Shared mailbox"}); !ok {
		t.Fatal("processChannel reported failure")
	}

	var itemCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE channel_id = ?`, channelID).Scan(&itemCount); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if itemCount != 2 {
		t.Fatalf("items = %d, want 2 (one per folder)", itemCount)
	}

	for folder, intakeID := range map[string]int{"INBOX": inboxIntakeID, "Support": supportIntakeID} {
		var got int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE intake_id = ?`, intakeID).Scan(&got); err != nil {
			t.Fatalf("count items for %s intake: %v", folder, err)
		}
		if got != 1 {
			t.Errorf("items for %s intake = %d, want 1", folder, got)
		}
		if uid := intakeStateUID(t, db, intakeID); uid != 1 {
			t.Errorf("%s intake watermark = %d, want 1", folder, uid)
		}
	}
}

// A mailbox that has intake rows is fully managed by them: a disabled intake
// must not fall back to the legacy channel routing and keep processing mail.
func TestEmailScheduler_DisabledIntakeDoesNotFallBackToLegacyConfig(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	ctx := context.Background()

	var itemTypeID int
	if err := db.QueryRowContext(ctx, `SELECT id FROM item_types WHERE is_default = true ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("load default item type: %v", err)
	}

	legacyConfig := fmt.Sprintf(`{"email_workspace_id":%d,"email_item_type_id":%d}`, data.WorkspaceID, itemTypeID)
	var channelID int
	if err := db.QueryRowContext(ctx, `
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Legacy mailbox', 'email', 'inbound', 'enabled', ?)
		RETURNING id
	`, legacyConfig).Scan(&channelID); err != nil {
		t.Fatalf("insert mailbox channel: %v", err)
	}
	if _, err := db.ExecWriteContext(ctx, `
		INSERT INTO intakes (mailbox_id, folder, workspace_id, item_type_id, status)
		VALUES (?, 'INBOX', ?, ?, 'disabled')
	`, channelID, data.WorkspaceID, itemTypeID); err != nil {
		t.Fatalf("insert disabled intake: %v", err)
	}

	client := &folderIMAPClient{
		uidValidity: 1,
		byFolder:    map[string][]*email.FetchedMessage{"INBOX": {intakeMessage("INBOX", "disabled-1", 1)}},
	}
	enc := sso.NewSecretEncryption("test-server-secret-with-sufficient-length-for-derivation")
	es := NewEmailScheduler(db, email.NewCredentialManager(db, enc), blobstore.NewLocal(t.TempDir()))
	es.SetCommentService(services.NewCommentService(db))
	es.providerForChannel = func(context.Context, int) (email.Provider, *models.ChannelConfig, error) {
		return &fakeProvider{client: client}, &models.ChannelConfig{
			EmailMailbox:     "INBOX",
			EmailWorkspaceID: data.WorkspaceID,
			EmailItemTypeID:  &itemTypeID,
		}, nil
	}

	if ok := es.processChannel(ctx, channelInfo{ID: channelID, Name: "Legacy mailbox"}); !ok {
		t.Fatal("processChannel reported failure")
	}
	var itemCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE channel_id = ?`, channelID).Scan(&itemCount); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if itemCount != 0 {
		t.Fatalf("items = %d, want 0 for a disabled intake", itemCount)
	}
}

// A second poll with nothing new keeps the per-intake watermarks and does not
// duplicate items.
func TestEmailScheduler_MultiIntakeMailboxIsIdempotentAcrossPolls(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	ctx := context.Background()

	var itemTypeID int
	if err := db.QueryRowContext(ctx, `SELECT id FROM item_types WHERE is_default = true ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("load default item type: %v", err)
	}
	var channelID int
	if err := db.QueryRowContext(ctx, `
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Shared mailbox', 'email', 'inbound', 'enabled', '{}')
		RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("insert mailbox channel: %v", err)
	}
	if _, err := db.ExecWriteContext(ctx, `
		INSERT INTO intakes (mailbox_id, folder, workspace_id, item_type_id, status)
		VALUES (?, 'INBOX', ?, ?, 'enabled')
	`, channelID, data.WorkspaceID, itemTypeID); err != nil {
		t.Fatalf("insert intake: %v", err)
	}

	client := &folderIMAPClient{
		uidValidity: 5,
		byFolder:    map[string][]*email.FetchedMessage{"INBOX": {intakeMessage("INBOX", "once-1", 1)}},
	}
	enc := sso.NewSecretEncryption("test-server-secret-with-sufficient-length-for-derivation")
	es := NewEmailScheduler(db, email.NewCredentialManager(db, enc), blobstore.NewLocal(t.TempDir()))
	es.SetCommentService(services.NewCommentService(db))
	es.providerForChannel = func(context.Context, int) (email.Provider, *models.ChannelConfig, error) {
		return &fakeProvider{client: client}, &models.ChannelConfig{EmailMailbox: "INBOX"}, nil
	}
	ch := channelInfo{ID: channelID, Name: "Shared mailbox"}

	for i := 0; i < 3; i++ {
		if ok := es.processChannel(ctx, ch); !ok {
			t.Fatalf("poll %d failed", i)
		}
	}
	var itemCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE channel_id = ?`, channelID).Scan(&itemCount); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if itemCount != 1 {
		t.Fatalf("items = %d, want 1 across repeated polls", itemCount)
	}
}

// An intake with no disposition inherits the mailbox default; a set value
// overrides it.
func TestEmailScheduler_IntakeDispositionInheritsAndOverridesMailboxDefault(t *testing.T) {
	tests := []struct {
		name        string
		intakeDisp  string
		wantMarked  bool
		wantDeleted bool
	}{
		{name: "empty inherits mailbox mark_read", intakeDisp: "", wantMarked: true, wantDeleted: false},
		{name: "intake delete overrides", intakeDisp: models.EmailDispositionDelete, wantMarked: false, wantDeleted: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tdb := testutils.CreateTestDB(t, true)
			t.Cleanup(func() { _ = tdb.Close() })
			data := tdb.SeedTestData(t)
			db := tdb.GetDatabase()
			ctx := context.Background()

			var itemTypeID int
			if err := db.QueryRowContext(ctx, `SELECT id FROM item_types WHERE is_default = true ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
				t.Fatalf("load default item type: %v", err)
			}
			var channelID int
			if err := db.QueryRowContext(ctx, `
				INSERT INTO channels (name, type, direction, status, config)
				VALUES ('Mailbox', 'email', 'inbound', 'enabled', '{}')
				RETURNING id
			`).Scan(&channelID); err != nil {
				t.Fatalf("insert channel: %v", err)
			}
			if _, err := db.ExecWriteContext(ctx, `
				INSERT INTO intakes (mailbox_id, folder, workspace_id, item_type_id, processing_disposition, status)
				VALUES (?, 'INBOX', ?, ?, ?, 'enabled')
			`, channelID, data.WorkspaceID, itemTypeID, tt.intakeDisp); err != nil {
				t.Fatalf("insert intake: %v", err)
			}

			client := &folderIMAPClient{
				uidValidity: 1,
				byFolder:    map[string][]*email.FetchedMessage{"INBOX": {intakeMessage("INBOX", "disp-1", 1)}},
			}
			enc := sso.NewSecretEncryption("test-server-secret-with-sufficient-length-for-derivation")
			es := NewEmailScheduler(db, email.NewCredentialManager(db, enc), blobstore.NewLocal(t.TempDir()))
			es.SetCommentService(services.NewCommentService(db))
			es.providerForChannel = func(context.Context, int) (email.Provider, *models.ChannelConfig, error) {
				return &fakeProvider{client: client}, &models.ChannelConfig{
					EmailMailbox:               "INBOX",
					EmailProcessingDisposition: models.EmailDispositionMarkRead,
				}, nil
			}

			if ok := es.processChannel(ctx, channelInfo{ID: channelID, Name: "Mailbox"}); !ok {
				t.Fatal("processChannel failed")
			}
			if (len(client.markedRead) > 0) != tt.wantMarked || (len(client.deleted) > 0) != tt.wantDeleted {
				t.Fatalf("marked=%v deleted=%v, want marked=%v deleted=%v",
					client.markedRead, client.deleted, tt.wantMarked, tt.wantDeleted)
			}
		})
	}
}

// A portal-linked intake routes by its own workspace and item type while
// attaching the portal's system Email request type as the email marker.
func TestEmailScheduler_PortalIntakeRoutesByIntakeWorkspaceAndType(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	ctx := context.Background()

	var defaultItemTypeID, overrideItemTypeID int
	if err := db.QueryRowContext(ctx, `SELECT id FROM item_types WHERE is_default = true ORDER BY id LIMIT 1`).Scan(&defaultItemTypeID); err != nil {
		t.Fatalf("load default item type: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM item_types WHERE id != ? ORDER BY id LIMIT 1`, defaultItemTypeID).Scan(&overrideItemTypeID); err != nil {
		t.Fatalf("load override item type: %v", err)
	}

	portalCfg, err := json.Marshal(models.ChannelConfig{
		PortalSlug:         "intake",
		PortalWorkspaceIDs: []int{data.WorkspaceID},
	})
	if err != nil {
		t.Fatalf("marshal portal config: %v", err)
	}
	var portalID int
	if err := db.QueryRowContext(ctx, `
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Intake portal', 'portal', 'inbound', 'enabled', ?)
		RETURNING id
	`, string(portalCfg)).Scan(&portalID); err != nil {
		t.Fatalf("insert portal: %v", err)
	}

	var channelID int
	if err := db.QueryRowContext(ctx, `
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Portal mailbox', 'email', 'inbound', 'enabled', '{}')
		RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("insert mailbox channel: %v", err)
	}
	if _, err := db.ExecWriteContext(ctx, `
		INSERT INTO intakes (mailbox_id, folder, workspace_id, portal_channel_id, item_type_id, status)
		VALUES (?, 'INBOX', ?, ?, ?, 'enabled')
	`, channelID, data.WorkspaceID, portalID, overrideItemTypeID); err != nil {
		t.Fatalf("insert portal intake: %v", err)
	}

	client := &folderIMAPClient{
		uidValidity: 3,
		byFolder:    map[string][]*email.FetchedMessage{"INBOX": {intakeMessage("INBOX", "portal-type-1", 1)}},
	}
	enc := sso.NewSecretEncryption("test-server-secret-with-sufficient-length-for-derivation")
	es := NewEmailScheduler(db, email.NewCredentialManager(db, enc), blobstore.NewLocal(t.TempDir()))
	es.SetCommentService(services.NewCommentService(db))
	es.providerForChannel = func(context.Context, int) (email.Provider, *models.ChannelConfig, error) {
		return &fakeProvider{client: client}, &models.ChannelConfig{EmailMailbox: "INBOX"}, nil
	}

	if ok := es.processChannel(ctx, channelInfo{ID: channelID, Name: "Portal mailbox"}); !ok {
		t.Fatal("processChannel reported failure")
	}

	var itemTypeID, workspaceID int
	var requestTypeID sql.NullInt64
	if err := db.QueryRowContext(ctx,
		`SELECT item_type_id, workspace_id, request_type_id FROM items WHERE channel_id = ?`, channelID,
	).Scan(&itemTypeID, &workspaceID, &requestTypeID); err != nil {
		t.Fatalf("load item: %v", err)
	}
	if itemTypeID != overrideItemTypeID {
		t.Errorf("item type = %d, want intake item type %d", itemTypeID, overrideItemTypeID)
	}
	if workspaceID != data.WorkspaceID {
		t.Errorf("workspace = %d, want intake workspace %d", workspaceID, data.WorkspaceID)
	}
	if !requestTypeID.Valid {
		t.Error("portal-linked item must carry the portal's system Email request type")
	}
}
