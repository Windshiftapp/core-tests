//go:build test

package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/smtp"
	"windshift/internal/testutils"
)

type fakeThreadedSender struct {
	configured bool
	sent       []smtp.ThreadedEmailParams
	failWith   error
}

func (s *fakeThreadedSender) IsSMTPConfigured() bool { return s.configured }
func (s *fakeThreadedSender) SendThreadedEmail(params smtp.ThreadedEmailParams) error {
	return s.SendThreadedEmailContext(context.Background(), params)
}
func (s *fakeThreadedSender) SendThreadedEmailContext(_ context.Context, params smtp.ThreadedEmailParams) error {
	if s.failWith != nil {
		return s.failWith
	}
	s.sent = append(s.sent, params)
	return nil
}
func (s *fakeThreadedSender) RenderEmail(templateName string, data any) (string, string, string, error) {
	return "subject", "<p>html</p>", "text", nil
}

type customerNoticeFixture struct {
	db          database.Database
	sender      *fakeThreadedSender
	service     *EmailReplyService
	workspaceID int
	itemID      int
	channelID   int
	customerID  int
}

func newCustomerNoticeFixture(t *testing.T, configured bool) *customerNoticeFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Notice WS', 'NOTICE') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	var channelID int
	if err := db.QueryRow(`INSERT INTO channels (name, type, direction, status) VALUES ('Support inbox', 'email', 'inbound', 'enabled') RETURNING id`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	var customerID int
	if err := db.QueryRow(`INSERT INTO portal_customers (name, email, created_via) VALUES ('Pat Customer', 'pat@example.test', 'email-intake') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatalf("insert portal customer: %v", err)
	}
	var itemID int
	if err := db.QueryRow(`INSERT INTO items (workspace_id, workspace_item_number, title, description, channel_id, creator_portal_customer_id, frac_index) VALUES (?, 4, 'Broken login', 'd', ?, ?, ?) RETURNING id`,
		workspaceID, channelID, customerID, testutils.NextTestFracIndex()).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	// Thread anchor: the customer's original inbound mail.
	if _, err := db.Exec(`INSERT INTO email_message_tracking (channel_id, message_id, dedup_key, from_email, from_name, subject, item_id, processed_at) VALUES (?, ?, 'dedup-1', 'pat@example.test', 'Pat Customer', 'Broken login', ?, ?)`,
		channelID, fmt.Sprintf("<orig-%d@example.test>", itemID), itemID, time.Now().UTC()); err != nil {
		t.Fatalf("insert tracking: %v", err)
	}

	sender := &fakeThreadedSender{configured: configured}
	service := NewEmailReplyService(db, sender)
	return &customerNoticeFixture{db: db, sender: sender, service: service, workspaceID: workspaceID, itemID: itemID, channelID: channelID, customerID: customerID}
}

func TestSendAutomationNoticeDeliversThreadedEmail(t *testing.T) {
	f := newCustomerNoticeFixture(t, true)

	delivered, skip, err := f.service.SendAutomationNotice(f.itemID, 0, "Custom subject", "Here is an update")
	if err != nil {
		t.Fatalf("notice: %v", err)
	}
	if !delivered || skip != "" {
		t.Fatalf("delivered=%v skip=%q, want true/empty", delivered, skip)
	}
	if len(f.sender.sent) != 1 {
		t.Fatalf("sent %d emails, want 1", len(f.sender.sent))
	}
	email := f.sender.sent[0]
	if email.ToEmail != "pat@example.test" || email.ToName != "Pat Customer" {
		t.Fatalf("recipient = %s <%s>, want pat@example.test (Pat Customer)", email.ToEmail, email.ToName)
	}
	if email.Subject != "Re: Custom subject" {
		t.Fatalf("subject = %q, want Re: Custom subject", email.Subject)
	}
	anchor := fmt.Sprintf("<orig-%d@example.test>", f.itemID)
	if email.InReplyTo != anchor {
		t.Fatalf("in_reply_to = %q, want the thread anchor %q", email.InReplyTo, anchor)
	}
	if len(email.References) == 0 || email.References[len(email.References)-1] != anchor {
		t.Fatalf("references = %v, want the anchor as the last entry", email.References)
	}
	if email.MessageID == "" {
		t.Fatal("outbound email missing Message-ID")
	}
}

func TestSendAutomationNoticeKeepsGeneratedSubjectWhenUnset(t *testing.T) {
	f := newCustomerNoticeFixture(t, true)
	if delivered, _, err := f.service.SendAutomationNotice(f.itemID, 0, "", "body"); err != nil || !delivered {
		t.Fatalf("delivered=%v err=%v, want true/nil", delivered, err)
	}
	if f.sender.sent[0].Subject != "Re: Broken login" {
		t.Fatalf("subject = %q, want Re: Broken login", f.sender.sent[0].Subject)
	}
}

func TestSendAutomationNoticeSkipsWithoutCustomer(t *testing.T) {
	f := newCustomerNoticeFixture(t, true)
	if _, err := f.db.Exec(`UPDATE items SET creator_portal_customer_id = NULL WHERE id = ?`, f.itemID); err != nil {
		t.Fatalf("clear customer: %v", err)
	}
	delivered, skip, err := f.service.SendAutomationNotice(f.itemID, 0, "", "body")
	if err != nil || delivered || skip != "no_customer" {
		t.Fatalf("delivered=%v skip=%q err=%v, want false/no_customer/nil", delivered, skip, err)
	}
	if len(f.sender.sent) != 0 {
		t.Fatal("skip path must not send email")
	}
}

// TestSendAutomationNoticeThreadsForPortalTicket verifies the lazy anchor
// ensure on the notice path: a portal-originated ticket with no tracking rows
// (pre-upgrade backlog) gains its anchor and the notice threads from it,
// instead of being skipped as no_thread (WI-1546).
func TestSendAutomationNoticeThreadsForPortalTicket(t *testing.T) {
	f := newCustomerNoticeFixture(t, true)
	if _, err := f.db.Exec(`UPDATE items SET channel_id = ? WHERE id = ?`, f.portalChannelID(t), f.itemID); err != nil {
		t.Fatalf("move item to portal channel: %v", err)
	}
	if _, err := f.db.Exec(`DELETE FROM email_message_tracking WHERE item_id = ?`, f.itemID); err != nil {
		t.Fatalf("clear tracking: %v", err)
	}

	delivered, skip, err := f.service.SendAutomationNotice(f.itemID, 0, "", "status update")
	if err != nil || !delivered || skip != "" {
		t.Fatalf("delivered=%v skip=%q err=%v, want true/empty/nil", delivered, skip, err)
	}
	if len(f.sender.sent) != 1 {
		t.Fatalf("sent %d notices, want 1", len(f.sender.sent))
	}
	wantAnchor := fmt.Sprintf("<ws-item-%d@windshift.local>", f.itemID)
	if f.sender.sent[0].InReplyTo != wantAnchor {
		t.Fatalf("InReplyTo = %q, want anchor %q", f.sender.sent[0].InReplyTo, wantAnchor)
	}

	// The minted anchor stays a root: no from identity, outbound direction.
	var fromEmail, direction string
	var commentID *int
	if err := f.db.QueryRow(`
		SELECT from_email, direction, comment_id FROM email_message_tracking
		WHERE item_id = ? AND message_id = ?
	`, f.itemID, wantAnchor).Scan(&fromEmail, &direction, &commentID); err != nil {
		t.Fatalf("load minted anchor: %v", err)
	}
	if fromEmail != "" || direction != "outbound" || commentID != nil {
		t.Fatalf("anchor row shape wrong: from=%q dir=%q comment=%v", fromEmail, direction, commentID)
	}
}

// portalChannelID seeds a portal channel for the fixture's workspace.
func (f *customerNoticeFixture) portalChannelID(t *testing.T) int {
	t.Helper()
	var id int
	if err := f.db.QueryRow(`INSERT INTO channels (name, type, direction, status) VALUES ('Portal', 'portal', 'inbound', 'enabled') RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("insert portal channel: %v", err)
	}
	return id
}

func TestSendAutomationNoticeSkipsWithoutThread(t *testing.T) {
	f := newCustomerNoticeFixture(t, true)
	if _, err := f.db.Exec(`DELETE FROM email_message_tracking WHERE item_id = ?`, f.itemID); err != nil {
		t.Fatalf("clear tracking: %v", err)
	}
	_, skip, err := f.service.SendAutomationNotice(f.itemID, 0, "", "body")
	if err != nil || skip != "no_thread" {
		t.Fatalf("skip=%q err=%v, want no_thread/nil", skip, err)
	}
}

func TestSendAutomationNoticeSkipsWhenSMTPUnconfigured(t *testing.T) {
	f := newCustomerNoticeFixture(t, false)
	_, skip, err := f.service.SendAutomationNotice(f.itemID, 0, "", "body")
	if err != nil || skip != "smtp_not_configured" {
		t.Fatalf("skip=%q err=%v, want smtp_not_configured/nil", skip, err)
	}
}

func TestSendAutomationNoticeSurfacesTransportErrors(t *testing.T) {
	f := newCustomerNoticeFixture(t, true)
	f.sender.failWith = fmt.Errorf("smtp 550")
	if _, _, err := f.service.SendAutomationNotice(f.itemID, 0, "", "body"); err == nil {
		t.Fatal("transport error swallowed, want error")
	}
}

// TestSendAutomationNoticeRecordsMessageIDForReplyThreading covers the bug hunt
// finding that notice replies became separate tickets: the notice's own
// Message-ID must be tracked so an In-Reply-To-only reply resolves to the item.
func TestSendAutomationNoticeRecordsMessageIDForReplyThreading(t *testing.T) {
	f := newCustomerNoticeFixture(t, true)
	if delivered, _, err := f.service.SendAutomationNotice(f.itemID, 0, "Update", "body"); err != nil || !delivered {
		t.Fatalf("delivered=%v err=%v, want true/nil", delivered, err)
	}
	messageID := f.sender.sent[0].MessageID
	if messageID == "" {
		t.Fatal("notice has no Message-ID")
	}

	var itemID int
	var direction string
	if err := f.db.QueryRow(
		`SELECT item_id, direction FROM email_message_tracking WHERE message_id = ?`, messageID,
	).Scan(&itemID, &direction); err != nil {
		t.Fatalf("notice Message-ID was not recorded: %v", err)
	}
	if itemID != f.itemID {
		t.Fatalf("tracked item_id = %d, want %d", itemID, f.itemID)
	}
	if direction != "outbound" {
		t.Fatalf("tracked direction = %q, want outbound", direction)
	}
}

var _ = models.Item{}
