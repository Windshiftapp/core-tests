//go:build test

package services

import (
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
)

// WI-1768 follow-up: suppressing a recipient's invisible backlog must stay
// bounded. A single recipient can accumulate more invisible notifications than
// SQLite/PostgreSQL accept bind variables, and the suppression UPDATE builds
// one placeholder per row. The drain must not fail the whole digest tick.
func TestUnreadEmailBatchesBoundsSuppressionForOneRecipient(t *testing.T) {
	env := newPermTestEnv(t)
	deniedWorkspaceID := env.insertWorkspace("NOTIFY-SUPPRESS-BOUND-DENIED")
	keeperID := env.insertUser("suppress-bound-keeper@example.com")
	if err := repository.NewWorkspaceRoleRepository(env.db).AssignToUser(keeperID, deniedWorkspaceID, env.roleID("Viewer"), keeperID); err != nil {
		t.Fatalf("assign denied workspace viewer: %v", err)
	}

	userID := env.insertUser("suppress-bound-invisible@example.com")

	// SQLite's default SQLITE_MAX_VARIABLE_NUMBER is 32766; stay above it so
	// the old one-statement-per-row suppression exceeds the bind limit.
	const invisibleCount = 33000
	if _, err := env.db.Exec(`
		WITH RECURSIVE seq(n) AS (
			SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < ?
		)
		INSERT INTO notifications (user_id, title, message, type, timestamp, read, authorization_scope, workspace_id)
		SELECT ?, 'invisible-' || n, 'body', 'info', datetime('now', '+' || n || ' seconds'), false, ?, ?
		FROM seq
	`, invisibleCount, userID, models.NotificationScopeWorkspace, deniedWorkspaceID); err != nil {
		t.Fatalf("insert invisible notifications: %v", err)
	}

	service := NewNotificationService(env.db, nil, env.service, DefaultNotificationServiceConfig())
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("close notification service: %v", err)
		}
	})

	batches, err := service.UnreadEmailBatches(100)
	if err != nil {
		t.Fatalf("email batch run failed: %v", err)
	}
	if len(batches) != 0 {
		t.Fatalf("batches = %d, want 0 for an invisible-only recipient", len(batches))
	}

	var pending, suppressed int
	if err := env.db.QueryRow(`
		SELECT
			SUM(CASE WHEN email_delivery_state = 'pending' THEN 1 ELSE 0 END),
			SUM(CASE WHEN email_delivery_state = 'suppressed' THEN 1 ELSE 0 END)
		FROM notifications
		WHERE user_id = ?
	`, userID).Scan(&pending, &suppressed); err != nil {
		t.Fatalf("count notification states: %v", err)
	}
	if pending != 0 {
		t.Fatalf("pending invisible notifications = %d, want 0", pending)
	}
	if suppressed != invisibleCount {
		t.Fatalf("suppressed invisible notifications = %d, want %d", suppressed, invisibleCount)
	}
}
