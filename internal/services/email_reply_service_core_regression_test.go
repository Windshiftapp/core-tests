package services

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/smtp"
)

type retryingThreadedSender struct {
	fail  bool
	sends int
}

type blockingThreadedSender struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	sends   atomic.Int32
}

func (*blockingThreadedSender) IsSMTPConfigured() bool { return true }

func (s *blockingThreadedSender) SendThreadedEmail(smtp.ThreadedEmailParams) error {
	s.sends.Add(1)
	s.once.Do(func() { close(s.started) })
	<-s.release
	return nil
}

func (*blockingThreadedSender) RenderEmail(string, interface{}) (string, string, string, error) {
	return "subject", "html", "text", nil
}

func (s *retryingThreadedSender) IsSMTPConfigured() bool { return true }

func (s *retryingThreadedSender) SendThreadedEmail(smtp.ThreadedEmailParams) error {
	s.sends++
	if s.fail {
		return errors.New("temporary SMTP failure")
	}
	return nil
}

func (*retryingThreadedSender) RenderEmail(string, interface{}) (string, string, string, error) {
	return "subject", "html", "text", nil
}

func TestEmailReplyOutboxRetriesTransientFailure(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "reply-outbox.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	var workspaceID, channelID, itemID, commentID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Test', 'TST') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO channels (name, type, direction) VALUES ('Email', 'email', 'inbound') RETURNING id`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	itemID64, err := CreateItem(db, ItemCreationParams{WorkspaceID: workspaceID, Title: "Item"})
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}
	itemID = int(itemID64)
	if err := db.QueryRow(`INSERT INTO comments (item_id, content) VALUES (?, 'Reply') RETURNING id`, itemID).Scan(&commentID); err != nil {
		t.Fatalf("insert comment: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO email_reply_outbox (
			comment_id, channel_id, item_id, to_email, subject, html_body,
			text_body, message_id, references_json, from_email, next_attempt_at
		) VALUES (?, ?, ?, 'customer@example.com', 'Re: Item', 'html', 'text',
		          '<reply@example.com>', '[]', 'team@example.com', CURRENT_TIMESTAMP)
	`, commentID, channelID, itemID); err != nil {
		t.Fatalf("insert outbox row: %v", err)
	}

	sender := &retryingThreadedSender{fail: true}
	service := NewEmailReplyService(db, sender)
	delivered, err := service.ProcessPendingReplies(10)
	if err == nil || delivered != 0 {
		t.Fatalf("first processing = (%d, %v), want (0, error)", delivered, err)
	}
	var attempts int
	if err := db.QueryRow(`SELECT attempt_count FROM email_reply_outbox WHERE comment_id = ?`, commentID).Scan(&attempts); err != nil {
		t.Fatalf("load attempt count: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempt count = %d, want 1", attempts)
	}

	if _, err := db.ExecWrite(`UPDATE email_reply_outbox SET next_attempt_at = CURRENT_TIMESTAMP WHERE comment_id = ?`, commentID); err != nil {
		t.Fatalf("make reply due: %v", err)
	}
	sender.fail = false
	delivered, err = service.ProcessPendingReplies(10)
	if err != nil || delivered != 1 {
		t.Fatalf("retry processing = (%d, %v), want (1, nil)", delivered, err)
	}
	var tracked int
	if err := db.QueryRow(`SELECT COUNT(*) FROM email_message_tracking WHERE comment_id = ? AND direction = 'outbound'`, commentID).Scan(&tracked); err != nil {
		t.Fatalf("count tracking: %v", err)
	}
	if tracked != 1 || sender.sends != 2 {
		t.Fatalf("tracking/sends = %d/%d, want 1/2", tracked, sender.sends)
	}
}

func TestEmailReplyOutboxLeasePreventsCrossInstanceDuplicateSend(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "reply-lease.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	var workspaceID, channelID, itemID, commentID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Test', 'TST') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO channels (name, type, direction) VALUES ('Email', 'email', 'inbound') RETURNING id`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	itemID64, err := CreateItem(db, ItemCreationParams{WorkspaceID: workspaceID, Title: "Item"})
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}
	itemID = int(itemID64)
	if err := db.QueryRow(`INSERT INTO comments (item_id, content) VALUES (?, 'Reply') RETURNING id`, itemID).Scan(&commentID); err != nil {
		t.Fatalf("insert comment: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO email_reply_outbox (
			comment_id, channel_id, item_id, to_email, subject, html_body,
			text_body, message_id, references_json, from_email, next_attempt_at
		) VALUES (?, ?, ?, 'customer@example.com', 'Re: Item', 'html', 'text',
		          '<reply@example.com>', '[]', 'team@example.com', CURRENT_TIMESTAMP)
	`, commentID, channelID, itemID); err != nil {
		t.Fatalf("insert outbox row: %v", err)
	}

	sender := &blockingThreadedSender{started: make(chan struct{}), release: make(chan struct{})}
	first := NewEmailReplyService(db, sender)
	second := NewEmailReplyService(db, sender)
	firstResult := make(chan error, 1)
	go func() {
		_, processErr := first.ProcessPendingReplies(10)
		firstResult <- processErr
	}()
	select {
	case <-sender.started:
	case <-time.After(2 * time.Second):
		close(sender.release)
		t.Fatal("first outbox worker did not reach SMTP send")
	}

	delivered, err := second.ProcessPendingReplies(10)
	if err != nil || delivered != 0 {
		t.Fatalf("second instance processing = (%d, %v), want no claimed row", delivered, err)
	}
	close(sender.release)
	if err := <-firstResult; err != nil {
		t.Fatalf("first instance processing: %v", err)
	}
	if got := sender.sends.Load(); got != 1 {
		t.Fatalf("SMTP sends = %d, want exactly 1", got)
	}
}
