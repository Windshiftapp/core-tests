//go:build test

package services

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"windshift/internal/events"
	"windshift/internal/itemevents"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type cancellationProbeExecutor struct {
	started chan struct{}
	release chan struct{}
}

func (e *cancellationProbeExecutor) NodeType() models.ActionNodeType {
	return models.ActionNodeHTTPRequest
}

func (e *cancellationProbeExecutor) Execute(_ *models.ActionNode, ctx *models.ExecutionContext, _ *models.StepResult) error {
	close(e.started)
	select {
	case <-ctx.Context.Done():
		return ctx.Context.Err()
	case <-e.release:
		return nil
	}
}

func TestDurableActionConsumerCancelsInFlightExecution(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID, Title: "cancellable durable target",
		StatusID: &data.StatusID, CreatorID: &data.UserID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	repo := repository.NewActionRepository(db)
	actionID := createDurableTestAction(t, repo, data.WorkspaceID, "cancellable", models.ActionTriggerItemCreated)
	triggerID, err := repo.CreateNode(&models.ActionNode{ActionID: actionID, NodeType: models.ActionNodeTrigger, NodeConfig: "{}"})
	if err != nil {
		t.Fatalf("create trigger node: %v", err)
	}
	requestID, err := repo.CreateNode(&models.ActionNode{ActionID: actionID, NodeType: models.ActionNodeHTTPRequest, NodeConfig: "{}"})
	if err != nil {
		t.Fatalf("create probe node: %v", err)
	}
	if _, err := repo.CreateEdge(&models.ActionEdge{ActionID: actionID, SourceNodeID: triggerID, TargetNodeID: requestID}); err != nil {
		t.Fatalf("create action edge: %v", err)
	}

	service := NewActionService(db, DefaultActionServiceConfig(), nil)
	t.Cleanup(service.Stop)
	probe := &cancellationProbeExecutor{started: make(chan struct{}), release: make(chan struct{})}
	service.RegisterNodeExecutor(probe)
	service.InvalidateWorkspaceCache(data.WorkspaceID)
	consumer := NewDurableActionConsumer(db, service)
	event := durableCreatedEvent(t, 43, "item-created-cancel-43", data.WorkspaceID, int(itemID), data.UserID)

	handlerCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- consumer.Handle(handlerCtx, event) }()
	select {
	case <-probe.started:
	case <-time.After(2 * time.Second):
		close(probe.release)
		t.Fatal("action node did not start")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Handle() error = %v, want context cancellation", err)
		}
	case <-time.After(250 * time.Millisecond):
		close(probe.release)
		<-done
		t.Fatal("durable action continued after its delivery context was canceled")
	}
}

func TestDurableActionConsumerFreezesTargetsAndDeduplicatesExecution(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	itemID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "durable target item",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	itemID := int(itemID64)

	repo := repository.NewActionRepository(db)
	firstActionID := createDurableTestAction(t, repo, data.WorkspaceID, "first", models.ActionTriggerItemCreated)
	service := NewActionService(db, DefaultActionServiceConfig(), nil)
	t.Cleanup(service.Stop)
	service.InvalidateWorkspaceCache(data.WorkspaceID)
	consumer := NewDurableActionConsumer(db, service)
	event := durableCreatedEvent(t, 41, "item-created-41", data.WorkspaceID, itemID, data.UserID)

	if err := consumer.Handle(context.Background(), event); err != nil {
		t.Fatalf("first Handle() error = %v", err)
	}
	secondActionID := createDurableTestAction(t, repo, data.WorkspaceID, "enabled later", models.ActionTriggerItemCreated)
	service.InvalidateWorkspaceCache(data.WorkspaceID)
	if err := consumer.Handle(context.Background(), event); err != nil {
		t.Fatalf("retry Handle() error = %v", err)
	}

	var batchCount, targetCount, completedCount, executionCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM action_event_batches WHERE event_key = ?", event.Key).Scan(&batchCount); err != nil {
		t.Fatalf("count materialization batches: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*), COUNT(*) FILTER (WHERE state = 'completed') FROM action_event_targets WHERE event_key = ?", event.Key).Scan(&targetCount, &completedCount); err != nil {
		t.Fatalf("count durable targets: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM action_execution_logs WHERE durable_event_key = ?", event.Key).Scan(&executionCount); err != nil {
		t.Fatalf("count durable executions: %v", err)
	}
	if batchCount != 1 || targetCount != 1 || completedCount != 1 || executionCount != 1 {
		t.Fatalf("durable rows = batches:%d targets:%d completed:%d executions:%d, want 1/1/1/1", batchCount, targetCount, completedCount, executionCount)
	}
	var targetActionID int
	if err := db.QueryRow("SELECT action_id FROM action_event_targets WHERE event_key = ?", event.Key).Scan(&targetActionID); err != nil {
		t.Fatalf("load frozen target: %v", err)
	}
	if targetActionID != firstActionID || targetActionID == secondActionID {
		t.Fatalf("frozen target action = %d, want first action %d and not later action %d", targetActionID, firstActionID, secondActionID)
	}
}

func TestDurableActionConsumerSkipsImportedFacts(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	itemID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "imported target item",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	repo := repository.NewActionRepository(db)
	createDurableTestAction(t, repo, data.WorkspaceID, "imported", models.ActionTriggerItemCreated)
	service := NewActionService(db, DefaultActionServiceConfig(), nil)
	t.Cleanup(service.Stop)
	service.InvalidateWorkspaceCache(data.WorkspaceID)
	consumer := NewDurableActionConsumer(db, service)
	event := durableCreatedEvent(t, 42, "item-created-import-42", data.WorkspaceID, int(itemID64), data.UserID)
	event.ActorKind, event.ActorRef, event.SourceKind = "import", "jira-job-1", "import"

	if err := consumer.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	var batchCount, targetCount, executionCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM action_event_batches WHERE event_key = ?", event.Key).Scan(&batchCount); err != nil {
		t.Fatalf("count materialization batches: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM action_event_targets WHERE event_key = ?", event.Key).Scan(&targetCount); err != nil {
		t.Fatalf("count durable targets: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM action_execution_logs WHERE durable_event_key = ?", event.Key).Scan(&executionCount); err != nil {
		t.Fatalf("count durable executions: %v", err)
	}
	if batchCount != 0 || targetCount != 0 || executionCount != 0 {
		t.Fatalf("imported fact rows = batches:%d targets:%d executions:%d, want 0/0/0", batchCount, targetCount, executionCount)
	}
}

func TestDurableActionIngressNeverJournalsItemTriggers(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	ingress := NewDurableActionIngress(db)
	ctx := context.Background()

	// Item triggers are derived from canonical item facts, so the synchronous
	// legacy shape must never be journaled.
	if err := ingress.Emit(ctx, &models.ActionEvent{
		EventType: models.ActionTriggerItemUpdated, WorkspaceID: data.WorkspaceID,
		ItemID: 17, ActorUserID: data.UserID, NewValues: map[string]any{"title": "before"},
	}); err != nil {
		t.Fatalf("Emit() item trigger error = %v", err)
	}
	// SCM observations stay durable.
	if err := ingress.Emit(ctx, &models.ActionEvent{
		EventType: models.ActionTriggerSCMTagCreated, WorkspaceID: data.WorkspaceID,
		NewValues: map[string]any{"ref.name": "v0.8.8", "ref.sha": "abc123", "repo.workspace_repository_id": 9},
	}); err != nil {
		t.Fatalf("Emit() SCM event error = %v", err)
	}

	var sideEffectEvents, scmEvents int
	if err := db.QueryRow("SELECT COUNT(*) FROM domain_events WHERE event_type = ?", DurableActionRequestedEvent).Scan(&sideEffectEvents); err != nil {
		t.Fatalf("count side-effect events: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM domain_events WHERE event_type = ?", DurableSCMObservationEvent).Scan(&scmEvents); err != nil {
		t.Fatalf("count SCM events: %v", err)
	}
	if sideEffectEvents != 0 || scmEvents != 1 {
		t.Fatalf("durable ingress events = side_effect:%d scm:%d, want 0/1", sideEffectEvents, scmEvents)
	}

	cutover, err := ingress.ActivateCanonicalItems(ctx)
	if err != nil {
		t.Fatalf("ActivateCanonicalItems() error = %v", err)
	}
	store := events.NewStore(db)
	if err := ConfigureDurableActionConsumers(ctx, store, cutover); err != nil {
		t.Fatalf("ConfigureDurableActionConsumers() error = %v", err)
	}
	var active bool
	var startID int64
	if err := db.QueryRow("SELECT is_active, start_event_id FROM domain_event_consumers WHERE consumer_key = ?", DurableItemActionConsumerKey).Scan(&active, &startID); err != nil {
		t.Fatalf("load canonical item consumer: %v", err)
	}
	if !active || startID != cutover.StartEventID {
		t.Fatalf("canonical item consumer = active:%v start:%d, want true/%d", active, startID, cutover.StartEventID)
	}
}

func TestDurableActionConsumerRetriesFailedTargetWithoutRerunningSibling(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	itemID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID, Title: "partial target item",
		StatusID: &data.StatusID, CreatorID: &data.UserID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	repo := repository.NewActionRepository(db)
	successID := createDurableTestAction(t, repo, data.WorkspaceID, "success", models.ActionTriggerItemCreated)
	failingID := createDurableTestAction(t, repo, data.WorkspaceID, "failing", models.ActionTriggerItemCreated)
	firstNode, err := repo.CreateNode(&models.ActionNode{ActionID: failingID, NodeType: models.ActionNodeTrigger, NodeConfig: "{}"})
	if err != nil {
		t.Fatalf("create first cycle node: %v", err)
	}
	secondNode, err := repo.CreateNode(&models.ActionNode{ActionID: failingID, NodeType: models.ActionNodeTrigger, NodeConfig: "{}"})
	if err != nil {
		t.Fatalf("create second cycle node: %v", err)
	}
	for _, edge := range []models.ActionEdge{
		{ActionID: failingID, SourceNodeID: firstNode, TargetNodeID: secondNode},
		{ActionID: failingID, SourceNodeID: secondNode, TargetNodeID: firstNode},
	} {
		if _, err := repo.CreateEdge(&edge); err != nil {
			t.Fatalf("create cycle edge: %v", err)
		}
	}

	service := NewActionService(db, DefaultActionServiceConfig(), nil)
	t.Cleanup(service.Stop)
	service.InvalidateWorkspaceCache(data.WorkspaceID)
	consumer := NewDurableActionConsumer(db, service)
	event := durableCreatedEvent(t, 42, "item-created-42", data.WorkspaceID, int(itemID64), data.UserID)
	if err := consumer.Handle(context.Background(), event); err == nil {
		t.Fatal("first Handle() succeeded with a cyclic frozen action")
	}

	if err := repo.DeleteEdgesByActionID(failingID); err != nil {
		t.Fatalf("repair failing action: %v", err)
	}
	service.InvalidateWorkspaceCache(data.WorkspaceID)
	if err := consumer.Handle(context.Background(), event); err != nil {
		t.Fatalf("retry Handle() error = %v", err)
	}

	rows, err := db.Query(`
		SELECT action_id, state, attempt_count
		FROM action_event_targets
		WHERE event_key = ?
		ORDER BY action_id
	`, event.Key)
	if err != nil {
		t.Fatalf("query target attempts: %v", err)
	}
	defer func() { _ = rows.Close() }()
	want := []struct {
		actionID int
		attempts int
	}{{successID, 1}, {failingID, 2}}
	for index, expected := range want {
		if !rows.Next() {
			t.Fatalf("target %d missing", index)
		}
		var actionID, attempts int
		var state string
		if err := rows.Scan(&actionID, &state, &attempts); err != nil {
			t.Fatalf("scan target %d: %v", index, err)
		}
		if actionID != expected.actionID || state != "completed" || attempts != expected.attempts {
			t.Fatalf("target %d = action:%d state:%s attempts:%d, want %d/completed/%d", index, actionID, state, attempts, expected.actionID, expected.attempts)
		}
	}
	var executionCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM action_execution_logs WHERE durable_event_key = ?", event.Key).Scan(&executionCount); err != nil {
		t.Fatalf("count execution logs: %v", err)
	}
	if executionCount != 2 {
		t.Fatalf("execution logs = %d, want one stable log per frozen target", executionCount)
	}
}

func createDurableTestAction(t *testing.T, repo *repository.ActionRepository, workspaceID int, name string, trigger models.ActionTriggerType) int {
	t.Helper()
	id, err := repo.Create(&models.Action{
		WorkspaceID: workspaceID,
		Name:        name,
		IsEnabled:   true,
		TriggerType: trigger,
	})
	if err != nil {
		t.Fatalf("create action %q: %v", name, err)
	}
	return id
}

func durableCreatedEvent(t *testing.T, id int64, key string, workspaceID, itemID, actorUserID int) events.Event {
	t.Helper()
	payload, err := json.Marshal(itemevents.CreatedV1{
		Item: itemevents.ItemSnapshot{ID: itemID, WorkspaceID: workspaceID, Title: "durable target item"},
	})
	if err != nil {
		t.Fatalf("marshal item event: %v", err)
	}
	return events.Event{
		ID: id, Key: key, WorkspaceID: &workspaceID,
		AggregateType: "item", AggregateID: "17", AggregateSequence: 1,
		Type: itemevents.Created, PayloadVersion: itemevents.PayloadVersion,
		OccurredAt: time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC),
		ActorKind:  "user", ActorRef: strconv.Itoa(actorUserID), SourceKind: "test", Payload: payload,
	}
}
