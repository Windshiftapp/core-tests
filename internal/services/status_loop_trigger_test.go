//go:build test

package services

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/events"
	"windshift/internal/itemevents"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type statusLoopTestEnv struct {
	db          database.Database
	workspaceID int
	statusID    int
	itemTypeID  int
	actorUserID int
	itemID      int
}

// newStatusLoopTestEnv builds a workspace whose workflow has one status. When
// declareSelfTransition is set the workflow also declares the self-transition
// that makes a same-status update a status loop.
func newStatusLoopTestEnv(t *testing.T, declareSelfTransition bool) statusLoopTestEnv {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	db := tdb.DB

	insertID := func(query string, args ...any) int {
		t.Helper()
		return testutils.InsertID(t, db, query, args...)
	}

	workspaceID := insertID(`INSERT INTO workspaces (name, key, active, is_personal) VALUES ('Loop', 'LOOP', true, false)`)
	actorUserID := insertID(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('loop@example.com', 'loop', 'Loop', 'User')
	`)
	var categoryID int
	if err := db.QueryRow(`SELECT id FROM status_categories LIMIT 1`).Scan(&categoryID); err != nil {
		t.Fatalf("load status category: %v", err)
	}
	statusID := insertID(`INSERT INTO statuses (name, category_id) VALUES ('Loop review', ?)`, categoryID)
	workflowID := insertID(`INSERT INTO workflows (name, description, is_default) VALUES ('loop', '', false)`)
	insertID(`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id) VALUES (?, NULL, ?)`, workflowID, statusID)
	if declareSelfTransition {
		insertID(`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id) VALUES (?, ?, ?)`, workflowID, statusID, statusID)
	}
	configurationSetID := insertID(`INSERT INTO configuration_sets (name, workflow_id) VALUES ('loop', ?)`, workflowID)
	insertID(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, workspaceID, configurationSetID)
	var defaultItemTypeID int
	if err := db.QueryRow(`SELECT id FROM item_types WHERE is_default = true LIMIT 1`).Scan(&defaultItemTypeID); err != nil {
		t.Fatalf("load default item type: %v", err)
	}
	insertID(`INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id) VALUES (?, ?)`, configurationSetID, defaultItemTypeID)

	itemID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: workspaceID,
		ItemTypeID:  &defaultItemTypeID,
		Title:       "Loop item",
		StatusID:    &statusID,
		CreatorID:   &actorUserID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	return statusLoopTestEnv{
		db:          db,
		workspaceID: workspaceID,
		statusID:    statusID,
		itemTypeID:  defaultItemTypeID,
		actorUserID: actorUserID,
		itemID:      int(itemID64),
	}
}

func (env statusLoopTestEnv) transitionToSelf(t *testing.T) *PerformTransitionResult {
	t.Helper()
	result, err := NewWorkflowService(env.db).PerformTransition(context.Background(), PerformTransitionRequest{
		ItemID:      env.itemID,
		ToStatusID:  env.statusID,
		ActorUserID: env.actorUserID,
	}, repository.NewItemRepository(env.db), nil, nil)
	if err != nil {
		t.Fatalf("PerformTransition error = %v", err)
	}
	return result
}

func TestPerformTransitionRecordsDeclaredStatusLoop(t *testing.T) {
	env := newStatusLoopTestEnv(t, true)

	result := env.transitionToSelf(t)
	if !result.NoOp || !result.Loop {
		t.Fatalf("result NoOp=%v Loop=%v, want true/true", result.NoOp, result.Loop)
	}
	if result.OldStatusID == nil || *result.OldStatusID != env.statusID ||
		result.NewStatusID == nil || *result.NewStatusID != env.statusID {
		t.Fatalf("status ids = %v/%v, want %d/%d", result.OldStatusID, result.NewStatusID, env.statusID, env.statusID)
	}

	var historyCount int
	if err := env.db.QueryRow(`
		SELECT COUNT(*) FROM item_history
		WHERE item_id = ? AND field_name = 'status_id' AND old_value = ? AND new_value = ?
	`, env.itemID, strconv.Itoa(env.statusID), strconv.Itoa(env.statusID)).Scan(&historyCount); err != nil {
		t.Fatalf("count loop history: %v", err)
	}
	if historyCount != 1 {
		t.Fatalf("loop history rows = %d, want 1", historyCount)
	}

	var loopEvents int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE event_type = ?`, itemevents.StatusLooped).Scan(&loopEvents); err != nil {
		t.Fatalf("count loop events: %v", err)
	}
	if loopEvents != 1 {
		t.Fatalf("status_looped events = %d, want 1", loopEvents)
	}
}

func TestPerformTransitionIgnoresUndeclaredStatusLoop(t *testing.T) {
	env := newStatusLoopTestEnv(t, false)

	result := env.transitionToSelf(t)
	if !result.NoOp || result.Loop {
		t.Fatalf("result NoOp=%v Loop=%v, want true/false", result.NoOp, result.Loop)
	}

	var loopHistory int
	if err := env.db.QueryRow(`
		SELECT COUNT(*) FROM item_history
		WHERE item_id = ? AND field_name = 'status_id' AND old_value = new_value
	`, env.itemID).Scan(&loopHistory); err != nil {
		t.Fatalf("count loop history: %v", err)
	}
	if loopHistory != 0 {
		t.Fatalf("loop history rows = %d, want 0", loopHistory)
	}

	var loopEvents int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE event_type = ?`, itemevents.StatusLooped).Scan(&loopEvents); err != nil {
		t.Fatalf("count loop events: %v", err)
	}
	if loopEvents != 0 {
		t.Fatalf("status_looped events = %d, want 0", loopEvents)
	}
}

func TestActionEventFromDomainEventMapsStatusLooped(t *testing.T) {
	statusID := 42
	payload, err := json.Marshal(itemevents.StatusChangedV1{
		Item:        itemevents.ItemSnapshot{ID: 7, WorkspaceID: 3, StatusID: &statusID},
		OldStatusID: &statusID,
		NewStatusID: &statusID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	event := events.Event{
		ID: 1, Key: "status-looped-1", Type: itemevents.StatusLooped,
		PayloadVersion: itemevents.PayloadVersion, Payload: payload,
		OccurredAt: time.Now(), ActorKind: "user", ActorRef: "9",
	}

	actionEvent, consumerKey, err := actionEventFromDomainEvent(event)
	if err != nil {
		t.Fatalf("actionEventFromDomainEvent error = %v", err)
	}
	if consumerKey != DurableItemActionConsumerKey {
		t.Fatalf("consumer key = %q, want %q", consumerKey, DurableItemActionConsumerKey)
	}
	if actionEvent.EventType != models.ActionTriggerStatusLoop {
		t.Fatalf("event type = %q, want %q", actionEvent.EventType, models.ActionTriggerStatusLoop)
	}
	if actionEvent.OldValues["status_id"] != statusID || actionEvent.NewValues["status_id"] != statusID {
		t.Fatalf("status values = %v/%v, want %d/%d", actionEvent.OldValues["status_id"], actionEvent.NewValues["status_id"], statusID, statusID)
	}
}

func TestMatchesStatusLoop(t *testing.T) {
	statusID := 5
	itemTypeID := 9
	event := &models.ActionEvent{
		NewValues: map[string]any{"status_id": statusID, "item_type_id": itemTypeID},
	}
	tests := []struct {
		name   string
		config models.ActionTriggerConfig
		want   bool
	}{
		{name: "no filter matches", config: models.ActionTriggerConfig{}, want: true},
		{name: "matching status", config: models.ActionTriggerConfig{ToStatusID: &statusID}, want: true},
		{name: "other status", config: models.ActionTriggerConfig{ToStatusID: loopIntPtr(6)}, want: false},
		{name: "matching item type", config: models.ActionTriggerConfig{ItemTypeID: &itemTypeID}, want: true},
		{name: "other item type", config: models.ActionTriggerConfig{ItemTypeID: loopIntPtr(10)}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesStatusLoop(tt.config, event); got != tt.want {
				t.Fatalf("matchesStatusLoop() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestStatusLoopTriggerFiresAction proves the durable pipeline maps the loop
// fact to the status_loop trigger and leaves status_transition alone.
func TestStatusLoopTriggerFiresAction(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	statusID := data.StatusID

	repo := repository.NewActionRepository(db)
	loopActionID := createDurableTestAction(t, repo, data.WorkspaceID, "loop", models.ActionTriggerStatusLoop)
	transitionActionID := createDurableTestAction(t, repo, data.WorkspaceID, "transition", models.ActionTriggerStatusTransition)

	itemID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "loop trigger item",
		StatusID:    &statusID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	itemID := int(itemID64)

	payload, err := json.Marshal(itemevents.StatusChangedV1{
		Item:        itemevents.ItemSnapshot{ID: itemID, WorkspaceID: data.WorkspaceID, StatusID: &statusID},
		OldStatusID: &statusID,
		NewStatusID: &statusID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	event := events.Event{
		ID: 91, Key: "item-status-looped-91", WorkspaceID: &data.WorkspaceID,
		AggregateType: "item", AggregateID: strconv.Itoa(itemID), AggregateSequence: 1,
		Type: itemevents.StatusLooped, PayloadVersion: itemevents.PayloadVersion,
		OccurredAt: time.Now(), ActorKind: "user", ActorRef: strconv.Itoa(data.UserID), SourceKind: "test", Payload: payload,
	}

	service := NewActionService(db, DefaultActionServiceConfig(), nil)
	t.Cleanup(service.Stop)
	service.InvalidateWorkspaceCache(data.WorkspaceID)
	consumer := NewDurableActionConsumer(db, service)

	if err := consumer.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	var loopExecutions, transitionExecutions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM action_execution_logs WHERE action_id = ?`, loopActionID).Scan(&loopExecutions); err != nil {
		t.Fatalf("count loop executions: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM action_execution_logs WHERE action_id = ?`, transitionActionID).Scan(&transitionExecutions); err != nil {
		t.Fatalf("count transition executions: %v", err)
	}
	if loopExecutions != 1 {
		t.Fatalf("loop action executions = %d, want 1", loopExecutions)
	}
	if transitionExecutions != 0 {
		t.Fatalf("status_transition action executions = %d, want 0", transitionExecutions)
	}
}

func loopIntPtr(value int) *int { return &value }
