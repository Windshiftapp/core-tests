//go:build test

package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"windshift/internal/assetevents"
	"windshift/internal/events"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestDurableAssetActionConsumerUsesSharedFrozenTargetsAndDeduplication(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	userID, setID, typeID, assetID := durableAssetFixture(t, db)
	repo := repository.NewAssetActionRepository(db)
	firstActionID := createDurableAssetAction(t, repo, setID, "first")
	service := NewAssetActionService(db, DefaultActionServiceConfig(), nil)
	t.Cleanup(service.Stop)
	service.InvalidateSetCache(setID)
	consumer := NewDurableAssetActionConsumer(db, service)
	event := durableAssetCreatedEvent(t, 51, "asset-created-51", userID, setID, typeID, assetID)

	if err := consumer.Handle(context.Background(), event); err != nil {
		t.Fatalf("first Handle() error = %v", err)
	}
	secondActionID := createDurableAssetAction(t, repo, setID, "enabled later")
	service.InvalidateSetCache(setID)
	if err := consumer.Handle(context.Background(), event); err != nil {
		t.Fatalf("retry Handle() error = %v", err)
	}

	var batchCount, targetCount, completedCount, executionCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM action_event_batches WHERE event_key = ? AND consumer_key = ?", event.Key, DurableAssetActionConsumerKey).Scan(&batchCount); err != nil {
		t.Fatalf("count materialization batches: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*), COUNT(*) FILTER (WHERE state = 'completed') FROM action_event_targets WHERE event_key = ?", event.Key).Scan(&targetCount, &completedCount); err != nil {
		t.Fatalf("count durable asset targets: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM asset_action_execution_logs WHERE durable_event_key = ?", event.Key).Scan(&executionCount); err != nil {
		t.Fatalf("count durable asset executions: %v", err)
	}
	if batchCount != 1 || targetCount != 1 || completedCount != 1 || executionCount != 1 {
		t.Fatalf("durable asset rows = batches:%d targets:%d completed:%d executions:%d, want 1/1/1/1", batchCount, targetCount, completedCount, executionCount)
	}
	var targetActionID int
	if err := db.QueryRow("SELECT action_id FROM action_event_targets WHERE event_key = ?", event.Key).Scan(&targetActionID); err != nil {
		t.Fatalf("load frozen asset target: %v", err)
	}
	if targetActionID != firstActionID || targetActionID == secondActionID {
		t.Fatalf("frozen target = %d, want first %d and not later %d", targetActionID, firstActionID, secondActionID)
	}
}

func TestDurableAssetIngressCutsCompatibilityAtCanonicalBoundary(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	userID, setID, _, assetID := durableAssetFixture(t, db)
	ingress := NewDurableAssetActionIngress(db)
	ctx := context.Background()
	legacy := &models.AssetActionEvent{
		EventType: models.AssetTriggerAssetUpdated, SetID: setID, AssetID: assetID,
		ActorUserID: userID, NewValues: map[string]any{"title": "before"},
	}
	if err := ingress.Emit(ctx, legacy); err != nil {
		t.Fatalf("Emit() before cutover error = %v", err)
	}
	cutover, err := ingress.ActivateCanonicalAssets(ctx)
	if err != nil {
		t.Fatalf("ActivateCanonicalAssets() error = %v", err)
	}
	legacy.NewValues["title"] = "after"
	if err := ingress.Emit(ctx, legacy); err != nil {
		t.Fatalf("Emit() after cutover error = %v", err)
	}
	var compatibilityEvents int
	if err := db.QueryRow("SELECT COUNT(*) FROM domain_events WHERE event_type = ?", DurableAssetActionCompatibilityEvent).Scan(&compatibilityEvents); err != nil {
		t.Fatalf("count asset compatibility events: %v", err)
	}
	if compatibilityEvents != 1 {
		t.Fatalf("asset compatibility events = %d, want 1", compatibilityEvents)
	}
	if err := ConfigureDurableAssetActionConsumers(ctx, events.NewStore(db), cutover); err != nil {
		t.Fatalf("ConfigureDurableAssetActionConsumers() error = %v", err)
	}
	var active bool
	var startID int64
	if err := db.QueryRow("SELECT is_active, start_event_id FROM domain_event_consumers WHERE consumer_key = ?", DurableAssetActionConsumerKey).Scan(&active, &startID); err != nil {
		t.Fatalf("load canonical asset consumer: %v", err)
	}
	if !active || startID != cutover.StartEventID {
		t.Fatalf("canonical asset consumer = active:%v start:%d, want true/%d", active, startID, cutover.StartEventID)
	}
}

func TestSharedDurableEngineProcessesMixedItemAndAssetActions(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	userID, setID, typeID, _ := durableAssetFixtureWithEmail(t, db, "mixed-durable-asset@example.test")

	itemRepo := repository.NewActionRepository(db)
	createDurableTestAction(t, itemRepo, data.WorkspaceID, "mixed item", models.ActionTriggerItemCreated)
	assetRepo := repository.NewAssetActionRepository(db)
	createDurableAssetAction(t, assetRepo, setID, "mixed asset")
	chainStore := NewExecutionChainStore()
	itemActions := NewActionService(db, DefaultActionServiceConfig(), chainStore)
	assetActions := NewAssetActionService(db, DefaultActionServiceConfig(), chainStore)
	t.Cleanup(itemActions.Stop)
	t.Cleanup(assetActions.Stop)
	itemActions.InvalidateWorkspaceCache(data.WorkspaceID)
	assetActions.InvalidateSetCache(setID)

	config := events.DefaultConfig()
	config.WorkerCount = 2
	config.PollInterval = 5 * time.Millisecond
	config.HandlerTimeout = time.Second
	config.LeaseDuration = 2 * time.Second
	engine := events.NewEngine(db, config)
	if err := PrepareDurableActionEngine(context.Background(), engine, itemActions, true); err != nil {
		t.Fatalf("prepare item consumer: %v", err)
	}
	if err := PrepareDurableAssetActionEngine(context.Background(), engine, assetActions, true); err != nil {
		t.Fatalf("prepare asset consumer: %v", err)
	}
	if err := engine.Start(context.Background()); err != nil {
		t.Fatalf("start shared engine: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = engine.Shutdown(ctx)
	})

	if _, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID, Title: "Mixed durable item",
		StatusID: &data.StatusID, CreatorID: &data.UserID,
	}); err != nil {
		t.Fatalf("create mixed item: %v", err)
	}
	if _, err := NewAssetService(db, repository.NewAssetRepository(db)).CreateAsset(AuditActor{UserID: userID}, repository.CreateAssetInput{
		SetID: setID, AssetTypeID: typeID, Title: "Mixed durable asset", CreatedBy: userID, CreatedAt: time.Now().UTC(),
	}, nil); err != nil {
		t.Fatalf("create mixed asset: %v", err)
	}

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var itemLogs, assetLogs, completed int
		if err := db.QueryRow("SELECT COUNT(*) FROM action_execution_logs WHERE durable_event_key IS NOT NULL").Scan(&itemLogs); err != nil {
			t.Fatalf("count item action logs: %v", err)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM asset_action_execution_logs WHERE durable_event_key IS NOT NULL").Scan(&assetLogs); err != nil {
			t.Fatalf("count asset action logs: %v", err)
		}
		if err := db.QueryRow(`
			SELECT COUNT(*) FROM domain_event_deliveries
			WHERE consumer_key IN (?, ?) AND state = 'completed'
		`, DurableItemActionConsumerKey, DurableAssetActionConsumerKey).Scan(&completed); err != nil {
			t.Fatalf("count completed mixed deliveries: %v", err)
		}
		if itemLogs == 1 && assetLogs == 1 && completed == 2 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("mixed durable results = item logs:%d asset logs:%d completed deliveries:%d, want 1/1/2", itemLogs, assetLogs, completed)
		case <-ticker.C:
		}
	}
}

func TestDurableAssetActionEngineProcessesEventsWrittenWhileStoppedAfterRestart(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	userID, setID, typeID, _ := durableAssetFixtureWithEmail(t, db, "durable-restart@example.test")
	repo := repository.NewAssetActionRepository(db)
	createDurableAssetAction(t, repo, setID, "restart asset")
	actions := NewAssetActionService(db, DefaultActionServiceConfig(), NewExecutionChainStore())
	t.Cleanup(actions.Stop)
	actions.InvalidateSetCache(setID)
	assets := NewAssetService(db, repository.NewAssetRepository(db))

	config := events.DefaultConfig()
	config.WorkerCount = 1
	config.PollInterval = 5 * time.Millisecond
	config.HandlerTimeout = time.Second
	config.LeaseDuration = 2 * time.Second
	startEngine := func(activate bool) *events.Engine {
		t.Helper()
		engine := events.NewEngine(db, config)
		if err := PrepareDurableAssetActionEngine(context.Background(), engine, actions, activate); err != nil {
			t.Fatalf("prepare asset consumer: %v", err)
		}
		if err := engine.Start(context.Background()); err != nil {
			t.Fatalf("start asset engine: %v", err)
		}
		return engine
	}
	stopEngine := func(engine *events.Engine) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := engine.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown asset engine: %v", err)
		}
	}

	firstEngine := startEngine(true)
	if _, err := assets.CreateAsset(AuditActor{UserID: userID}, repository.CreateAssetInput{
		SetID: setID, AssetTypeID: typeID, Title: "Before restart", AssetTag: "AST-R1", CreatedBy: userID, CreatedAt: time.Now().UTC(),
	}, nil); err != nil {
		t.Fatalf("create asset before restart: %v", err)
	}
	waitForDurableAssetExecutions(t, db, 1)
	stopEngine(firstEngine)

	if _, err := assets.CreateAsset(AuditActor{UserID: userID}, repository.CreateAssetInput{
		SetID: setID, AssetTypeID: typeID, Title: "While stopped", AssetTag: "AST-R2", CreatedBy: userID, CreatedAt: time.Now().UTC(),
	}, nil); err != nil {
		t.Fatalf("create asset while engine stopped: %v", err)
	}
	var executionsWhileStopped int
	if err := db.QueryRow("SELECT COUNT(*) FROM asset_action_execution_logs WHERE durable_event_key IS NOT NULL").Scan(&executionsWhileStopped); err != nil {
		t.Fatalf("count executions while stopped: %v", err)
	}
	if executionsWhileStopped != 1 {
		t.Fatalf("executions while stopped = %d, want 1", executionsWhileStopped)
	}

	secondEngine := startEngine(false)
	t.Cleanup(func() { stopEngine(secondEngine) })
	waitForDurableAssetExecutions(t, db, 2)
}

func TestDurableAssetIngressDoesNotDropEventsAtLegacyBufferCapacity(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	userID, setID, _, assetID := durableAssetFixtureWithEmail(t, db, "durable-saturation@example.test")
	config := DefaultActionServiceConfig()
	config.EventBufferSize = 1
	service := NewAssetActionService(db, config, nil)
	t.Cleanup(service.Stop)

	const eventCount = 32
	for index := 0; index < eventCount; index++ {
		service.EmitAssetActionEvent(&models.AssetActionEvent{
			EventType: models.AssetTriggerAssetUpdated,
			SetID:     setID, AssetID: assetID, ActorUserID: userID,
			NewValues: map[string]any{"sequence": index},
		})
	}
	var persisted int
	if err := db.QueryRow("SELECT COUNT(*) FROM domain_events WHERE event_type = ?", DurableAssetActionCompatibilityEvent).Scan(&persisted); err != nil {
		t.Fatalf("count saturated compatibility events: %v", err)
	}
	if persisted != eventCount {
		t.Fatalf("persisted compatibility events = %d, want %d", persisted, eventCount)
	}
}

func waitForDurableAssetExecutions(t *testing.T, db interface {
	QueryRow(string, ...any) *sql.Row
}, want int) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM asset_action_execution_logs WHERE durable_event_key IS NOT NULL").Scan(&count); err != nil {
			t.Fatalf("count durable asset executions: %v", err)
		}
		if count == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("durable asset executions = %d, want %d", count, want)
		case <-ticker.C:
		}
	}
}

func durableAssetFixture(t *testing.T, db interface {
	QueryRow(string, ...any) *sql.Row
}) (userID, setID, typeID, assetID int) {
	return durableAssetFixtureWithEmail(t, db, "durable-asset@example.test")
}

func durableAssetFixtureWithEmail(t *testing.T, db interface {
	QueryRow(string, ...any) *sql.Row
}, email string) (userID, setID, typeID, assetID int) {
	t.Helper()
	insert := func(label, query string, args ...any) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		return id
	}
	userID = insert("user", "INSERT INTO users (email, username, first_name, last_name) VALUES (?, ?, 'Durable', 'Asset') RETURNING id", email, email)
	setID = insert("asset set", "INSERT INTO asset_management_sets (name, created_by) VALUES (?, ?) RETURNING id", "Durable assets "+email, userID)
	typeID = insert("asset type", "INSERT INTO asset_types (set_id, name) VALUES (?, 'Server') RETURNING id", setID)
	assetID = insert("asset", "INSERT INTO assets (set_id, asset_type_id, title, asset_tag, created_by) VALUES (?, ?, 'Durable server', 'AST-1', ?) RETURNING id", setID, typeID, userID)
	return userID, setID, typeID, assetID
}

func createDurableAssetAction(t *testing.T, repo *repository.AssetActionRepository, setID int, name string) int {
	t.Helper()
	id, err := repo.Create(&models.AssetAction{SetID: setID, Name: name, IsEnabled: true, TriggerType: models.AssetTriggerAssetCreated})
	if err != nil {
		t.Fatalf("create asset action %q: %v", name, err)
	}
	return id
}

func durableAssetCreatedEvent(t *testing.T, id int64, key string, actorUserID, setID, typeID, assetID int) events.Event {
	t.Helper()
	payload, err := json.Marshal(assetevents.CreatedV1{
		Asset:     assetevents.AssetSnapshot{ID: assetID, SetID: setID, AssetTypeID: typeID, Title: "Durable server", AssetTag: "AST-1"},
		NewValues: map[string]any{"title": "Durable server", "asset_type_id": typeID, "asset_tag": "AST-1"},
	})
	if err != nil {
		t.Fatalf("marshal asset event: %v", err)
	}
	return events.Event{
		ID: id, Key: key, AggregateType: "asset", AggregateID: strconv.Itoa(assetID), AggregateSequence: 1,
		Type: assetevents.Created, PayloadVersion: assetevents.PayloadVersion,
		OccurredAt: time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC),
		ActorKind:  "user", ActorRef: strconv.Itoa(actorUserID), SourceKind: "test", Payload: payload,
	}
}
