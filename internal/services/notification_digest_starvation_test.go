//go:build test

package services

import (
	"fmt"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
)

// WI-1768: a backlog of recipients whose pending notifications are all
// invisible must not starve a recipient with a deliverable notification. The
// invisible rows are terminally suppressed so they stop consuming the
// per-tick recipient budget.
func TestUnreadEmailBatchesServesVisibleRecipientBehindInvisibleBacklog(t *testing.T) {
	env := newPermTestEnv(t)
	deniedWorkspaceID := env.insertWorkspace("NOTIFY-STARVE-DENIED")
	// Give a different user an explicit Viewer role so the workspace stops
	// granting its default "everyone" permissions to users with no role.
	keeperID := env.insertUser("starved-keeper@example.com")
	if err := repository.NewWorkspaceRoleRepository(env.db).AssignToUser(keeperID, deniedWorkspaceID, env.roleID("Viewer"), keeperID); err != nil {
		t.Fatalf("assign denied workspace viewer: %v", err)
	}

	const invisibleRecipients = maxNotificationEmailRecipients + 50
	for i := 0; i < invisibleRecipients; i++ {
		userID := env.insertUser(fmt.Sprintf("starved-invisible-%03d@example.com", i))
		if _, err := env.db.Exec(`
			INSERT INTO notifications (user_id, title, message, type, timestamp, read, authorization_scope, workspace_id)
			VALUES (?, ?, 'body', 'info', CURRENT_TIMESTAMP, false, ?, ?)
		`, userID, fmt.Sprintf("invisible-%03d", i), models.NotificationScopeWorkspace, deniedWorkspaceID); err != nil {
			t.Fatalf("insert invisible notification %d: %v", i, err)
		}
	}
	visibleUserID := env.insertUser("starved-visible@example.com")
	if _, err := env.db.Exec(`
		INSERT INTO notifications (user_id, title, message, type, timestamp, read, authorization_scope)
		VALUES (?, 'visible notice', 'body', 'info', CURRENT_TIMESTAMP, false, ?)
	`, visibleUserID, models.NotificationScopeSystem); err != nil {
		t.Fatalf("insert visible notification: %v", err)
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
	assertNotificationTitles(t, first["starved-visible@example.com"], "visible notice")

	var pendingInvisible, suppressedInvisible int
	if err := env.db.QueryRow(`
		SELECT
			SUM(CASE WHEN email_delivery_state = 'pending' THEN 1 ELSE 0 END),
			SUM(CASE WHEN email_delivery_state = 'suppressed' THEN 1 ELSE 0 END)
		FROM notifications
		WHERE authorization_scope = ? AND workspace_id = ?
	`, models.NotificationScopeWorkspace, deniedWorkspaceID).Scan(&pendingInvisible, &suppressedInvisible); err != nil {
		t.Fatalf("count invisible notification states: %v", err)
	}
	if pendingInvisible != 0 {
		t.Fatalf("pending invisible notifications = %d, want 0 after suppression", pendingInvisible)
	}
	if suppressedInvisible != invisibleRecipients {
		t.Fatalf("suppressed invisible notifications = %d, want %d", suppressedInvisible, invisibleRecipients)
	}

	second, err := service.UnreadEmailBatches(100)
	if err != nil {
		t.Fatalf("second email batch run: %v", err)
	}
	for email := range second {
		if email != "starved-visible@example.com" {
			t.Fatalf("second run reselected invisible recipient %q", email)
		}
	}
}
