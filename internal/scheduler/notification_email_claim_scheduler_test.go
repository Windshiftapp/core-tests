//go:build test

package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

type claimTestSMTPSender struct {
	calls        atomic.Int64
	unconfigured bool
	started      chan struct{}
	release      chan struct{}
	once         sync.Once
	mu           sync.Mutex
	errors       []error
	records      []claimTestSMTPRecord
}

type claimTestSMTPRecord struct {
	email           string
	notificationIDs []int
}

func (s *claimTestSMTPSender) IsSMTPConfigured() bool { return !s.unconfigured }

func (s *claimTestSMTPSender) SendBatchedNotificationsContext(ctx context.Context, email string, _ string, notifications []models.Notification) error {
	call := int(s.calls.Add(1))
	if s.started != nil && call == 1 {
		s.once.Do(func() { close(s.started) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record := claimTestSMTPRecord{email: email, notificationIDs: make([]int, len(notifications))}
	for i, notification := range notifications {
		record.notificationIDs[i] = notification.ID
	}
	s.records = append(s.records, record)
	if len(s.errors) == 0 {
		return nil
	}
	err := s.errors[0]
	s.errors = s.errors[1:]
	return err
}

func newClaimSchedulerFixture(t *testing.T, suffix string) (database.Database, *services.NotificationService, string, int) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	email := "claim-scheduler-" + suffix + "@example.com"
	var userID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES (?, ?, 'Claim', 'Scheduler') RETURNING id
	`, email, "claim-scheduler-"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert recipient: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO notifications (user_id, title, message, type, timestamp, read, authorization_scope)
		VALUES (?, 'Deliver once', 'Claimed body', 'info', CURRENT_TIMESTAMP, false, ?)
	`, userID, models.NotificationScopeSystem); err != nil {
		t.Fatalf("insert notification: %v", err)
	}
	service := services.NewNotificationService(db, nil, nil, services.DefaultNotificationServiceConfig())
	t.Cleanup(func() { _ = service.Close() })
	return db, service, email, userID
}

func TestNotificationSchedulersSendOneExclusiveClaim(t *testing.T) {
	db, service, _, userID := newClaimSchedulerFixture(t, "exclusive")
	sender := &claimTestSMTPSender{started: make(chan struct{}), release: make(chan struct{})}
	first := NewNotificationScheduler(db, sender, time.Minute, service)
	second := NewNotificationScheduler(db, sender, time.Minute, service)
	now := time.Date(2026, 9, 5, 15, 0, 0, 0, time.UTC)
	first.now = func() time.Time { return now }
	second.now = func() time.Time { return now }

	done := make(chan struct{})
	go func() {
		first.processPendingNotifications()
		close(done)
	}()
	<-sender.started
	second.processPendingNotifications()
	if got := sender.calls.Load(); got != 1 {
		t.Fatalf("SMTP sends while first claim is live = %d, want 1", got)
	}
	close(sender.release)
	<-done

	var state string
	var sentAt any
	var attempts int
	if err := db.QueryRow(`SELECT email_delivery_state, sent_at, email_delivery_attempts FROM notifications WHERE user_id = ?`, userID).Scan(&state, &sentAt, &attempts); err != nil {
		t.Fatalf("read delivery state: %v", err)
	}
	if state != "sent" || sentAt == nil || attempts != 1 || sender.calls.Load() != 1 {
		t.Fatalf("delivery = state:%s sent:%v attempts:%d sends:%d", state, sentAt, attempts, sender.calls.Load())
	}
}

func TestNotificationSchedulerRetriesRecordedFailure(t *testing.T) {
	db, service, _, userID := newClaimSchedulerFixture(t, "retry")
	sender := &claimTestSMTPSender{errors: []error{errors.New("smtp unavailable"), nil}}
	scheduler := NewNotificationScheduler(db, sender, time.Minute, service)
	now := time.Date(2026, 9, 5, 16, 0, 0, 0, time.UTC)
	scheduler.now = func() time.Time { return now }

	scheduler.processPendingNotifications()
	var failedState, lastError string
	var sentAt any
	var attempts int
	if err := db.QueryRow(`SELECT email_delivery_state, email_last_error, sent_at, email_delivery_attempts FROM notifications WHERE user_id = ?`, userID).Scan(&failedState, &lastError, &sentAt, &attempts); err != nil {
		t.Fatalf("read failure state: %v", err)
	}
	if failedState != "failed" || lastError != "smtp unavailable" || sentAt != nil || attempts != 1 || sender.calls.Load() != 1 {
		t.Fatalf("first attempt = state:%s error:%q sent:%v attempts:%d sends:%d", failedState, lastError, sentAt, attempts, sender.calls.Load())
	}
	var runSuccess bool
	var runError string
	var processed int
	if err := db.QueryRow(`SELECT success, error_message, items_processed FROM scheduler_runs WHERE scheduler_name = 'notification' ORDER BY id DESC LIMIT 1`).Scan(&runSuccess, &runError, &processed); err != nil {
		t.Fatalf("read failed scheduler run: %v", err)
	}
	if runSuccess || runError != "1 of 1 notification batches failed" || processed != 1 {
		t.Fatalf("failed scheduler run = success:%v error:%q processed:%d", runSuccess, runError, processed)
	}

	scheduler.processPendingNotifications()
	var completedState string
	var completedAt, completedError any
	if err := db.QueryRow(`SELECT email_delivery_state, sent_at, email_last_error, email_delivery_attempts FROM notifications WHERE user_id = ?`, userID).Scan(&completedState, &completedAt, &completedError, &attempts); err != nil {
		t.Fatalf("read completed state: %v", err)
	}
	if completedState != "sent" || completedAt == nil || completedError != nil || attempts != 2 || sender.calls.Load() != 2 {
		t.Fatalf("retry = state:%s sent:%v error:%v attempts:%d sends:%d", completedState, completedAt, completedError, attempts, sender.calls.Load())
	}
	var completedRunError sql.NullString
	if err := db.QueryRow(`SELECT success, error_message, items_processed FROM scheduler_runs WHERE scheduler_name = 'notification' ORDER BY id DESC LIMIT 1`).Scan(&runSuccess, &completedRunError, &processed); err != nil {
		t.Fatalf("read successful scheduler run: %v", err)
	}
	if !runSuccess || completedRunError.Valid || processed != 1 {
		t.Fatalf("successful scheduler run = success:%v error:%v processed:%d", runSuccess, completedRunError, processed)
	}
}

func TestNotificationSchedulerSkipsWhenSMTPUnavailable(t *testing.T) {
	db, service, _, userID := newClaimSchedulerFixture(t, "unconfigured")
	sender := &claimTestSMTPSender{unconfigured: true}
	scheduler := NewNotificationScheduler(db, sender, time.Minute, service)
	scheduler.processPendingNotifications()

	var state string
	var attempts int
	if err := db.QueryRow(`SELECT email_delivery_state, email_delivery_attempts FROM notifications WHERE user_id = ?`, userID).Scan(&state, &attempts); err != nil {
		t.Fatalf("read pending state: %v", err)
	}
	if state != "pending" || attempts != 0 || sender.calls.Load() != 0 {
		t.Fatalf("unconfigured SMTP = state:%s attempts:%d sends:%d", state, attempts, sender.calls.Load())
	}
}

func TestNotificationSchedulerCapsBatchesAndKeepsRecipientsIndependent(t *testing.T) {
	db, service, firstEmail, firstUserID := newClaimSchedulerFixture(t, "caps-first")
	for i := 0; i < 54; i++ {
		if _, err := db.Exec(`
			INSERT INTO notifications (user_id, title, message, type, timestamp, read, authorization_scope)
			VALUES (?, ?, 'body', 'info', CURRENT_TIMESTAMP, false, ?)
		`, firstUserID, "extra", models.NotificationScopeSystem); err != nil {
			t.Fatalf("insert capped notification: %v", err)
		}
	}
	secondEmail := "claim-scheduler-caps-second@example.com"
	var secondUserID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES (?, 'claim-scheduler-caps-second', 'Second', 'Recipient') RETURNING id
	`, secondEmail).Scan(&secondUserID); err != nil {
		t.Fatalf("insert second recipient: %v", err)
	}
	var secondNotificationID int
	if err := db.QueryRow(`
		INSERT INTO notifications (user_id, title, message, type, timestamp, read, authorization_scope)
		VALUES (?, 'independent', 'body', 'info', CURRENT_TIMESTAMP, false, ?) RETURNING id
	`, secondUserID, models.NotificationScopeSystem).Scan(&secondNotificationID); err != nil {
		t.Fatalf("insert second notification: %v", err)
	}

	sender := &claimTestSMTPSender{}
	scheduler := NewNotificationScheduler(db, sender, time.Minute, service)
	scheduler.processPendingNotifications()
	sender.mu.Lock()
	records := append([]claimTestSMTPRecord(nil), sender.records...)
	sender.mu.Unlock()
	if len(records) != 2 {
		t.Fatalf("SMTP records = %+v, want two recipients", records)
	}
	byEmail := make(map[string][]int, len(records))
	for _, record := range records {
		byEmail[record.email] = record.notificationIDs
	}
	if len(byEmail[firstEmail]) != maxBatchSize {
		t.Fatalf("first recipient batch size = %d, want %d", len(byEmail[firstEmail]), maxBatchSize)
	}
	if got := byEmail[secondEmail]; len(got) != 1 || got[0] != secondNotificationID {
		t.Fatalf("second recipient IDs = %v, want [%d]", got, secondNotificationID)
	}
	var firstPending, secondPending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND email_delivery_state = 'pending'`, firstUserID).Scan(&firstPending); err != nil {
		t.Fatalf("count capped remainder: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND email_delivery_state = 'pending'`, secondUserID).Scan(&secondPending); err != nil {
		t.Fatalf("count second remainder: %v", err)
	}
	if firstPending != 5 || secondPending != 0 {
		t.Fatalf("pending remainder = first:%d second:%d, want 5/0", firstPending, secondPending)
	}
}

func TestNotificationSchedulerExcludesUnauthorizedContent(t *testing.T) {
	db, service, email, userID := newClaimSchedulerFixture(t, "authorization")
	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Denied notification', 'DEN-NOTIFY') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert denied workspace: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO notifications (user_id, title, message, type, timestamp, read, authorization_scope, workspace_id)
		VALUES (?, 'restricted', 'secret', 'info', CURRENT_TIMESTAMP, false, ?, ?)
	`, userID, models.NotificationScopeWorkspace, workspaceID); err != nil {
		t.Fatalf("insert restricted notification: %v", err)
	}
	sender := &claimTestSMTPSender{}
	scheduler := NewNotificationScheduler(db, sender, time.Minute, service)
	scheduler.processPendingNotifications()
	sender.mu.Lock()
	records := append([]claimTestSMTPRecord(nil), sender.records...)
	sender.mu.Unlock()
	if len(records) != 1 || records[0].email != email || len(records[0].notificationIDs) != 1 {
		t.Fatalf("authorized SMTP records = %+v, want only system notification", records)
	}
	var restrictedState string
	if err := db.QueryRow(`SELECT email_delivery_state FROM notifications WHERE title = 'restricted'`).Scan(&restrictedState); err != nil {
		t.Fatalf("read restricted state: %v", err)
	}
	// The restricted row is terminally suppressed so it stops being rescanned
	// and cannot starve other recipients (WI-1768).
	if restrictedState != "suppressed" {
		t.Fatalf("restricted notification state = %q, want suppressed", restrictedState)
	}
}

// insertSystemNotificationRecipient creates an active user with one pending
// system-scope notification, enough for the digest scheduler to target them.
func insertSystemNotificationRecipient(t *testing.T, db database.Database, email string) {
	t.Helper()
	var userID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES (?, ?, 'Slot', 'Recipient') RETURNING id
	`, email, email).Scan(&userID); err != nil {
		t.Fatalf("insert recipient %s: %v", email, err)
	}
	if _, err := db.Exec(`
		INSERT INTO notifications (user_id, title, message, type, timestamp, read, authorization_scope)
		VALUES (?, 'notice', 'body', 'info', CURRENT_TIMESTAMP, false, ?)
	`, userID, models.NotificationScopeSystem); err != nil {
		t.Fatalf("insert notification for %s: %v", email, err)
	}
}

// WI-1768: users in the SMTP failure cooldown are held out of the recipient
// scan, so a full cap of cooling-down recipients cannot block everyone else.
func TestNotificationSchedulerCooldownDoesNotConsumeRecipientSlots(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	// More than the per-tick recipient cap, so the old claim-and-release path
	// would spend the whole budget on cooling-down users.
	const cooldownRecipients = 250
	cooldownEmails := make([]string, 0, cooldownRecipients)
	for i := 0; i < cooldownRecipients; i++ {
		email := fmt.Sprintf("cooldown-slot-%03d@example.com", i)
		cooldownEmails = append(cooldownEmails, email)
		insertSystemNotificationRecipient(t, db, email)
	}
	normalEmail := "cooldown-slot-normal@example.com"
	insertSystemNotificationRecipient(t, db, normalEmail)

	service := services.NewNotificationService(db, nil, nil, services.DefaultNotificationServiceConfig())
	t.Cleanup(func() { _ = service.Close() })
	sender := &claimTestSMTPSender{}
	scheduler := NewNotificationScheduler(db, sender, time.Minute, service)
	for _, email := range cooldownEmails {
		scheduler.skipUntil[email] = time.Now().Add(time.Hour)
	}
	scheduler.processPendingNotifications()

	sender.mu.Lock()
	records := append([]claimTestSMTPRecord(nil), sender.records...)
	sender.mu.Unlock()
	if len(records) != 1 || records[0].email != normalEmail {
		t.Fatalf("SMTP records = %+v, want only %s", records, normalEmail)
	}

	var pending, claimed, attempted int
	if err := db.QueryRow(`
		SELECT
			SUM(CASE WHEN n.email_delivery_state = 'pending' THEN 1 ELSE 0 END),
			SUM(CASE WHEN n.email_delivery_state = 'claimed' THEN 1 ELSE 0 END),
			SUM(CASE WHEN n.email_delivery_attempts > 0 THEN 1 ELSE 0 END)
		FROM notifications n JOIN users u ON u.id = n.user_id
		WHERE u.email LIKE 'cooldown-slot-%@example.com' AND u.email != ?
	`, normalEmail).Scan(&pending, &claimed, &attempted); err != nil {
		t.Fatalf("read cooldown recipient states: %v", err)
	}
	if pending != cooldownRecipients || claimed != 0 || attempted != 0 {
		t.Fatalf("cooldown recipients = pending:%d claimed:%d attempted:%d, want %d/0/0",
			pending, claimed, attempted, cooldownRecipients)
	}
}

func TestNotificationSchedulerCooldownExpiresWithoutSleeping(t *testing.T) {
	db, service, email, userID := newClaimSchedulerFixture(t, "cooldown")
	sender := &claimTestSMTPSender{}
	scheduler := NewNotificationScheduler(db, sender, time.Minute, service)
	scheduler.failCounts[email] = maxConsecutiveFailures
	scheduler.skipUntil[email] = time.Now().Add(time.Minute)
	scheduler.processPendingNotifications()
	if sender.calls.Load() != 0 {
		t.Fatalf("SMTP sends during cooldown = %d, want 0", sender.calls.Load())
	}
	var state string
	if err := db.QueryRow(`SELECT email_delivery_state FROM notifications WHERE user_id = ?`, userID).Scan(&state); err != nil {
		t.Fatalf("read cooldown state: %v", err)
	}
	if state != "pending" {
		t.Fatalf("cooldown state = %q, want pending", state)
	}
	scheduler.skipUntil[email] = time.Now().Add(-time.Second)
	scheduler.processPendingNotifications()
	if sender.calls.Load() != 1 {
		t.Fatalf("SMTP sends after cooldown expiry = %d, want 1", sender.calls.Load())
	}
}

func TestNotificationSchedulerStopCancelsAndWaitsForActiveDelivery(t *testing.T) {
	db, service, _, userID := newClaimSchedulerFixture(t, "shutdown")
	sender := &claimTestSMTPSender{started: make(chan struct{}), release: make(chan struct{})}
	scheduler := NewNotificationScheduler(db, sender, time.Hour, service)
	now := time.Date(2026, 9, 5, 17, 0, 0, 0, time.UTC)
	scheduler.now = func() time.Time { return now }
	scheduler.Start()

	firstRunDone := make(chan struct{})
	scheduler.runNow <- firstRunDone
	<-sender.started
	stopDone := make(chan struct{})
	go func() {
		scheduler.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel and wait for active SMTP delivery")
	}
	<-firstRunDone

	var state string
	var sentAt, claimOwner, claimToken, claimExpiry any
	var attempts int
	if err := db.QueryRow(`
		SELECT email_delivery_state, sent_at, email_claim_owner, email_claim_token,
		       email_claim_expires_at, email_delivery_attempts
		FROM notifications WHERE user_id = ?
	`, userID).Scan(&state, &sentAt, &claimOwner, &claimToken, &claimExpiry, &attempts); err != nil {
		t.Fatalf("read cancelled claim: %v", err)
	}
	if state != "claimed" || sentAt != nil || claimOwner == nil || claimToken == nil || claimExpiry == nil || attempts != 1 || sender.calls.Load() != 1 {
		t.Fatalf("cancelled delivery = state:%s sent:%v owner:%v token:%v expiry:%v attempts:%d sends:%d", state, sentAt, claimOwner, claimToken, claimExpiry, attempts, sender.calls.Load())
	}

	// A restart cannot overlap the still-live claim.
	scheduler.Start()
	secondRunDone := make(chan struct{})
	scheduler.runNow <- secondRunDone
	<-secondRunDone
	if sender.calls.Load() != 1 {
		t.Fatalf("SMTP sends before abandoned claim expiry = %d, want 1", sender.calls.Load())
	}
	if _, err := db.Exec(`UPDATE notifications SET email_claim_expires_at = ? WHERE user_id = ?`, now.Add(-time.Second), userID); err != nil {
		t.Fatalf("expire abandoned claim: %v", err)
	}
	thirdRunDone := make(chan struct{})
	scheduler.runNow <- thirdRunDone
	<-thirdRunDone
	if sender.calls.Load() != 2 {
		t.Fatalf("SMTP sends after abandoned claim recovery = %d, want 2", sender.calls.Load())
	}
	scheduler.Stop()
	if err := db.QueryRow(`SELECT email_delivery_state, email_delivery_attempts FROM notifications WHERE user_id = ?`, userID).Scan(&state, &attempts); err != nil {
		t.Fatalf("read recovered delivery: %v", err)
	}
	if state != "sent" || attempts != 2 {
		t.Fatalf("recovered delivery = state:%s attempts:%d, want sent/2", state, attempts)
	}
}

// processPendingNotifications finds unread notifications and sends them in batches
//
//nolint:unused // focused overlay tests invoke a deterministic tick directly
func (ns *NotificationScheduler) processPendingNotifications() {
	ns.processPendingNotificationsContext(context.Background())
}

func TestNotificationSchedulerTickBudgetBoundsSlowSMTP(t *testing.T) {
	db, service, _, userID := newClaimSchedulerFixture(t, "tick-budget")
	sender := &claimTestSMTPSender{started: make(chan struct{}), release: make(chan struct{})}
	scheduler := NewNotificationScheduler(db, sender, time.Minute, service)
	scheduler.SetTickBudget(50 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		scheduler.processPendingNotifications()
		close(done)
	}()
	<-sender.started

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		close(sender.release)
		t.Fatal("digest tick outlived its budget with a stalled SMTP send")
	}
	close(sender.release)

	var state string
	if err := db.QueryRow(`SELECT email_delivery_state FROM notifications WHERE user_id = ?`, userID).Scan(&state); err != nil {
		t.Fatalf("read delivery state: %v", err)
	}
	if state != "claimed" {
		t.Fatalf("state after budget expiry = %q, want claimed until the lease recovers", state)
	}
}

func TestNotificationSchedulerClaimsOneBatchAtATime(t *testing.T) {
	db, service, _, firstUserID := newClaimSchedulerFixture(t, "one-at-a-time")
	var secondUserID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('claim-scheduler-one-at-a-time-second@example.com', 'claim-second', 'Second', 'Recipient')
		RETURNING id
	`).Scan(&secondUserID); err != nil {
		t.Fatalf("insert second recipient: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO notifications (user_id, title, message, type, timestamp, read, authorization_scope)
		VALUES (?, 'Second', 'Second body', 'info', CURRENT_TIMESTAMP, false, ?)
	`, secondUserID, models.NotificationScopeSystem); err != nil {
		t.Fatalf("insert second notification: %v", err)
	}

	sender := &claimTestSMTPSender{started: make(chan struct{}), release: make(chan struct{})}
	scheduler := NewNotificationScheduler(db, sender, time.Minute, service)
	done := make(chan struct{})
	go func() {
		scheduler.processPendingNotifications()
		close(done)
	}()
	<-sender.started

	var claimed, pending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id IN (?, ?) AND email_delivery_state = 'claimed'`, firstUserID, secondUserID).Scan(&claimed); err != nil {
		t.Fatalf("count claimed: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id IN (?, ?) AND email_delivery_state = 'pending'`, firstUserID, secondUserID).Scan(&pending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if claimed != 1 || pending != 1 {
		t.Fatalf("while one send is in flight = claimed:%d pending:%d, want 1/1", claimed, pending)
	}

	close(sender.release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not finish after releasing the first send")
	}
	if got := sender.calls.Load(); got != 2 {
		t.Fatalf("SMTP sends = %d, want one per recipient", got)
	}
}
