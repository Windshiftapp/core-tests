//go:build test

package services

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

// notificationScanCounter records whether notification email queries are
// SQL-bounded: the per-recipient backlog reads must carry a LIMIT and the
// recipient scan must be capped per tick.
type notificationScanCounter struct {
	database.Database
	mu                 sync.Mutex
	perUserQueries     int
	perUserUnbounded   int
	recipientQueries   int
	recipientUnbounded int
}

func (db *notificationScanCounter) Query(query string, args ...any) (*sql.Rows, error) {
	db.classify(query)
	return db.Database.Query(query, args...)
}

func (db *notificationScanCounter) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db.classify(query)
	return db.Database.QueryContext(ctx, query, args...)
}

func (db *notificationScanCounter) classify(query string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if strings.Contains(query, "FROM notifications n") && strings.Contains(query, "n.user_id = ?") {
		db.perUserQueries++
		if !strings.Contains(query, "LIMIT") {
			db.perUserUnbounded++
		}
	}
	if strings.Contains(query, "JOIN users u") && strings.Contains(query, "GROUP BY u.id") {
		db.recipientQueries++
		if !strings.Contains(query, "LIMIT") {
			db.recipientUnbounded++
		}
	}
}

// A large per-user backlog must be read through a SQL LIMIT (paged in Go for
// authorization filtering), not streamed in full on every scheduler tick.
func TestUnreadEmailBatchesBoundsPerUserBacklogReads(t *testing.T) {
	env := newPermTestEnv(t)
	userID := env.insertUser("backlog-recipient@example.com")
	workspaceID := env.insertWorkspace("NOTIFY-BOUNDED")
	roles := repository.NewWorkspaceRoleRepository(env.db)
	if err := roles.AssignToUser(userID, workspaceID, env.roleID("Viewer"), userID); err != nil {
		t.Fatalf("assign workspace role: %v", err)
	}

	const backlogSize = 300
	titleFor := func(i int) string {
		if i%10 == 0 {
			return fmt.Sprintf("system-%03d", i)
		}
		return fmt.Sprintf("backlog-%03d", i)
	}
	for i := 1; i <= backlogSize; i++ {
		if i%10 == 0 {
			if _, err := env.db.Exec(`
				INSERT INTO notifications (user_id, title, message, type, timestamp, read, authorization_scope)
				VALUES (?, ?, ?, 'info', CURRENT_TIMESTAMP, false, ?)
			`, userID, titleFor(i), "body", models.NotificationScopeSystem); err != nil {
				t.Fatalf("seed system notification %d: %v", i, err)
			}
			continue
		}
		if _, err := env.db.Exec(`
			INSERT INTO notifications (user_id, title, message, type, timestamp, read, authorization_scope, workspace_id)
			VALUES (?, ?, ?, 'info', CURRENT_TIMESTAMP, false, ?, ?)
		`, userID, titleFor(i), "body", models.NotificationScopeWorkspace, workspaceID); err != nil {
			t.Fatalf("seed workspace notification %d: %v", i, err)
		}
	}

	scans := &notificationScanCounter{Database: env.db}
	service := NewNotificationService(scans, nil, env.service, DefaultNotificationServiceConfig())
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("close notification service: %v", err)
		}
	})

	const maxBatchSize = 50
	batches, err := service.UnreadEmailBatches(maxBatchSize)
	if err != nil {
		t.Fatalf("email batches: %v", err)
	}
	batch := batches["backlog-recipient@example.com"]
	if batch == nil {
		t.Fatal("no email batch for the backlogged recipient")
	}
	if len(batch.Notifications) != maxBatchSize {
		t.Fatalf("batch size = %d, want the %d cap", len(batch.Notifications), maxBatchSize)
	}
	// Ordering contract: oldest first, so the first page is the oldest backlog
	// in insertion order.
	for i, notification := range batch.Notifications {
		want := titleFor(i + 1)
		if notification.Title != want {
			t.Fatalf("batch position %d = %q, want %q", i, notification.Title, want)
		}
	}

	scans.mu.Lock()
	defer scans.mu.Unlock()
	if scans.perUserQueries == 0 || scans.recipientQueries == 0 {
		t.Fatalf("expected both recipient and per-user queries, saw %d and %d", scans.recipientQueries, scans.perUserQueries)
	}
	if scans.perUserUnbounded > 0 {
		t.Fatalf("%d of %d per-user backlog queries had no SQL LIMIT; the full backlog is read per recipient", scans.perUserUnbounded, scans.perUserQueries)
	}
	if scans.recipientUnbounded > 0 {
		t.Fatalf("%d of %d recipient scans had no LIMIT", scans.recipientUnbounded, scans.recipientQueries)
	}
}

// The recipient scan is capped per tick, and recipients beyond the cap are
// served on the following tick instead of being starved.
func TestUnreadEmailBatchesCapsRecipientsPerTick(t *testing.T) {
	env := newPermTestEnv(t)
	totalRecipients := maxNotificationEmailRecipients + 5
	userIDs := make([]int, 0, totalRecipients)
	for i := 0; i < totalRecipients; i++ {
		userIDs = append(userIDs, env.insertUser(fmt.Sprintf("capped-recipient-%03d@example.com", i)))
		if _, err := env.db.Exec(`
			INSERT INTO notifications (user_id, title, message, type, timestamp, read, authorization_scope)
			VALUES (?, ?, ?, 'info', CURRENT_TIMESTAMP, false, ?)
		`, userIDs[i], "notice", "body", models.NotificationScopeSystem); err != nil {
			t.Fatalf("seed notification for user %d: %v", userIDs[i], err)
		}
	}

	service := NewNotificationService(env.db, nil, env.service, DefaultNotificationServiceConfig())
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("close notification service: %v", err)
		}
	})

	first, err := service.UnreadEmailBatches(100)
	if err != nil {
		t.Fatalf("first email batch run: %v", err)
	}
	if len(first) != maxNotificationEmailRecipients {
		t.Fatalf("first run returned %d recipient batches, want the %d cap", len(first), maxNotificationEmailRecipients)
	}

	// Drain the served batches, then the remaining recipients must surface.
	for _, batch := range first {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch.NotificationIDs)), ",")
		args := make([]any, 0, len(batch.NotificationIDs))
		for _, id := range batch.NotificationIDs {
			args = append(args, id)
		}
		if _, err := env.db.Exec(`UPDATE notifications SET sent_at = CURRENT_TIMESTAMP WHERE id IN (`+placeholders+`)`, args...); err != nil {
			t.Fatalf("mark served notifications sent: %v", err)
		}
	}
	second, err := service.UnreadEmailBatches(100)
	if err != nil {
		t.Fatalf("second email batch run: %v", err)
	}
	if len(second) != totalRecipients-maxNotificationEmailRecipients {
		t.Fatalf("second run returned %d recipient batches, want the %d leftovers", len(second), totalRecipients-maxNotificationEmailRecipients)
	}
}
