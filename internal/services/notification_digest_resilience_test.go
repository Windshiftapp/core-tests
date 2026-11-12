//go:build test

package services

import (
	"errors"
	"testing"

	"windshift/internal/models"
)

// flakyAuthorizer wraps the real authorizer and injects a snapshot failure for
// one user so the digest's per-recipient resilience can be exercised.
type flakyAuthorizer struct {
	real   *NotificationAuthorizer
	failID int
}

func (a flakyAuthorizer) Snapshot(userID int) (*NotificationAuthorizationSnapshot, error) {
	if userID == a.failID {
		return nil, errors.New("injected scope resolution failure")
	}
	return a.real.Snapshot(userID)
}

func (a flakyAuthorizer) snapshotForRecipient(userID int, active bool, permissions *models.UserPermissionCache) *NotificationAuthorizationSnapshot {
	return a.real.snapshotForRecipient(userID, active, permissions)
}

// TestUnreadEmailBatchesSkipsFailingRecipientWithoutAborting is the regression
// test for one recipient's authorization failure wedging the whole digest tick:
// the healthy recipient must still be batched, and the failing recipient's rows
// must stay pending for a later retry.
func TestUnreadEmailBatchesSkipsFailingRecipientWithoutAborting(t *testing.T) {
	env := newPermTestEnv(t)
	failingID := env.insertUser("digest-failing@example.com")
	healthyID := env.insertUser("digest-healthy@example.com")

	// The failing recipient's row sorts first so it is scanned before the
	// healthy one.
	if _, err := env.db.Exec(`
		INSERT INTO notifications (user_id, title, message, type, timestamp, read, authorization_scope)
		VALUES (?, 'failing notice', 'body', 'info', datetime('now', '-1 minute'), false, ?)
	`, failingID, models.NotificationScopeSystem); err != nil {
		t.Fatalf("insert failing notification: %v", err)
	}
	if _, err := env.db.Exec(`
		INSERT INTO notifications (user_id, title, message, type, timestamp, read, authorization_scope)
		VALUES (?, 'healthy notice', 'body', 'info', CURRENT_TIMESTAMP, false, ?)
	`, healthyID, models.NotificationScopeSystem); err != nil {
		t.Fatalf("insert healthy notification: %v", err)
	}

	service := NewNotificationService(env.db, nil, env.service, DefaultNotificationServiceConfig())
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("close notification service: %v", err)
		}
	})
	service.notificationAuth = flakyAuthorizer{
		real:   NewNotificationAuthorizer(env.db, env.service, nil),
		failID: failingID,
	}

	batches, err := service.UnreadEmailBatches(100)
	if err != nil {
		t.Fatalf("digest aborted on one recipient's failure: %v", err)
	}
	assertNotificationTitles(t, batches["digest-healthy@example.com"], "healthy notice")
	if _, ok := batches["digest-failing@example.com"]; ok {
		t.Fatalf("failing recipient was batched despite the injected failure")
	}

	// The skipped recipient's row must remain pending, not suppressed, so a
	// later tick can retry it.
	var state string
	if err := env.db.QueryRow(`SELECT email_delivery_state FROM notifications WHERE user_id = ?`, failingID).Scan(&state); err != nil {
		t.Fatalf("read failing notification state: %v", err)
	}
	if state != "pending" {
		t.Fatalf("failing recipient state = %q, want pending", state)
	}
}

// TestUnreadEmailBatchesSurfacesSystemicRecipientFailure proves that when every
// candidate recipient fails, the tick reports an error instead of silently
// returning "nothing to send".
func TestUnreadEmailBatchesSurfacesSystemicRecipientFailure(t *testing.T) {
	env := newPermTestEnv(t)
	userID := env.insertUser("digest-only-failing@example.com")
	if _, err := env.db.Exec(`
		INSERT INTO notifications (user_id, title, message, type, timestamp, read, authorization_scope)
		VALUES (?, 'notice', 'body', 'info', CURRENT_TIMESTAMP, false, ?)
	`, userID, models.NotificationScopeSystem); err != nil {
		t.Fatalf("insert notification: %v", err)
	}

	service := NewNotificationService(env.db, nil, env.service, DefaultNotificationServiceConfig())
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("close notification service: %v", err)
		}
	})
	service.notificationAuth = flakyAuthorizer{
		real:   NewNotificationAuthorizer(env.db, env.service, nil),
		failID: userID,
	}

	if _, err := service.UnreadEmailBatches(100); err == nil {
		t.Fatalf("expected an error when every candidate recipient fails")
	}
}
