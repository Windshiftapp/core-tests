//go:build test

package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/emailutil"
	"windshift/internal/smtp"
	"windshift/internal/testutils"
)

// renderingThreadedSender renders the embedded default template for the named
// template so tests can assert on the actual customer-visible email body,
// rather than on an opaque data struct.
type renderingThreadedSender struct {
	configured bool
	failNext   error
	sent       []smtp.ThreadedEmailParams
}

func (s *renderingThreadedSender) IsSMTPConfigured() bool { return s.configured }
func (s *renderingThreadedSender) SendThreadedEmail(params smtp.ThreadedEmailParams) error {
	return s.SendThreadedEmailContext(context.Background(), params)
}
func (s *renderingThreadedSender) SendThreadedEmailContext(_ context.Context, params smtp.ThreadedEmailParams) error {
	if s.failNext != nil {
		err := s.failNext
		s.failNext = nil
		return err
	}
	s.sent = append(s.sent, params)
	return nil
}
func (s *renderingThreadedSender) RenderEmail(name string, data any) (string, string, string, error) {
	for _, tmpl := range emailutil.DefaultTemplates() {
		if tmpl.Name != name {
			continue
		}
		subject, _, err := emailutil.RenderTemplates(tmpl.Subject, tmpl.Subject, data)
		if err != nil {
			return "", "", "", err
		}
		enriched := emailutil.EnrichWithSubject(data, subject)
		htmlBody, textBody, err := emailutil.RenderTemplates(tmpl.HTMLBody, tmpl.TextBody, enriched)
		return subject, htmlBody, textBody, err
	}
	return "", "", "", fmt.Errorf("unknown email template %q", name)
}

type intakeConfirmationFixture struct {
	db              database.Database
	sender          *renderingThreadedSender
	service         *EmailReplyService
	workspaceID     int
	emailChannelID  int
	portalChannelID int
	intakeID        int
	itemID          int
	customerID      int
	userID          int
	baseURL         string
}

// newIntakeConfirmationFixture builds an email-originated ticket. When
// withPortal is true the intake links to a portal channel with slug "acme" and
// the item can be reached at a portal request URL.
func newIntakeConfirmationFixture(t *testing.T, withPortal bool) *intakeConfirmationFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Confirmation WS', 'CONF') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	var emailChannelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Support inbox', 'email', 'inbound', 'enabled', '{"imap_username":"support@example.test"}')
		RETURNING id`).Scan(&emailChannelID); err != nil {
		t.Fatalf("insert email channel: %v", err)
	}
	var portalChannelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Acme portal', 'portal', 'inbound', 'enabled', '{"portal_slug":"acme"}')
		RETURNING id`).Scan(&portalChannelID); err != nil {
		t.Fatalf("insert portal channel: %v", err)
	}
	var customerID int
	if err := db.QueryRow(`
		INSERT INTO portal_customers (name, email, created_via)
		VALUES ('Pat Customer', 'pat@example.test', 'email-intake')
		RETURNING id`).Scan(&customerID); err != nil {
		t.Fatalf("insert portal customer: %v", err)
	}
	var userID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('agent@example.test', 'agent', 'Ann', 'Agent')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	var portalChannel *int
	if withPortal {
		portalChannel = &portalChannelID
	}
	var intakeID int
	if err := db.QueryRow(`
		INSERT INTO intakes (mailbox_id, folder, workspace_id, portal_channel_id, send_confirmation, status)
		VALUES (?, 'INBOX', ?, ?, true, 'enabled')
		RETURNING id`, emailChannelID, workspaceID, portalChannel).Scan(&intakeID); err != nil {
		t.Fatalf("insert intake: %v", err)
	}

	var itemID int
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, channel_id, creator_portal_customer_id, intake_id, frac_index)
		VALUES (?, 7, 'Broken login', 'cannot sign in', ?, ?, ?, ?)
		RETURNING id`,
		workspaceID, emailChannelID, customerID, intakeID, testutils.NextTestFracIndex()).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO email_message_tracking (channel_id, message_id, dedup_key, from_email, from_name, subject, item_id, processed_at)
		VALUES (?, ?, ?, 'pat@example.test', 'Pat Customer', 'Broken login', ?, ?)`,
		emailChannelID, fmt.Sprintf("<orig-%d@example.test>", itemID), fmt.Sprintf("dedup-%d", itemID), itemID, time.Now().UTC()); err != nil {
		t.Fatalf("insert tracking: %v", err)
	}

	sender := &renderingThreadedSender{configured: true}
	service := NewEmailReplyService(db, sender)
	service.SetBaseURL("https://app.example.com")

	return &intakeConfirmationFixture{
		db:              db,
		sender:          sender,
		service:         service,
		workspaceID:     workspaceID,
		emailChannelID:  emailChannelID,
		portalChannelID: portalChannelID,
		intakeID:        intakeID,
		itemID:          itemID,
		customerID:      customerID,
		userID:          userID,
		baseURL:         "https://app.example.com",
	}
}

func (f *intakeConfirmationFixture) portalURL() string {
	return fmt.Sprintf("%s/portal/acme?view=requests&id=%d", f.baseURL, f.itemID)
}

// portalURLHTML is the HTML-escaped form html/template emits inside href.
func (f *intakeConfirmationFixture) portalURLHTML() string {
	return fmt.Sprintf("%s/portal/acme?view=requests&amp;id=%d", f.baseURL, f.itemID)
}

// Enqueueing a receipt must not touch SMTP: the outbox pass delivers it, and
// the delivery records the receipt's Message-ID so a reply threads onto the
// ticket. Re-processing the same email must not queue a second receipt.
func TestEnqueueIntakeConfirmationQueuesDeliverableReceipt(t *testing.T) {
	f := newIntakeConfirmationFixture(t, true)

	if err := f.service.EnqueueIntakeConfirmation(f.itemID, f.customerID); err != nil {
		t.Fatalf("EnqueueIntakeConfirmation: %v", err)
	}
	if len(f.sender.sent) != 0 {
		t.Fatalf("sent %d emails during enqueue, want 0 (the outbox delivers it)", len(f.sender.sent))
	}

	var kind, toEmail, toName, subject, replyToEmail, inReplyTo, messageID string
	var commentID sql.NullInt64
	var queuedItemID int
	if err := f.db.QueryRow(`
		SELECT kind, comment_id, item_id, to_email, to_name, subject, reply_to_email, in_reply_to, message_id
		FROM email_reply_outbox
	`).Scan(&kind, &commentID, &queuedItemID, &toEmail, &toName, &subject, &replyToEmail, &inReplyTo, &messageID); err != nil {
		t.Fatalf("load queued confirmation: %v", err)
	}
	if kind != "intake_confirmation" || commentID.Valid {
		t.Fatalf("queued row = kind %q comment_id %v, want intake_confirmation/NULL", kind, commentID)
	}
	if queuedItemID != f.itemID || toEmail != "pat@example.test" || toName != "Pat Customer" {
		t.Fatalf("queued row = item %d to %s <%s>, want %d to pat@example.test (Pat Customer)", queuedItemID, toEmail, toName, f.itemID)
	}
	if subject != "We've received your submission (CONF-7)" {
		t.Fatalf("subject = %q, want %q", subject, "We've received your submission (CONF-7)")
	}
	if replyToEmail != "support@example.test" {
		t.Fatalf("reply-to = %q, want the intake mailbox support@example.test", replyToEmail)
	}
	if inReplyTo != "" {
		t.Fatalf("in_reply_to = %q, want an empty (new thread) value", inReplyTo)
	}

	// Re-processing the same email must not queue a second receipt.
	if err := f.service.EnqueueIntakeConfirmation(f.itemID, f.customerID); err != nil {
		t.Fatalf("second EnqueueIntakeConfirmation: %v", err)
	}
	var queued int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM email_reply_outbox WHERE kind = 'intake_confirmation'`).Scan(&queued); err != nil {
		t.Fatalf("count queued confirmations: %v", err)
	}
	if queued != 1 {
		t.Fatalf("queued confirmations = %d, want 1", queued)
	}

	if _, err := f.service.ProcessPendingReplies(context.Background(), 10); err != nil {
		t.Fatalf("ProcessPendingReplies: %v", err)
	}
	if len(f.sender.sent) != 1 {
		t.Fatalf("sent %d emails, want 1", len(f.sender.sent))
	}
	msg := f.sender.sent[0]
	if msg.MessageID != messageID {
		t.Fatalf("delivered Message-ID = %q, want the queued %q", msg.MessageID, messageID)
	}
	if !strings.Contains(msg.HTMLBody, f.portalURLHTML()) {
		t.Fatalf("HTML body missing portal request link %q:\n%s", f.portalURLHTML(), msg.HTMLBody)
	}
	if !strings.Contains(msg.TextBody, f.portalURL()) {
		t.Fatalf("text body missing portal request link %q:\n%s", f.portalURL(), msg.TextBody)
	}

	// The receipt records its own Message-ID so a reply to it threads onto the
	// ticket instead of opening a new one.
	var direction string
	var trackingItemID int
	var trackingCommentID sql.NullInt64
	if err := f.db.QueryRow(`
		SELECT direction, item_id, comment_id FROM email_message_tracking
		WHERE channel_id = ? AND message_id = ?
	`, f.emailChannelID, msg.MessageID).Scan(&direction, &trackingItemID, &trackingCommentID); err != nil {
		t.Fatalf("load receipt tracking row: %v", err)
	}
	if direction != "outbound" || trackingItemID != f.itemID || trackingCommentID.Valid {
		t.Fatalf("tracking row = direction %q item %d comment %v, want outbound/%d/NULL", direction, trackingItemID, trackingCommentID, f.itemID)
	}
}

func TestEnqueueIntakeConfirmationOmitsPortalLinkForWorkspaceIntake(t *testing.T) {
	f := newIntakeConfirmationFixture(t, false)

	if err := f.service.EnqueueIntakeConfirmation(f.itemID, f.customerID); err != nil {
		t.Fatalf("EnqueueIntakeConfirmation: %v", err)
	}
	if _, err := f.service.ProcessPendingReplies(context.Background(), 10); err != nil {
		t.Fatalf("ProcessPendingReplies: %v", err)
	}
	if len(f.sender.sent) != 1 {
		t.Fatalf("sent %d emails, want 1", len(f.sender.sent))
	}
	msg := f.sender.sent[0]
	if strings.Contains(msg.HTMLBody, "/portal/") {
		t.Fatalf("workspace-only intake receipt must not contain a portal link:\n%s", msg.HTMLBody)
	}
	if !strings.Contains(msg.HTMLBody, "CONF-7") {
		t.Fatalf("receipt should still identify the created request:\n%s", msg.HTMLBody)
	}
}

func TestEnqueueIntakeConfirmationSkipsWhenSMTPUnconfigured(t *testing.T) {
	f := newIntakeConfirmationFixture(t, true)
	f.sender.configured = false

	if err := f.service.EnqueueIntakeConfirmation(f.itemID, f.customerID); err != nil {
		t.Fatalf("EnqueueIntakeConfirmation: %v", err)
	}
	var queued int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM email_reply_outbox`).Scan(&queued); err != nil {
		t.Fatalf("count outbox rows: %v", err)
	}
	if queued != 0 {
		t.Fatalf("queued %d rows, want none without SMTP", queued)
	}
}

// A transient SMTP failure leaves the receipt in the outbox for the normal
// retry/backoff path rather than dropping it.
func TestEnqueueIntakeConfirmationRetriesAfterTransientFailure(t *testing.T) {
	f := newIntakeConfirmationFixture(t, true)
	if err := f.service.EnqueueIntakeConfirmation(f.itemID, f.customerID); err != nil {
		t.Fatalf("EnqueueIntakeConfirmation: %v", err)
	}

	f.sender.failNext = errors.New("smtp unavailable")
	if _, err := f.service.ProcessPendingReplies(context.Background(), 10); err == nil {
		t.Fatal("ProcessPendingReplies returned no error for a failed send")
	}
	var attempts int
	var lastError sql.NullString
	var deliveredAt sql.NullTime
	if err := f.db.QueryRow(`SELECT attempt_count, last_error, delivered_at FROM email_reply_outbox`).Scan(&attempts, &lastError, &deliveredAt); err != nil {
		t.Fatalf("read failed confirmation: %v", err)
	}
	if attempts != 1 || !lastError.Valid || deliveredAt.Valid {
		t.Fatalf("after failure = attempts %d error %v delivered %v, want 1/<error>/NULL", attempts, lastError, deliveredAt)
	}

	// Clear the retry backoff so the next pass retries immediately.
	if _, err := f.db.Exec(`UPDATE email_reply_outbox SET next_attempt_at = CURRENT_TIMESTAMP`); err != nil {
		t.Fatalf("clear retry backoff: %v", err)
	}
	if _, err := f.service.ProcessPendingReplies(context.Background(), 10); err != nil {
		t.Fatalf("retry ProcessPendingReplies: %v", err)
	}
	if len(f.sender.sent) != 1 {
		t.Fatalf("sent %d emails after retry, want 1", len(f.sender.sent))
	}
}

func TestHandleCommentCreatedIncludesPortalRequestLink(t *testing.T) {
	f := newIntakeConfirmationFixture(t, true)

	var commentID int
	if err := f.db.QueryRow(`
		INSERT INTO comments (item_id, author_id, content, created_at, updated_at)
		VALUES (?, ?, 'On it.', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id`, f.itemID, f.userID).Scan(&commentID); err != nil {
		t.Fatalf("insert comment: %v", err)
	}

	if err := f.service.HandleCommentCreated(HandleCommentParams{
		CommentID: commentID,
		ItemID:    f.itemID,
		AuthorID:  f.userID,
		Content:   "On it.",
	}); err != nil {
		t.Fatalf("HandleCommentCreated: %v", err)
	}
	if len(f.sender.sent) != 1 {
		t.Fatalf("sent %d replies, want 1", len(f.sender.sent))
	}
	if !strings.Contains(f.sender.sent[0].HTMLBody, f.portalURLHTML()) {
		t.Fatalf("reply HTML body missing portal request link %q:\n%s", f.portalURLHTML(), f.sender.sent[0].HTMLBody)
	}
}
