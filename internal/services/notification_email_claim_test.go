//go:build test

package services

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

func newNotificationClaimTestService(t *testing.T, suffix string) (database.Database, *NotificationService, int, string) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	email := "notification-claim-" + suffix + "@example.com"
	var userID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES (?, ?, 'Claim', 'Recipient') RETURNING id
	`, email, "notification-claim-"+suffix).Scan(&userID); err != nil {
		t.Fatalf("insert claim recipient: %v", err)
	}
	service := NewNotificationService(db, nil, nil, DefaultNotificationServiceConfig())
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("close notification service: %v", err)
		}
	})
	return db, service, userID, email
}

func insertClaimNotification(t *testing.T, db database.Database, userID int, title string) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`
		INSERT INTO notifications (user_id, title, message, type, timestamp, read, authorization_scope)
		VALUES (?, ?, ?, 'info', CURRENT_TIMESTAMP, false, ?) RETURNING id
	`, userID, title, title+" body", models.NotificationScopeSystem).Scan(&id); err != nil {
		t.Fatalf("insert notification: %v", err)
	}
	return id
}

func TestNotificationEmailClaimRecoversExpiryAndFencesStaleOwner(t *testing.T) {
	db, service, userID, email := newNotificationClaimTestService(t, "recovery")
	insertClaimNotification(t, db, userID, "recover me")
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	first, err := service.ClaimUnreadEmailBatches("worker-a", now, time.Minute, 50)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	claimA := first[email]
	if claimA == nil || len(claimA.NotificationIDs) != 1 {
		t.Fatalf("first claim = %+v, want one notification", claimA)
	}
	beforeExpiry, err := service.ClaimUnreadEmailBatches("worker-b", now.Add(59*time.Second), time.Minute, 50)
	if err != nil {
		t.Fatalf("claim before expiry: %v", err)
	}
	if len(beforeExpiry) != 0 {
		t.Fatalf("claims before expiry = %+v, want none", beforeExpiry)
	}

	recovered, err := service.ClaimUnreadEmailBatches("worker-b", now.Add(time.Minute), time.Minute, 50)
	if err != nil {
		t.Fatalf("recover expired claim: %v", err)
	}
	claimB := recovered[email]
	if claimB == nil || claimB.Token == claimA.Token {
		t.Fatalf("recovered claim = %+v, want new token", claimB)
	}
	if err := service.CompleteNotificationEmailClaim(claimA, now.Add(time.Minute)); !errors.Is(err, ErrNotificationEmailClaimLost) {
		t.Fatalf("stale completion error = %v, want claim lost", err)
	}
	if err := service.FailNotificationEmailClaim(claimA, errors.New("late failure"), now.Add(time.Minute)); !errors.Is(err, ErrNotificationEmailClaimLost) {
		t.Fatalf("stale failure error = %v, want claim lost", err)
	}
	if err := service.CompleteNotificationEmailClaim(claimB, now.Add(time.Minute)); err != nil {
		t.Fatalf("complete recovered claim: %v", err)
	}

	var state string
	var sentAt, owner, token, expires, lastError any
	var attempts int
	var failed bool
	if err := db.QueryRow(`
		SELECT email_delivery_state, sent_at, email_claim_owner, email_claim_token,
		       email_claim_expires_at, email_delivery_attempts, email_last_error, last_send_failed
		FROM notifications WHERE user_id = ?
	`, userID).Scan(&state, &sentAt, &owner, &token, &expires, &attempts, &lastError, &failed); err != nil {
		t.Fatalf("read completed delivery state: %v", err)
	}
	if state != notificationEmailSent || sentAt == nil || owner != nil || token != nil || expires != nil || attempts != 2 || lastError != nil || failed {
		t.Fatalf("completed delivery = state:%s sent:%v owner:%v token:%v expires:%v attempts:%d error:%v failed:%v", state, sentAt, owner, token, expires, attempts, lastError, failed)
	}
}

func TestNotificationEmailClaimIsExclusiveAcrossServices(t *testing.T) {
	db, firstService, userID, email := newNotificationClaimTestService(t, "exclusive")
	insertClaimNotification(t, db, userID, "one send")
	secondService := NewNotificationService(db, nil, nil, DefaultNotificationServiceConfig())
	t.Cleanup(func() { _ = secondService.Close() })
	now := time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC)

	start := make(chan struct{})
	type result struct {
		claims map[string]*NotificationEmailClaim
		err    error
	}
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	claim := func(service *NotificationService, owner string) {
		ready.Done()
		<-start
		claims, err := service.ClaimUnreadEmailBatches(owner, now, time.Minute, 50)
		results <- result{claims: claims, err: err}
	}
	go claim(firstService, "worker-a")
	go claim(secondService, "worker-b")
	ready.Wait()
	close(start)

	winners := 0
	var winningClaim *NotificationEmailClaim
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("simultaneous claim: %v", result.err)
		}
		if candidate := result.claims[email]; candidate != nil {
			winners++
			winningClaim = candidate
		}
	}
	if winners != 1 {
		t.Fatalf("simultaneous claim winners = %d, want 1", winners)
	}
	if err := firstService.CompleteNotificationEmailClaim(winningClaim, now); err != nil {
		t.Fatalf("complete exclusive claim: %v", err)
	}
}

func TestNotificationEmailClaimFailureIsRecoverable(t *testing.T) {
	db, service, userID, email := newNotificationClaimTestService(t, "failure")
	insertClaimNotification(t, db, userID, "retry me")
	now := time.Date(2026, 9, 5, 14, 0, 0, 0, time.UTC)
	claims, err := service.ClaimUnreadEmailBatches("worker-a", now, time.Minute, 50)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := service.FailNotificationEmailClaim(claims[email], errors.New("smtp unavailable"), now); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	var state, lastError string
	var sentAt, owner, token, expires any
	var attempts int
	var failed bool
	if err := db.QueryRow(`
		SELECT email_delivery_state, email_last_error, sent_at, email_claim_owner,
		       email_claim_token, email_claim_expires_at, email_delivery_attempts, last_send_failed
		FROM notifications WHERE user_id = ?
	`, userID).Scan(&state, &lastError, &sentAt, &owner, &token, &expires, &attempts, &failed); err != nil {
		t.Fatalf("read failed delivery state: %v", err)
	}
	if state != notificationEmailFailed || lastError != "smtp unavailable" || sentAt != nil || owner != nil || token != nil || expires != nil || attempts != 1 || !failed {
		t.Fatalf("failed delivery = state:%s error:%q sent:%v owner:%v token:%v expires:%v attempts:%d failed:%v", state, lastError, sentAt, owner, token, expires, attempts, failed)
	}
	retry, err := service.ClaimUnreadEmailBatches("worker-b", now, time.Minute, 50)
	if err != nil {
		t.Fatalf("retry failed claim: %v", err)
	}
	if retry[email] == nil {
		t.Fatal("failed notification was not reclaimable")
	}
}

func TestNotificationEmailClaimsMakeOldestFirstProgress(t *testing.T) {
	db, service, userID, email := newNotificationClaimTestService(t, "fairness")
	base := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	insertAt := func(title string, timestamp time.Time, read bool, sent bool) {
		t.Helper()
		var sentAt any
		state := notificationEmailPending
		if sent {
			sentAt = timestamp
			state = notificationEmailSent
		}
		if _, err := db.Exec(`
			INSERT INTO notifications (
				user_id, title, message, type, timestamp, read, sent_at,
				email_delivery_state, authorization_scope
			) VALUES (?, ?, ?, 'info', ?, ?, ?, ?, ?)
		`, userID, title, title+" body", timestamp, read, sentAt, state, models.NotificationScopeSystem); err != nil {
			t.Fatalf("insert %s: %v", title, err)
		}
	}
	insertAt("excluded sent", base.Add(-2*time.Minute), false, true)
	insertAt("excluded read", base.Add(-time.Minute), true, false)
	for i, title := range []string{"oldest one", "oldest two", "middle three", "middle four"} {
		insertAt(title, base.Add(time.Duration(i)*time.Minute), false, false)
	}
	claimTitles := func(owner string) (*NotificationEmailClaim, []string) {
		t.Helper()
		claims, err := service.ClaimUnreadEmailBatches(owner, base.Add(time.Hour), time.Minute, 2)
		if err != nil {
			t.Fatalf("claim %s: %v", owner, err)
		}
		claim := claims[email]
		if claim == nil {
			t.Fatalf("claim %s missing", owner)
		}
		titles := make([]string, len(claim.Notifications))
		for i, notification := range claim.Notifications {
			titles[i] = notification.Title
		}
		return claim, titles
	}
	claim, titles := claimTitles("worker-a")
	if strings.Join(titles, "|") != "oldest one|oldest two" {
		t.Fatalf("first claim titles = %v, want oldest rows", titles)
	}
	if err := service.FailNotificationEmailClaim(claim, errors.New("retry"), base.Add(time.Hour)); err != nil {
		t.Fatalf("fail first claim: %v", err)
	}
	insertAt("new arrival five", base.Add(5*time.Minute), false, false)
	insertAt("new arrival six", base.Add(6*time.Minute), false, false)
	retry, retryTitles := claimTitles("worker-b")
	if strings.Join(retryTitles, "|") != "oldest one|oldest two" {
		t.Fatalf("retry titles = %v, want failed oldest rows before arrivals", retryTitles)
	}
	if err := service.CompleteNotificationEmailClaim(retry, base.Add(time.Hour)); err != nil {
		t.Fatalf("complete retry: %v", err)
	}
	next, nextTitles := claimTitles("worker-c")
	if strings.Join(nextTitles, "|") != "middle three|middle four" {
		t.Fatalf("next titles = %v, want pre-existing middle rows", nextTitles)
	}
	if err := service.CompleteNotificationEmailClaim(next, base.Add(time.Hour)); err != nil {
		t.Fatalf("complete middle claim: %v", err)
	}
	_, lastTitles := claimTitles("worker-d")
	if strings.Join(lastTitles, "|") != "new arrival five|new arrival six" {
		t.Fatalf("last titles = %v, want new arrivals", lastTitles)
	}
}

func TestNotificationBatchIsClaimedOnlyWhenSent(t *testing.T) {
	db, service, userID, email := newNotificationClaimTestService(t, "deferred")
	insertClaimNotification(t, db, userID, "send me")
	now := time.Date(2026, 9, 5, 14, 0, 0, 0, time.UTC)

	batches, err := service.UnreadEmailBatches(50)
	if err != nil {
		t.Fatalf("UnreadEmailBatches: %v", err)
	}
	batch := batches[email]
	if batch == nil {
		t.Fatalf("UnreadEmailBatches = %+v, want recipient %s", batches, email)
	}
	var state string
	if err := db.QueryRow(`SELECT email_delivery_state FROM notifications WHERE user_id = ?`, userID).Scan(&state); err != nil {
		t.Fatalf("read selected state: %v", err)
	}
	if state != notificationEmailPending {
		t.Fatalf("state after selection = %q, want pending (selection must not claim)", state)
	}

	claim, err := service.ClaimNotificationEmailBatch(batch, "worker-a", now, time.Minute)
	if err != nil {
		t.Fatalf("ClaimNotificationEmailBatch: %v", err)
	}
	if claim == nil || claim.Token == "" {
		t.Fatalf("claim = %+v, want a live claim", claim)
	}
	if err := db.QueryRow(`SELECT email_delivery_state FROM notifications WHERE user_id = ?`, userID).Scan(&state); err != nil {
		t.Fatalf("read claimed state: %v", err)
	}
	if state != notificationEmailClaimed {
		t.Fatalf("state after claim = %q, want claimed", state)
	}

	second, err := service.ClaimNotificationEmailBatch(batch, "worker-b", now, time.Minute)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if second != nil {
		t.Fatalf("second claim = %+v, want nil while the first is live", second)
	}
	if err := service.CompleteNotificationEmailClaim(claim, now); err != nil {
		t.Fatalf("complete claim: %v", err)
	}
}
