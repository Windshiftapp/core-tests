package services

import (
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

// makeDefaultNotificationScheme makes a single configuration set the workspace
// default (configuration_sets.is_default) and attaches a notification setting
// whose comment.created rule notifies the item creator. It clears is_default on
// every other config set first so the resolver's `WHERE is_default = true LIMIT 1`
// is deterministic regardless of what db.Initialize() seeded.
func makeDefaultNotificationScheme(t *testing.T, db database.Database, creatorOfSetting int) {
	t.Helper()

	if _, err := db.Exec(`UPDATE configuration_sets SET is_default = FALSE`); err != nil {
		t.Fatalf("failed to clear existing default config sets: %v", err)
	}

	defaultConfigSetID := testutils.InsertID(t, db, `
		INSERT INTO configuration_sets (name, description, is_default, created_at, updated_at)
		VALUES ('Default Scheme', 'fallback default', TRUE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)

	settingID := insertNotificationSetting(t, db, creatorOfSetting)
	if _, err := db.Exec(`
		INSERT INTO configuration_set_notification_settings (configuration_set_id, notification_setting_id, created_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`, defaultConfigSetID, settingID); err != nil {
		t.Fatalf("failed to link default notification setting: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO notification_event_rules (
			notification_setting_id, event_type, is_enabled,
			notify_assignee, notify_creator, notify_watchers, notify_workspace_admins,
			custom_recipients, message_template, created_at, updated_at
		) VALUES (?, 'comment.created', TRUE, FALSE, TRUE, FALSE, FALSE, NULL, NULL, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, settingID); err != nil {
		t.Fatalf("failed to insert default comment.created rule: %v", err)
	}
}

func insertPersonalWorkspace(t *testing.T, db database.Database, name, key string) int {
	t.Helper()
	id := testutils.InsertID(t, db, `
		INSERT INTO workspaces (name, key, description, active, is_personal, created_at, updated_at)
		VALUES (?, ?, 'Personal workspace', TRUE, TRUE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, name, key)
	return id
}

// newFallbackTestService builds a notification service with the standard
// fallback-test config (no permission service, manual refresh) and registers
// cleanup, returning it with its stub manager.
func newFallbackTestService(t *testing.T, db database.Database) (*NotificationService, *stubNotificationManager) {
	t.Helper()
	manager := newStubNotificationManager()
	service := NewNotificationService(db, manager, nil, NotificationServiceConfig{
		RefreshInterval: time.Hour,
		EventBufferSize: 10,
	})
	t.Cleanup(func() { _ = service.Close() })
	return service, manager
}

func emitComment(service *NotificationService, workspaceID, actorID, creatorID int) {
	service.EmitEvent(&NotificationEvent{
		EventType:   models.EventCommentCreated,
		WorkspaceID: workspaceID,
		ActorUserID: actorID,
		ItemID:      1,
		CreatorID:   &creatorID,
		Title:       "New Comment Added",
		TemplateData: map[string]interface{}{
			"item.key":   "TST-1",
			"item.title": "Item",
			"user.name":  "Actor",
		},
	})
}

// A workspace with no configuration set assigned falls back to the default
// scheme — mirroring how an unassigned workflow falls back to the default
// workflow. This is the bug the fallback fixes: comment.created on such a
// workspace previously produced no notification for the item creator.
func TestNotificationService_UnassignedWorkspaceFallsBackToDefault(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()

	actorID := insertUser(t, db, "actor@example.com", "actor")
	creatorID := insertUser(t, db, "creator@example.com", "creator")
	// Workspace deliberately has NO workspace_configuration_sets row.
	workspaceID := insertWorkspace(t, db, "Unassigned WS", "UNA")
	makeDefaultNotificationScheme(t, db, actorID)

	service, manager := newFallbackTestService(t, db)

	emitComment(service, workspaceID, actorID, creatorID)

	n := manager.waitForNotification(t, 2*time.Second)
	if n.UserID != creatorID {
		t.Fatalf("expected fallback notification for creator %d, got %d", creatorID, n.UserID)
	}
}

// Personal workspaces never notify, even when a default scheme exists — the
// fallback must not start notifying personal workspaces that previously
// resolved to config set 0.
func TestNotificationService_PersonalWorkspaceNeverNotifies(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()

	actorID := insertUser(t, db, "actor@example.com", "actor")
	creatorID := insertUser(t, db, "creator@example.com", "creator")
	workspaceID := insertPersonalWorkspace(t, db, "Personal WS", "PER")
	makeDefaultNotificationScheme(t, db, actorID)

	service, manager := newFallbackTestService(t, db)

	emitComment(service, workspaceID, actorID, creatorID)

	manager.expectNoNotification(t, 300*time.Millisecond)
}

// A workspace that HAS a config set whose scheme produces no rule for the event
// is honored as an intentional "no notifications" — the default fallback must
// NOT override it. Here the workspace's scheme only has an item.assigned rule,
// so comment.created yields nothing even though the default scheme would notify
// the creator.
func TestNotificationService_AssignedEmptySchemeNotOverriddenByDefault(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()

	actorID := insertUser(t, db, "actor@example.com", "actor")
	creatorID := insertUser(t, db, "creator@example.com", "creator")
	makeDefaultNotificationScheme(t, db, actorID)

	// Workspace with its own config set + scheme, but no comment.created rule.
	workspaceID := insertWorkspace(t, db, "Own Scheme WS", "OWN")
	configSetID := insertConfigurationSet(t, db, "Own Config")
	if _, err := db.Exec(`
		INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id, created_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`, workspaceID, configSetID); err != nil {
		t.Fatalf("failed to map workspace to config set: %v", err)
	}
	settingID := insertNotificationSetting(t, db, actorID)
	if _, err := db.Exec(`
		INSERT INTO configuration_set_notification_settings (configuration_set_id, notification_setting_id, created_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`, configSetID, settingID); err != nil {
		t.Fatalf("failed to link own notification setting: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO notification_event_rules (
			notification_setting_id, event_type, is_enabled,
			notify_assignee, notify_creator, notify_watchers, notify_workspace_admins,
			custom_recipients, message_template, created_at, updated_at
		) VALUES (?, 'item.assigned', TRUE, TRUE, FALSE, FALSE, FALSE, NULL, NULL, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, settingID); err != nil {
		t.Fatalf("failed to insert own item.assigned rule: %v", err)
	}

	service, manager := newFallbackTestService(t, db)

	emitComment(service, workspaceID, actorID, creatorID)

	manager.expectNoNotification(t, 300*time.Millisecond)
}
