package services

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestUnreadEmailBatchesReauthorizeWorkspaceNotifications(t *testing.T) {
	env := newPermTestEnv(t)
	userID := env.insertUser("notification-recipient@example.com")
	keeperID := env.insertUser("notification-keeper@example.com")
	allowedWorkspaceID := env.insertWorkspace("NOTIFY-ALLOWED")
	deniedWorkspaceID := env.insertWorkspace("NOTIFY-DENIED")
	viewerRoleID := env.roleID("Viewer")
	roles := repository.NewWorkspaceRoleRepository(env.db)
	for _, assignment := range []struct {
		userID, workspaceID int
	}{
		{userID, allowedWorkspaceID},
		{keeperID, allowedWorkspaceID},
		{keeperID, deniedWorkspaceID},
	} {
		if err := roles.AssignToUser(assignment.userID, assignment.workspaceID, viewerRoleID, keeperID); err != nil {
			t.Fatalf("assign workspace role: %v", err)
		}
	}

	insert := func(title, scope string, workspaceID *int) {
		t.Helper()
		if _, err := env.db.Exec(`
			INSERT INTO notifications (
				user_id, title, message, type, timestamp, read, authorization_scope, workspace_id
			) VALUES (?, ?, ?, 'info', CURRENT_TIMESTAMP, false, ?, ?)
		`, userID, title, title+" body", scope, workspaceID); err != nil {
			t.Fatalf("insert notification %s: %v", title, err)
		}
	}
	insert("allowed workspace", models.NotificationScopeWorkspace, &allowedWorkspaceID)
	insert("denied workspace", models.NotificationScopeWorkspace, &deniedWorkspaceID)
	insert("system notice", models.NotificationScopeSystem, nil)
	insert("legacy unknown", models.NotificationScopeLegacy, nil)

	service := NewNotificationService(env.db, nil, env.service, DefaultNotificationServiceConfig())
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("close notification service: %v", err)
		}
	})

	before, err := service.UnreadEmailBatches(100)
	if err != nil {
		t.Fatalf("email batches before revocation: %v", err)
	}
	assertNotificationTitles(t, before["notification-recipient@example.com"], "system notice", "allowed workspace")

	if _, err := roles.RevokeFromUser(userID, allowedWorkspaceID, viewerRoleID); err != nil {
		t.Fatalf("revoke workspace role: %v", err)
	}
	if err := env.service.InvalidateUserCache(userID); err != nil {
		t.Fatalf("invalidate permission cache: %v", err)
	}
	after, err := service.UnreadEmailBatches(100)
	if err != nil {
		t.Fatalf("email batches after revocation: %v", err)
	}
	assertNotificationTitles(t, after["notification-recipient@example.com"], "system notice")
}

func TestUnreadEmailBatchesReauthorizeGroupDerivedWorkspaceNotifications(t *testing.T) {
	env := newPermTestEnv(t)
	userID := env.insertUser("notification-group-recipient@example.com")
	workspaceID := env.insertWorkspace("NOTIFY-GROUP")
	groupID := env.insertGroup("notification-group", true)
	env.addGroupMember(groupID, userID)
	env.assignGroupWorkspaceRole(groupID, workspaceID, env.roleID("Viewer"))
	for _, row := range []struct {
		title, scope string
		workspaceID  *int
	}{
		{"group workspace", models.NotificationScopeWorkspace, &workspaceID},
		{"system notice", models.NotificationScopeSystem, nil},
	} {
		if _, err := env.db.Exec(`
			INSERT INTO notifications (
				user_id, title, message, type, timestamp, read, authorization_scope, workspace_id
			) VALUES (?, ?, ?, 'info', CURRENT_TIMESTAMP, false, ?, ?)
		`, userID, row.title, row.title+" body", row.scope, row.workspaceID); err != nil {
			t.Fatalf("insert notification %s: %v", row.title, err)
		}
	}

	service := NewNotificationService(env.db, nil, env.service, DefaultNotificationServiceConfig())
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("close notification service: %v", err)
		}
	})
	before, err := service.UnreadEmailBatches(100)
	if err != nil {
		t.Fatalf("email batches before group revocation: %v", err)
	}
	assertNotificationTitles(t, before["notification-group-recipient@example.com"], "system notice", "group workspace")

	if _, err := env.db.Exec("DELETE FROM group_members WHERE group_id = ? AND user_id = ?", groupID, userID); err != nil {
		t.Fatalf("remove group membership: %v", err)
	}
	if err := env.service.OnUserGroupMembershipChanged(userID, groupID); err != nil {
		t.Fatalf("invalidate group-derived permission cache: %v", err)
	}
	after, err := service.UnreadEmailBatches(100)
	if err != nil {
		t.Fatalf("email batches after group revocation: %v", err)
	}
	assertNotificationTitles(t, after["notification-group-recipient@example.com"], "system notice")
}

func assertNotificationTitles(t *testing.T, batch *UserNotificationBatch, want ...string) {
	t.Helper()
	if batch == nil {
		t.Fatalf("notification batch missing; want titles %v", want)
	}
	got := make([]string, len(batch.Notifications))
	for i, notification := range batch.Notifications {
		got[i] = notification.Title
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("notification titles = %v, want %v", got, want)
	}
}

type stubNotificationManager struct {
	mu             sync.Mutex
	notifications  []models.Notification
	notificationCh chan models.Notification
}

func newStubNotificationManager() *stubNotificationManager {
	return &stubNotificationManager{
		notificationCh: make(chan models.Notification, 10),
	}
}

func (s *stubNotificationManager) AddNotification(notification models.Notification) (models.Notification, error) {
	s.mu.Lock()
	s.notifications = append(s.notifications, notification)
	s.mu.Unlock()
	s.notificationCh <- notification
	return notification, nil
}

func (s *stubNotificationManager) AddNotifications(notifications []models.Notification) ([]models.Notification, error) {
	for _, notification := range notifications {
		if _, err := s.AddNotification(notification); err != nil {
			return nil, err
		}
	}
	return notifications, nil
}

func (s *stubNotificationManager) AddNotificationsContext(_ context.Context, notifications []models.Notification) ([]models.Notification, error) {
	return s.AddNotifications(notifications)
}

// The sent/failed/rollback bookkeeping is delivery-pipeline state the
// event-emission tests don't observe — no-ops keep the stub satisfying
// NotificationManager as the interface grows.
func (s *stubNotificationManager) MarkNotificationsSent([]int) error       { return nil }
func (s *stubNotificationManager) MarkNotificationsSendFailed([]int) error { return nil }
func (s *stubNotificationManager) RollbackNotificationsSent([]int) error   { return nil }

func (s *stubNotificationManager) DeleteUserNotifications(userID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.notifications[:0]
	for _, n := range s.notifications {
		if n.UserID != userID {
			kept = append(kept, n)
		}
	}
	s.notifications = kept
	return nil
}

func (s *stubNotificationManager) waitForNotification(t *testing.T, timeout time.Duration) models.Notification {
	t.Helper()

	select {
	case n := <-s.notificationCh:
		return n
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for notification")
		return models.Notification{}
	}
}

func (s *stubNotificationManager) expectNoNotification(t *testing.T, timeout time.Duration) {
	t.Helper()

	select {
	case n := <-s.notificationCh:
		t.Fatalf("expected no notification, but received %+v", n)
	case <-time.After(timeout):
		// expected outcome
	}
}

func TestNotificationService_AssignmentIncludesItemKey(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()

	env := seedBaseNotificationEnv(t, db)
	attachNotificationSettingAndRule(t, db, env)

	manager := newStubNotificationManager()
	service := NewNotificationService(db, manager, nil, NotificationServiceConfig{
		RefreshInterval: time.Hour,
		EventBufferSize: 10,
	})
	defer func() { _ = service.Close() }()

	itemID := 42
	itemKey := "TST-42"
	service.EmitEvent(&NotificationEvent{
		EventType:   models.EventItemAssigned,
		WorkspaceID: env.workspaceID,
		ActorUserID: env.actorUserID,
		ItemID:      itemID,
		AssigneeID:  &env.assigneeID,
		CreatorID:   &env.actorUserID,
		Title:       "Item Assigned",
		TemplateData: map[string]interface{}{
			"item.key":   itemKey,
			"item.title": "Important Item",
		},
	})

	notification := manager.waitForNotification(t, 2*time.Second)
	if notification.UserID != env.assigneeID {
		t.Fatalf("expected notification for user %d, got %d", env.assigneeID, notification.UserID)
	}
	if notification.Type != "assignment" {
		t.Fatalf("expected type 'assignment', got %s", notification.Type)
	}
	if !strings.Contains(notification.Message, itemKey) {
		t.Fatalf("expected message to contain %q, got %q", itemKey, notification.Message)
	}

	expectedURL := fmt.Sprintf("/workspaces/%d/items/%d", env.workspaceID, itemID)
	if notification.ActionURL != expectedURL {
		t.Fatalf("expected action URL %q, got %q", expectedURL, notification.ActionURL)
	}
}

func TestNotificationService_ForceRefreshLoadsNewAssignments(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()

	env := seedBaseNotificationEnv(t, db)

	manager := newStubNotificationManager()
	service := NewNotificationService(db, manager, nil, NotificationServiceConfig{
		RefreshInterval: time.Hour,
		EventBufferSize: 10,
	})
	defer func() { _ = service.Close() }()

	itemID := 55

	// Emit event before notification settings are linked - no notification expected
	service.EmitEvent(&NotificationEvent{
		EventType:   models.EventItemAssigned,
		WorkspaceID: env.workspaceID,
		ActorUserID: env.actorUserID,
		ItemID:      itemID,
		AssigneeID:  &env.assigneeID,
		Title:       "Assignment",
		TemplateData: map[string]interface{}{
			"item.key": "TST-55",
		},
	})
	manager.expectNoNotification(t, 200*time.Millisecond)

	attachNotificationSettingAndRule(t, db, env)
	if err := service.ForceRefreshCache(); err != nil {
		t.Fatalf("ForceRefreshCache failed: %v", err)
	}

	service.EmitEvent(&NotificationEvent{
		EventType:   models.EventItemAssigned,
		WorkspaceID: env.workspaceID,
		ActorUserID: env.actorUserID,
		ItemID:      itemID,
		AssigneeID:  &env.assigneeID,
		Title:       "Assignment",
		TemplateData: map[string]interface{}{
			"item.key": "TST-55",
		},
	})

	notification := manager.waitForNotification(t, 2*time.Second)
	if notification.UserID != env.assigneeID {
		t.Fatalf("expected notification for user %d, got %d", env.assigneeID, notification.UserID)
	}
}

type notificationTestEnv struct {
	workspaceID int
	configSetID int
	actorUserID int
	assigneeID  int
}

func createTestDB(t *testing.T) database.Database {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	return tdb.DB
}

func seedBaseNotificationEnv(t *testing.T, db database.Database) notificationTestEnv {
	t.Helper()

	adminID := insertUser(t, db, "admin@example.com", "admin")
	assigneeID := insertUser(t, db, "user@example.com", "assignee")

	workspaceID := insertWorkspace(t, db, "Test Workspace", "TST")
	configSetID := insertConfigurationSet(t, db, "Test Config")

	_, err := db.Exec(`
		INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id, created_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`, workspaceID, configSetID)
	if err != nil {
		t.Fatalf("failed to link workspace and configuration set: %v", err)
	}

	return notificationTestEnv{
		workspaceID: workspaceID,
		configSetID: configSetID,
		actorUserID: adminID,
		assigneeID:  assigneeID,
	}
}

func attachNotificationSettingAndRule(t *testing.T, db database.Database, env notificationTestEnv) {
	t.Helper()

	settingID := insertNotificationSetting(t, db, env.actorUserID)
	_, err := db.Exec(`
		INSERT INTO configuration_set_notification_settings (configuration_set_id, notification_setting_id, created_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`, env.configSetID, settingID)
	if err != nil {
		t.Fatalf("failed to link notification setting: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO notification_event_rules (
			notification_setting_id, event_type, is_enabled,
			notify_assignee, notify_creator, notify_watchers, notify_workspace_admins,
			custom_recipients, message_template, created_at, updated_at
		) VALUES (?, 'item.assigned', TRUE, TRUE, FALSE, FALSE, FALSE, NULL, NULL, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, settingID)
	if err != nil {
		t.Fatalf("failed to insert notification rule: %v", err)
	}
}

func insertUser(t *testing.T, db database.Database, email, username string) int {
	t.Helper()
	return testutils.InsertID(t, db, `
		INSERT INTO users (email, username, first_name, last_name, password_hash, is_active, created_at, updated_at)
		VALUES (?, ?, 'Test', 'User', 'hash', true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, email, username)
}

func insertWorkspace(t *testing.T, db database.Database, name, key string) int {
	t.Helper()
	return testutils.InsertID(t, db, `
		INSERT INTO workspaces (name, key, description, active, is_personal, created_at, updated_at)
		VALUES (?, ?, 'Test workspace', TRUE, FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, name, key)
}

func insertConfigurationSet(t *testing.T, db database.Database, name string) int {
	t.Helper()
	return testutils.InsertID(t, db, `
		INSERT INTO configuration_sets (name, description, is_default, created_at, updated_at)
		VALUES (?, 'Test config set', FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, name)
}

type fullNotificationTestEnv struct {
	workspaceID int
	configSetID int
	actorUserID int
	assigneeID  int
	creatorID   int // item creator, different from actor
	watcherID   int // watches the item
	itemID      int // item for watches/creator context
}

func seedFullNotificationEnv(t *testing.T, db database.Database) fullNotificationTestEnv {
	t.Helper()

	actorID := insertUser(t, db, "actor@example.com", "actor")
	assigneeID := insertUser(t, db, "assignee@example.com", "assignee")
	creatorID := insertUser(t, db, "creator@example.com", "creator")
	watcherID := insertUser(t, db, "watcher@example.com", "watcher")

	workspaceID := insertWorkspace(t, db, "Full Test Workspace", "FTW")
	configSetID := insertConfigurationSet(t, db, "Full Test Config")

	_, err := db.Exec(`
		INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id, created_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`, workspaceID, configSetID)
	if err != nil {
		t.Fatalf("failed to link workspace and configuration set: %v", err)
	}

	// Insert an item through the production create path so we can create
	// watches against it (rank allocation and defaults stay production-aligned).
	itemID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: workspaceID,
		Title:       "Test Item",
		AssigneeID:  &assigneeID,
		CreatorID:   &creatorID,
	})
	if err != nil {
		t.Fatalf("failed to create item: %v", err)
	}
	itemID := int(itemID64)

	// Add watcher on the item
	_, err = db.Exec(`
		INSERT INTO item_watches (item_id, user_id, is_active, watch_reason, created_at, updated_at)
		VALUES (?, ?, TRUE, 'test', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, itemID, watcherID)
	if err != nil {
		t.Fatalf("failed to insert item watch: %v", err)
	}

	// Make assignee an Administrator (for notify_workspace_admins tests)
	var roleID int
	err = db.QueryRow(`SELECT id FROM workspace_roles WHERE name = 'Administrator'`).Scan(&roleID)
	if err != nil {
		// Insert the Administrator role if it doesn't exist
		roleID = testutils.InsertID(t, db, `
			INSERT INTO workspace_roles (name, description, is_system, created_at, updated_at)
			VALUES ('Administrator', 'Workspace admin', TRUE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`)
	}

	_, err = db.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
	`, assigneeID, workspaceID, roleID)
	if err != nil {
		t.Fatalf("failed to insert user workspace role: %v", err)
	}

	return fullNotificationTestEnv{
		workspaceID: workspaceID,
		configSetID: configSetID,
		actorUserID: actorID,
		assigneeID:  assigneeID,
		creatorID:   creatorID,
		watcherID:   watcherID,
		itemID:      itemID,
	}
}

func attachAllEventRulesWithMixedFlags(t *testing.T, db database.Database, env fullNotificationTestEnv) {
	t.Helper()

	settingID := insertNotificationSetting(t, db, env.actorUserID)
	_, err := db.Exec(`
		INSERT INTO configuration_set_notification_settings (configuration_set_id, notification_setting_id, created_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`, env.configSetID, settingID)
	if err != nil {
		t.Fatalf("failed to link notification setting: %v", err)
	}

	type ruleSpec struct {
		eventType             string
		notifyAssignee        int
		notifyCreator         int
		notifyWatchers        int
		notifyWorkspaceAdmins int
	}

	rules := []ruleSpec{
		{models.EventItemCreated, 1, 0, 0, 0},
		{models.EventItemUpdated, 1, 0, 0, 0},
		{models.EventItemDeleted, 1, 0, 0, 0},
		{models.EventItemAssigned, 1, 0, 0, 0},
		{models.EventCommentCreated, 0, 1, 0, 0},
		{models.EventCommentUpdated, 0, 1, 0, 0},
		{models.EventCommentDeleted, 0, 1, 0, 0},
		{models.EventItemLinked, 0, 0, 1, 0},
		{models.EventItemUnlinked, 0, 0, 1, 0},
		{models.EventStatusChanged, 0, 0, 0, 1},
		{models.EventMention, 1, 0, 0, 0},
	}

	for _, r := range rules {
		_, err := db.Exec(`
			INSERT INTO notification_event_rules (
				notification_setting_id, event_type, is_enabled,
				notify_assignee, notify_creator, notify_watchers, notify_workspace_admins,
				custom_recipients, message_template, created_at, updated_at
			) VALUES (?, ?, TRUE, ?, ?, ?, ?, NULL, NULL, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, settingID, r.eventType, r.notifyAssignee, r.notifyCreator, r.notifyWatchers, r.notifyWorkspaceAdmins)
		if err != nil {
			t.Fatalf("failed to insert rule for %s: %v", r.eventType, err)
		}
	}
}

func TestNotificationService_AllEventTypes(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()

	env := seedFullNotificationEnv(t, db)
	attachAllEventRulesWithMixedFlags(t, db, env)

	manager := newStubNotificationManager()
	service := NewNotificationService(db, manager, nil, NotificationServiceConfig{
		RefreshInterval: time.Hour,
		EventBufferSize: 100,
	})
	defer func() { _ = service.Close() }()

	type testCase struct {
		name             string
		eventType        string
		title            string
		templateData     map[string]interface{}
		assigneeID       *int
		creatorID        *int
		expectedUserID   int
		expectedType     string
		expectedContains string
	}

	cases := []testCase{
		{
			name:             "item.created notifies assignee",
			eventType:        models.EventItemCreated,
			title:            "Item Created",
			templateData:     map[string]interface{}{"item.key": "FTW-1", "item.title": "Test Item"},
			assigneeID:       &env.assigneeID,
			creatorID:        &env.creatorID,
			expectedUserID:   env.assigneeID,
			expectedType:     "info",
			expectedContains: "New work item created",
		},
		{
			name:             "item.updated notifies assignee",
			eventType:        models.EventItemUpdated,
			title:            "Item Updated",
			templateData:     map[string]interface{}{"item.key": "FTW-1", "item.title": "Test Item"},
			assigneeID:       &env.assigneeID,
			creatorID:        &env.creatorID,
			expectedUserID:   env.assigneeID,
			expectedType:     "info",
			expectedContains: "Work item updated",
		},
		{
			name:             "item.deleted notifies assignee with warning",
			eventType:        models.EventItemDeleted,
			title:            "Item Deleted",
			templateData:     map[string]interface{}{"item.key": "FTW-1", "item.title": "Test Item"},
			assigneeID:       &env.assigneeID,
			creatorID:        &env.creatorID,
			expectedUserID:   env.assigneeID,
			expectedType:     "warning",
			expectedContains: "Work item deleted",
		},
		{
			name:             "item.assigned notifies assignee",
			eventType:        models.EventItemAssigned,
			title:            "Item Assigned",
			templateData:     map[string]interface{}{"item.key": "FTW-1", "item.title": "Test Item"},
			assigneeID:       &env.assigneeID,
			creatorID:        &env.creatorID,
			expectedUserID:   env.assigneeID,
			expectedType:     "assignment",
			expectedContains: "You have been assigned to",
		},
		{
			name:             "comment.created notifies creator",
			eventType:        models.EventCommentCreated,
			title:            "Comment Created",
			templateData:     map[string]interface{}{"item.key": "FTW-1", "item.title": "Test Item", "user.name": "Actor"},
			assigneeID:       &env.assigneeID,
			creatorID:        &env.creatorID,
			expectedUserID:   env.creatorID,
			expectedType:     "comment",
			expectedContains: "New comment added by",
		},
		{
			name:             "comment.updated notifies creator",
			eventType:        models.EventCommentUpdated,
			title:            "Comment Updated",
			templateData:     map[string]interface{}{"item.key": "FTW-1", "item.title": "Test Item", "user.name": "Actor"},
			assigneeID:       &env.assigneeID,
			creatorID:        &env.creatorID,
			expectedUserID:   env.creatorID,
			expectedType:     "comment",
			expectedContains: "Comment updated by",
		},
		{
			name:             "comment.deleted notifies creator",
			eventType:        models.EventCommentDeleted,
			title:            "Comment Deleted",
			templateData:     map[string]interface{}{"item.key": "FTW-1", "item.title": "Test Item", "user.name": "Actor"},
			assigneeID:       &env.assigneeID,
			creatorID:        &env.creatorID,
			expectedUserID:   env.creatorID,
			expectedType:     "comment",
			expectedContains: "Comment deleted by",
		},
		{
			name:             "item.linked notifies watcher",
			eventType:        models.EventItemLinked,
			title:            "Item Linked",
			templateData:     map[string]interface{}{"item.key": "FTW-1", "item.title": "Test Item"},
			assigneeID:       &env.assigneeID,
			creatorID:        &env.creatorID,
			expectedUserID:   env.watcherID,
			expectedType:     "info",
			expectedContains: "Work items linked",
		},
		{
			name:             "item.unlinked notifies watcher",
			eventType:        models.EventItemUnlinked,
			title:            "Item Unlinked",
			templateData:     map[string]interface{}{"item.key": "FTW-1", "item.title": "Test Item"},
			assigneeID:       &env.assigneeID,
			creatorID:        &env.creatorID,
			expectedUserID:   env.watcherID,
			expectedType:     "info",
			expectedContains: "Work item link removed",
		},
		{
			name:             "status.changed notifies workspace admins",
			eventType:        models.EventStatusChanged,
			title:            "Status Changed",
			templateData:     map[string]interface{}{"item.key": "FTW-1", "item.title": "Test Item", "status.name": "In Progress"},
			assigneeID:       &env.assigneeID,
			creatorID:        &env.creatorID,
			expectedUserID:   env.assigneeID, // assignee is the admin
			expectedType:     "status_change",
			expectedContains: "Status changed to",
		},
		{
			name:             "mention.created notifies assignee",
			eventType:        models.EventMention,
			title:            "Mention",
			templateData:     map[string]interface{}{"item.key": "FTW-1", "item.title": "Test Item", "actor.name": "Actor", "source.type": "comment"},
			assigneeID:       &env.assigneeID,
			creatorID:        &env.creatorID,
			expectedUserID:   env.assigneeID,
			expectedType:     "mention",
			expectedContains: "mentioned you",
		},
	}

	expectedURL := fmt.Sprintf("/workspaces/%d/items/%d", env.workspaceID, env.itemID)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service.EmitEvent(&NotificationEvent{
				EventType:    tc.eventType,
				WorkspaceID:  env.workspaceID,
				ActorUserID:  env.actorUserID,
				ItemID:       env.itemID,
				AssigneeID:   tc.assigneeID,
				CreatorID:    tc.creatorID,
				Title:        tc.title,
				TemplateData: tc.templateData,
			})

			notification := manager.waitForNotification(t, 2*time.Second)

			if notification.UserID != tc.expectedUserID {
				t.Errorf("expected UserID %d, got %d", tc.expectedUserID, notification.UserID)
			}
			if notification.Type != tc.expectedType {
				t.Errorf("expected Type %q, got %q", tc.expectedType, notification.Type)
			}
			if !strings.Contains(notification.Message, tc.expectedContains) {
				t.Errorf("expected Message to contain %q, got %q", tc.expectedContains, notification.Message)
			}
			if notification.ActionURL != expectedURL {
				t.Errorf("expected ActionURL %q, got %q", expectedURL, notification.ActionURL)
			}
		})
	}
}

func insertNotificationSetting(t *testing.T, db database.Database, creatorID int) int {
	t.Helper()
	return testutils.InsertID(t, db, `
		INSERT INTO notification_settings (name, description, is_active, created_by, created_at, updated_at)
		VALUES (?, 'Test notifications', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, "Test Settings", 1, creatorID)
}

// TestNotificationService_AgentRecipientFiltered confirms that a routing
// rule which would otherwise notify an assignee is dropped when the
// assignee row is flagged is_agent — covers owned agents and admin-
// provisioned service users (both wear the same flag). is_agent is
// immutable post-insert, so the agent recipient is created in-place
// rather than re-flagged.
func TestNotificationService_AgentRecipientFiltered(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()

	env := seedBaseNotificationEnv(t, db)
	attachNotificationSettingAndRule(t, db, env)

	agentID := testutils.InsertID(t, db, `
		INSERT INTO users (email, username, first_name, last_name, password_hash, is_active, is_agent, created_at, updated_at)
		VALUES ('agent@example.com', 'agent-recipient', 'Agent', 'User', 'hash', true, TRUE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)

	manager := newStubNotificationManager()
	service := NewNotificationService(db, manager, nil, NotificationServiceConfig{
		RefreshInterval: time.Hour,
		EventBufferSize: 10,
	})
	defer func() { _ = service.Close() }()

	service.EmitEvent(&NotificationEvent{
		EventType:   models.EventItemAssigned,
		WorkspaceID: env.workspaceID,
		ActorUserID: env.actorUserID,
		ItemID:      77,
		AssigneeID:  &agentID,
		CreatorID:   &env.actorUserID,
		Title:       "Item Assigned",
		TemplateData: map[string]interface{}{
			"item.key":   "TST-77",
			"item.title": "Routed To Agent",
		},
	})

	manager.expectNoNotification(t, 300*time.Millisecond)
}
