//go:build test

package services

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/events"
	"windshift/internal/itemevents"
	"windshift/internal/models"
)

type retryingNotificationManager struct {
	mu            sync.Mutex
	failuresLeft  int
	attempts      int
	notifications []models.Notification
	delivered     chan struct{}
}

func (m *retryingNotificationManager) AddNotification(notification models.Notification) (models.Notification, error) {
	stored, err := m.AddNotificationsContext(context.Background(), []models.Notification{notification})
	if err != nil || len(stored) == 0 {
		return notification, err
	}
	return stored[0], nil
}

func (m *retryingNotificationManager) AddNotifications(notifications []models.Notification) ([]models.Notification, error) {
	return m.AddNotificationsContext(context.Background(), notifications)
}

func (m *retryingNotificationManager) AddNotificationsContext(_ context.Context, notifications []models.Notification) ([]models.Notification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts++
	if m.failuresLeft > 0 {
		m.failuresLeft--
		return nil, errors.New("transient notification persistence failure")
	}
	m.notifications = append(m.notifications, notifications...)
	select {
	case m.delivered <- struct{}{}:
	default:
	}
	return notifications, nil
}

func (m *retryingNotificationManager) DeleteUserNotifications(int) error       { return nil }
func (m *retryingNotificationManager) MarkNotificationsSent([]int) error       { return nil }
func (m *retryingNotificationManager) MarkNotificationsSendFailed([]int) error { return nil }
func (m *retryingNotificationManager) RollbackNotificationsSent([]int) error   { return nil }

func durableNotificationEngine(db database.Database) *events.Engine {
	config := events.DefaultConfig()
	config.WorkerCount = 1
	config.PollInterval = 5 * time.Millisecond
	config.LeaseDuration = 500 * time.Millisecond
	config.HandlerTimeout = 200 * time.Millisecond
	config.ReconcileBatch = 20
	config.MaxAttempts = 3
	config.BaseRetryDelay = 5 * time.Millisecond
	config.MaxRetryDelay = 10 * time.Millisecond
	config.RetentionInterval = 0
	return events.NewEngine(db, config)
}

func attachDurableNotificationRule(t *testing.T, db database.Database, workspaceID, creatorID int, eventType string) {
	t.Helper()
	configSetID := insertConfigurationSet(t, db, "Durable notification config")
	if _, err := db.Exec(`
		INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id, created_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`, workspaceID, configSetID); err != nil {
		t.Fatalf("map notification config set: %v", err)
	}
	settingID := insertNotificationSetting(t, db, creatorID)
	if _, err := db.Exec(`
		INSERT INTO configuration_set_notification_settings (configuration_set_id, notification_setting_id, created_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`, configSetID, settingID); err != nil {
		t.Fatalf("attach notification setting: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO notification_event_rules (
			notification_setting_id, event_type, is_enabled, notify_assignee,
			notify_creator, notify_watchers, notify_workspace_admins,
			custom_recipients, message_template, created_at, updated_at
		) VALUES (?, ?, TRUE, FALSE, TRUE, FALSE, FALSE, NULL, NULL, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, settingID, eventType); err != nil {
		t.Fatalf("insert notification rule: %v", err)
	}
}

func newDurableNotificationService(t *testing.T, db database.Database, manager NotificationManager) *NotificationService {
	t.Helper()
	service := NewNotificationService(db, manager, nil, NotificationServiceConfig{
		RefreshInterval: time.Hour, EventBufferSize: 1, EventWorkers: 1,
	})
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func TestDurableNotificationAdmissionSurvivesRestartAndRetriesPersistence(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()
	actorID := insertUser(t, db, "durable-actor@example.com", "durable-actor")
	creatorID := insertUser(t, db, "durable-creator@example.com", "durable-creator")
	workspaceID := insertWorkspace(t, db, "Durable notifications", "DUR")
	attachDurableNotificationRule(t, db, workspaceID, creatorID, models.EventApprovalRequested)

	manager := &retryingNotificationManager{failuresLeft: 1, delivered: make(chan struct{}, 1)}
	firstService := newDurableNotificationService(t, db, manager)
	firstEngine := durableNotificationEngine(db)
	if err := PrepareDurableNotificationEngine(context.Background(), firstEngine, firstService); err != nil {
		t.Fatalf("prepare first engine: %v", err)
	}
	accepted := firstService.TryEmitEvent(&NotificationEvent{
		EventType: models.EventApprovalRequested, WorkspaceID: workspaceID,
		ActorUserID: actorID, ItemID: 41, CreatorID: &creatorID,
		Title: "Approval Requested", TemplateData: map[string]any{"item.key": "DUR-41"},
	})
	if !accepted {
		t.Fatal("durable notification admission was rejected")
	}
	if err := firstService.Close(); err != nil {
		t.Fatalf("close first service: %v", err)
	}

	secondService := newDurableNotificationService(t, db, manager)
	secondEngine := durableNotificationEngine(db)
	if err := PrepareDurableNotificationEngine(context.Background(), secondEngine, secondService); err != nil {
		t.Fatalf("prepare restarted engine: %v", err)
	}
	if err := secondEngine.Start(context.Background()); err != nil {
		t.Fatalf("start restarted engine: %v", err)
	}
	select {
	case <-manager.delivered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for retried durable notification")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := secondEngine.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown restarted engine: %v", err)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.attempts != 2 || len(manager.notifications) != 1 {
		t.Fatalf("attempts=%d notifications=%d, want 2 and 1", manager.attempts, len(manager.notifications))
	}
	if manager.notifications[0].DeliveryKey == "" {
		t.Fatal("durable notification has no replay key")
	}
}

func TestDurableNotificationAdmissionDoesNotDropAtLegacyQueueCapacity(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()
	manager := &retryingNotificationManager{delivered: make(chan struct{}, 1)}
	service := newDurableNotificationService(t, db, manager)
	engine := durableNotificationEngine(db)
	if err := PrepareDurableNotificationEngine(context.Background(), engine, service); err != nil {
		t.Fatalf("prepare engine: %v", err)
	}
	for i := 0; i < 20; i++ {
		if !service.TryEmitEvent(&NotificationEvent{EventType: models.EventApprovalRequested, WorkspaceID: 1, ItemID: i + 1}) {
			t.Fatalf("event %d rejected", i)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE event_type = ?`, DurableNotificationRequestedEvent).Scan(&count); err != nil {
		t.Fatalf("count durable notification events: %v", err)
	}
	if count != 20 {
		t.Fatalf("durable notification events = %d, want 20", count)
	}
}

func TestDurableNotificationSkipsImportedFacts(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()
	creatorID := insertUser(t, db, "import-notify-creator@example.com", "import-notify-creator")
	workspaceID := insertWorkspace(t, db, "Imported notifications", "IMN")
	attachDurableNotificationRule(t, db, workspaceID, creatorID, models.EventItemCreated)
	manager := &retryingNotificationManager{delivered: make(chan struct{}, 1)}
	service := newDurableNotificationService(t, db, manager)
	engine := durableNotificationEngine(db)
	if err := PrepareDurableNotificationEngine(context.Background(), engine, service); err != nil {
		t.Fatalf("prepare engine: %v", err)
	}

	item := &models.Item{ID: 7101, WorkspaceID: workspaceID, WorkspaceItemNumber: 1, Title: "Imported item", CreatorID: &creatorID}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin import transaction: %v", err)
	}
	if _, err := itemevents.NewRecorder(db).Created(context.Background(), tx, item, nil, itemevents.Import("jira-job-1")); err != nil {
		t.Fatalf("record imported item event: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit imported item event: %v", err)
	}
	created, err := engine.Store().Reconcile(context.Background(), 20)
	if err != nil {
		t.Fatalf("reconcile imported event: %v", err)
	}
	if created != 1 {
		t.Fatalf("imported event created %d deliveries, want 1", created)
	}
	delivery, err := engine.Store().Claim(context.Background(), DurableItemNotificationConsumerKey, "test", time.Now(), time.Minute)
	if err != nil || delivery == nil {
		t.Fatalf("claim imported delivery: delivery=%v err=%v", delivery, err)
	}
	if !itemevents.IsImportOrigin(delivery.Event.ActorKind, delivery.Event.SourceKind) {
		t.Fatalf("delivery origin = (%q, %q), want import", delivery.Event.ActorKind, delivery.Event.SourceKind)
	}
	consumer := &DurableNotificationConsumer{db: db, notifications: service}
	if err := consumer.Handle(context.Background(), delivery.Event); err != nil {
		t.Fatalf("handle imported delivery: %v", err)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.notifications) != 0 {
		t.Fatalf("imported fact produced %d notifications, want 0", len(manager.notifications))
	}
}

// TestDurableNotificationSkipsSuppressedFacts asserts the explicit
// suppress_side_effects marker (external reconciliation) keeps a canonical fact
// out of the notification pipeline even though it is not import origin.
func TestDurableNotificationSkipsSuppressedFacts(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()
	creatorID := insertUser(t, db, "suppressed-notify-creator@example.com", "suppressed-notify-creator")
	workspaceID := insertWorkspace(t, db, "Suppressed notifications", "SPN")
	attachDurableNotificationRule(t, db, workspaceID, creatorID, models.EventItemCreated)
	manager := &retryingNotificationManager{delivered: make(chan struct{}, 1)}
	service := newDurableNotificationService(t, db, manager)
	engine := durableNotificationEngine(db)
	if err := PrepareDurableNotificationEngine(context.Background(), engine, service); err != nil {
		t.Fatalf("prepare engine: %v", err)
	}

	item := &models.Item{ID: 7201, WorkspaceID: workspaceID, WorkspaceItemNumber: 1, Title: "Reconciled item", CreatorID: &creatorID}
	metadata := itemevents.System("github_issue_sync")
	metadata.SuppressSideEffects = true
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin reconciliation transaction: %v", err)
	}
	if _, err := itemevents.NewRecorder(db).Created(context.Background(), tx, item, nil, metadata); err != nil {
		t.Fatalf("record suppressed item event: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit suppressed item event: %v", err)
	}
	if _, err := engine.Store().Reconcile(context.Background(), 20); err != nil {
		t.Fatalf("reconcile suppressed event: %v", err)
	}
	delivery, err := engine.Store().Claim(context.Background(), DurableItemNotificationConsumerKey, "test", time.Now(), time.Minute)
	if err != nil || delivery == nil {
		t.Fatalf("claim suppressed delivery: delivery=%v err=%v", delivery, err)
	}
	if !itemevents.SuppressesSideEffects(delivery.Event.Type, delivery.Event.Payload) {
		t.Fatalf("delivery payload = %s, want suppress_side_effects", delivery.Event.Payload)
	}
	consumer := &DurableNotificationConsumer{db: db, notifications: service}
	if err := consumer.Handle(context.Background(), delivery.Event); err != nil {
		t.Fatalf("handle suppressed delivery: %v", err)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.notifications) != 0 {
		t.Fatalf("suppressed fact produced %d notifications, want 0", len(manager.notifications))
	}
}

// TestDurableNotificationSkipsImportedSideEffectEvents asserts provenance
// carried on a side-effect event (approvals, SLA, reminders) is honored the
// same way as on a canonical domain fact.
func TestDurableNotificationSkipsImportedSideEffectEvents(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()
	creatorID := insertUser(t, db, "import-sideeffect-creator@example.com", "import-sideeffect-creator")
	workspaceID := insertWorkspace(t, db, "Imported SLA notifications", "ISN")
	attachDurableNotificationRule(t, db, workspaceID, creatorID, models.EventSLABreached)
	manager := &retryingNotificationManager{delivered: make(chan struct{}, 1)}
	service := newDurableNotificationService(t, db, manager)
	engine := durableNotificationEngine(db)
	if err := PrepareDurableNotificationEngine(context.Background(), engine, service); err != nil {
		t.Fatalf("prepare engine: %v", err)
	}

	service.EmitEvent(&NotificationEvent{
		EventType: models.EventSLABreached, WorkspaceID: workspaceID, ItemID: 7301,
		CreatorID: &creatorID, Title: "SLA breached",
		ActorKind: "import", SourceKind: "import",
	})
	if _, err := engine.Store().Reconcile(context.Background(), 20); err != nil {
		t.Fatalf("reconcile side-effect event: %v", err)
	}
	delivery, err := engine.Store().Claim(context.Background(), DurableNotificationRequestedConsumerKey, "test", time.Now(), time.Minute)
	if err != nil || delivery == nil {
		t.Fatalf("claim side-effect delivery: delivery=%v err=%v", delivery, err)
	}
	if !itemevents.IsImportOrigin(delivery.Event.ActorKind, delivery.Event.SourceKind) {
		t.Fatalf("side-effect origin = (%q, %q), want import", delivery.Event.ActorKind, delivery.Event.SourceKind)
	}
	consumer := &DurableNotificationConsumer{db: db, notifications: service}
	if err := consumer.Handle(context.Background(), delivery.Event); err != nil {
		t.Fatalf("handle side-effect delivery: %v", err)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.notifications) != 0 {
		t.Fatalf("imported side effect produced %d notifications, want 0", len(manager.notifications))
	}
}

func TestCanonicalNotificationIntentCommitsAndRollsBackWithMutation(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()
	actorID := insertUser(t, db, "canonical-actor@example.com", "canonical-actor")
	creatorID := insertUser(t, db, "canonical-creator@example.com", "canonical-creator")
	workspaceID := insertWorkspace(t, db, "Canonical notifications", "CAN")
	attachDurableNotificationRule(t, db, workspaceID, creatorID, models.EventItemCreated)
	manager := &retryingNotificationManager{delivered: make(chan struct{}, 1)}
	service := newDurableNotificationService(t, db, manager)
	engine := durableNotificationEngine(db)
	if err := PrepareDurableNotificationEngine(context.Background(), engine, service); err != nil {
		t.Fatalf("prepare engine: %v", err)
	}

	item := &models.Item{ID: 7001, WorkspaceID: workspaceID, WorkspaceItemNumber: 1, Title: "Atomic notification", CreatorID: &creatorID}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin rollback transaction: %v", err)
	}
	if _, err := itemevents.NewRecorder(db).Created(context.Background(), tx, item, nil, itemevents.User(actorID, "test")); err != nil {
		t.Fatalf("record rolled-back item event: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback item event: %v", err)
	}
	created, err := engine.Store().Reconcile(context.Background(), 20)
	if err != nil {
		t.Fatalf("reconcile rolled-back event: %v", err)
	}
	if created != 0 {
		t.Fatalf("rolled-back mutation created %d notification deliveries", created)
	}

	tx, err = db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin commit transaction: %v", err)
	}
	if _, err := itemevents.NewRecorder(db).Created(context.Background(), tx, item, nil, itemevents.User(actorID, "test")); err != nil {
		t.Fatalf("record committed item event: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit item event: %v", err)
	}
	if err := engine.Start(context.Background()); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	select {
	case <-manager.delivered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for committed notification")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := engine.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown engine: %v", err)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.notifications) != 1 || manager.notifications[0].SourceType != models.EventItemCreated {
		t.Fatalf("notifications = %+v, want one item.created row", manager.notifications)
	}
}
