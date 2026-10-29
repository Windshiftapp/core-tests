//go:build test

package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/events"
	"windshift/internal/itemevents"
	"windshift/internal/smtp"
)

type durableReplySender struct {
	mu          sync.Mutex
	failRender  bool
	renderCalls int
	sendCalls   int
	rendered    chan error
}

func (*durableReplySender) IsSMTPConfigured() bool { return true }

func (s *durableReplySender) SendThreadedEmail(p smtp.ThreadedEmailParams) error {
	return s.SendThreadedEmailContext(context.Background(), p)
}
func (s *durableReplySender) SendThreadedEmailContext(_ context.Context, _ smtp.ThreadedEmailParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendCalls++
	return nil
}

func (s *durableReplySender) RenderEmail(string, any) (string, string, string, error) {
	s.mu.Lock()
	s.renderCalls++
	fail := s.failRender
	s.mu.Unlock()
	var err error
	if fail {
		err = errors.New("forced template failure")
	}
	select {
	case s.rendered <- err:
	default:
	}
	if err != nil {
		return "", "", "", err
	}
	return "rendered subject", "rendered html", "rendered text", nil
}

type durableReplyEnv struct {
	workspaceID, itemID, actorID, customerID, channelID int
}

func setupDurableReplyEnv(t *testing.T, db database.Database) durableReplyEnv {
	t.Helper()
	env := durableReplyEnv{}
	env.actorID = insertUser(t, db, "reply-actor@example.com", "reply-actor")
	env.workspaceID = insertWorkspace(t, db, "Reply workspace", "RPL")
	env.channelID = testInsertID(t, db, `
		INSERT INTO channels (name, type, direction, status)
		VALUES ('Inbound replies', 'email', 'inbound', 'enabled')
	`)
	env.customerID = testInsertID(t, db, `
		INSERT INTO portal_customers (name, email)
		VALUES ('Reply Customer', 'reply-customer@example.com')
	`)
	itemID, err := CreateItem(db, ItemCreationParams{WorkspaceID: env.workspaceID, Title: "Email request"})
	if err != nil {
		t.Fatalf("create email item: %v", err)
	}
	env.itemID = int(itemID)
	if _, err := db.Exec(`
		UPDATE items SET channel_id = ?, creator_portal_customer_id = ? WHERE id = ?
	`, env.channelID, env.customerID, env.itemID); err != nil {
		t.Fatalf("bind email item provenance: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO email_message_tracking (
			channel_id, message_id, dedup_key, from_email, from_name, subject,
			item_id, direction, processed_at
		) VALUES (?, '<incoming@example.com>', '<incoming@example.com>',
			'reply-customer@example.com', 'Reply Customer', 'Original subject', ?, 'inbound', CURRENT_TIMESTAMP)
	`, env.channelID, env.itemID); err != nil {
		t.Fatalf("insert inbound tracking: %v", err)
	}
	return env
}

func testInsertID(t *testing.T, db database.Database, query string, args ...any) int {
	t.Helper()
	var id int
	if err := db.QueryRow(query+" RETURNING id", args...).Scan(&id); err != nil {
		t.Fatalf("insert fixture: %v", err)
	}
	return id
}

func replyEngine(db database.Database, retryDelay time.Duration) *events.Engine {
	config := events.DefaultConfig()
	config.WorkerCount = 1
	config.PollInterval = 5 * time.Millisecond
	config.LeaseDuration = time.Second
	config.HandlerTimeout = 500 * time.Millisecond
	config.ReconcileBatch = 20
	config.MaxAttempts = 3
	config.BaseRetryDelay = retryDelay
	config.MaxRetryDelay = retryDelay
	config.RetentionInterval = 0
	return events.NewEngine(db, config)
}

func TestDurableEmailReplyRenderingFailureRecoversAfterRestart(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()
	env := setupDurableReplyEnv(t, db)
	sender := &durableReplySender{failRender: true, rendered: make(chan error, 4)}

	firstReplies := NewEmailReplyService(db, sender)
	firstEngine := replyEngine(db, time.Hour)
	if err := PrepareDurableEmailReplyEngine(context.Background(), firstEngine, firstReplies); err != nil {
		t.Fatalf("prepare first reply consumer: %v", err)
	}
	if err := firstEngine.Start(context.Background()); err != nil {
		t.Fatalf("start first reply engine: %v", err)
	}
	comments := NewCommentService(db)
	comments.SetEmailReplyService(firstReplies)
	created, err := comments.Create(CreateCommentParams{
		ItemID: env.itemID, AuthorID: env.actorID, ActorUserID: env.actorID,
		Content: "A durable customer reply",
	})
	if err != nil {
		t.Fatalf("create committed comment: %v", err)
	}
	select {
	case renderErr := <-sender.rendered:
		if renderErr == nil {
			t.Fatal("first durable render unexpectedly succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for failed durable render")
	}
	stopCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := firstEngine.Shutdown(stopCtx); err != nil {
		t.Fatalf("shutdown first reply engine: %v", err)
	}
	var outboxCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM email_reply_outbox WHERE comment_id = ?`, created.CommentID).Scan(&outboxCount); err != nil {
		t.Fatalf("count failed-render outbox: %v", err)
	}
	if outboxCount != 0 {
		t.Fatalf("failed render created %d outbox rows, want 0", outboxCount)
	}
	if _, err := db.Exec(`
		UPDATE domain_event_deliveries
		SET next_attempt_at = CURRENT_TIMESTAMP
		WHERE consumer_key = ?
	`, DurableEmailReplyConsumerKey); err != nil {
		t.Fatalf("advance durable retry window: %v", err)
	}

	sender.mu.Lock()
	sender.failRender = false
	sender.mu.Unlock()
	secondReplies := NewEmailReplyService(db, sender)
	secondEngine := replyEngine(db, 5*time.Millisecond)
	if err := PrepareDurableEmailReplyEngine(context.Background(), secondEngine, secondReplies); err != nil {
		t.Fatalf("prepare restarted reply consumer: %v", err)
	}
	if err := secondEngine.Start(context.Background()); err != nil {
		t.Fatalf("start restarted reply engine: %v", err)
	}
	select {
	case renderErr := <-sender.rendered:
		if renderErr != nil {
			t.Fatalf("restarted durable render: %v", renderErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for restarted durable render")
	}
	stopCtx2, stop2 := context.WithTimeout(context.Background(), time.Second)
	defer stop2()
	if err := secondEngine.Shutdown(stopCtx2); err != nil {
		t.Fatalf("shutdown restarted reply engine: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM email_reply_outbox WHERE comment_id = ?`, created.CommentID).Scan(&outboxCount); err != nil {
		t.Fatalf("count recovered outbox: %v", err)
	}
	if outboxCount != 1 {
		t.Fatalf("recovered outbox rows = %d, want 1", outboxCount)
	}

	var eventID int64
	if err := db.QueryRow(`
		SELECT id FROM domain_events
		WHERE event_type = 'item.comment_created' AND aggregate_id = ?
		ORDER BY id DESC LIMIT 1
	`, fmt.Sprint(env.itemID)).Scan(&eventID); err != nil {
		t.Fatalf("load committed comment event: %v", err)
	}
	event, err := events.NewStore(db).Event(context.Background(), eventID)
	if err != nil {
		t.Fatalf("load comment event: %v", err)
	}
	if err := (&DurableEmailReplyConsumer{replies: secondReplies}).Handle(context.Background(), *event); err != nil {
		t.Fatalf("replay committed comment event: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM email_reply_outbox WHERE comment_id = ?`, created.CommentID).Scan(&outboxCount); err != nil {
		t.Fatalf("count replayed outbox: %v", err)
	}
	if outboxCount != 1 {
		t.Fatalf("replayed outbox rows = %d, want 1", outboxCount)
	}

	delivered, err := secondReplies.ProcessPendingReplies(context.Background(), 10)
	if err != nil || delivered != 1 {
		t.Fatalf("deliver recovered reply = (%d, %v), want (1, nil)", delivered, err)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if sender.sendCalls != 1 {
		t.Fatalf("SMTP sends = %d, want 1", sender.sendCalls)
	}
}

func TestDurableEmailReplySuppressesIneligibleCommittedComments(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()
	env := setupDurableReplyEnv(t, db)
	sender := &durableReplySender{rendered: make(chan error, 4)}
	replies := NewEmailReplyService(db, sender)
	engine := replyEngine(db, 5*time.Millisecond)
	if err := PrepareDurableEmailReplyEngine(context.Background(), engine, replies); err != nil {
		t.Fatalf("prepare reply consumer: %v", err)
	}
	comments := NewCommentService(db)
	comments.SetEmailReplyService(replies)
	cases := []CreateCommentParams{
		{ItemID: env.itemID, AuthorID: env.actorID, ActorUserID: env.actorID, Content: "private", IsPrivate: true},
		{ItemID: env.itemID, PortalCustomerID: &env.customerID, Content: "portal"},
		{ItemID: env.itemID, AuthorID: env.actorID, ActorUserID: env.actorID, Content: "suppressed", SuppressNotifications: true},
	}
	for _, params := range cases {
		if _, err := comments.Create(params); err != nil {
			t.Fatalf("create ineligible comment %q: %v", params.Content, err)
		}
	}
	created, err := engine.Store().Reconcile(context.Background(), 20)
	if err != nil {
		t.Fatalf("reconcile ineligible comments: %v", err)
	}
	if created != int64(len(cases)) {
		t.Fatalf("durable deliveries = %d, want %d committed facts", created, len(cases))
	}
	for range cases {
		delivery, err := engine.Store().Claim(context.Background(), DurableEmailReplyConsumerKey, "test", time.Now(), time.Minute)
		if err != nil || delivery == nil {
			t.Fatalf("claim ineligible delivery: delivery=%v err=%v", delivery, err)
		}
		if err := (&DurableEmailReplyConsumer{replies: replies}).Handle(context.Background(), delivery.Event); err != nil {
			t.Fatalf("handle ineligible delivery: %v", err)
		}
		if err := engine.Store().Complete(context.Background(), *delivery, time.Now()); err != nil {
			t.Fatalf("complete ineligible delivery: %v", err)
		}
	}
	var outboxCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM email_reply_outbox`).Scan(&outboxCount); err != nil {
		t.Fatalf("count suppressed outbox: %v", err)
	}
	if outboxCount != 0 {
		t.Fatalf("ineligible comments created %d reply intents", outboxCount)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if sender.renderCalls != 0 || sender.sendCalls != 0 {
		t.Fatalf("render/send calls = %d/%d, want 0/0", sender.renderCalls, sender.sendCalls)
	}
}

func TestRolledBackCommentCreatesNoDurableEmailReplyDelivery(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()
	env := setupDurableReplyEnv(t, db)
	sender := &durableReplySender{rendered: make(chan error, 1)}
	replies := NewEmailReplyService(db, sender)
	engine := replyEngine(db, 5*time.Millisecond)
	if err := PrepareDurableEmailReplyEngine(context.Background(), engine, replies); err != nil {
		t.Fatalf("prepare reply consumer: %v", err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin comment transaction: %v", err)
	}
	var commentID int64
	if err := tx.QueryRowContext(context.Background(), `
		INSERT INTO comments (item_id, author_id, content, is_private, created_at, updated_at)
		VALUES (?, ?, 'rolled back', FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`, env.itemID, env.actorID).Scan(&commentID); err != nil {
		t.Fatalf("insert rolled-back comment: %v", err)
	}
	authorID := env.actorID
	if _, err := itemevents.NewRecorder(db).CommentCreated(context.Background(), tx, env.workspaceID, itemevents.CommentCreatedV1{
		ItemID: env.itemID, CommentID: commentID, AuthorID: &authorID,
	}, itemevents.User(env.actorID, "test")); err != nil {
		t.Fatalf("record rolled-back comment: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback comment: %v", err)
	}
	created, err := engine.Store().Reconcile(context.Background(), 20)
	if err != nil {
		t.Fatalf("reconcile rolled-back comment: %v", err)
	}
	if created != 0 {
		t.Fatalf("rolled-back comment created %d reply deliveries", created)
	}
}
