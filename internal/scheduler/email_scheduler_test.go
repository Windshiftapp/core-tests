//go:build test

package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"windshift/internal/email"
	"windshift/internal/models"
	"windshift/internal/sso"
	"windshift/internal/testutils"
)

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
	es := NewEmailScheduler(db, email.NewCredentialManager(db, enc), t.TempDir())

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
		if state.LastUID != 0 {
			t.Fatalf("poll %d: watermark advanced early to %d", i, state.LastUID)
		}
	}

	// Poll maxDeliveryAttempts: the message is treated as poison and dropped.
	if ok := es.processChannel(ctx, ch); ok {
		t.Fatalf("drop poll: expected processChannel to report failure")
	}
	state, err := es.getOrCreateChannelState(ctx, channelID)
	if err != nil {
		t.Fatalf("read state after drop: %v", err)
	}
	if state.LastUID != int(poisonUID) {
		t.Fatalf("watermark did not advance past poison: last_uid = %d, want %d", state.LastUID, poisonUID)
	}
	if state.ErrorCount != 0 {
		t.Fatalf("error_count not reset after drop: %d", state.ErrorCount)
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
