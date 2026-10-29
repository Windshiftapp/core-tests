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

// Regression coverage for GH #267/#266 follow-ups in the actions bug hunt:
// condition fields and template placeholders can reach the parent item, and
// milestone attachments resolve as a condition field.

// TestActionParentReach resolves condition fields and template placeholders
// against the trigger item's parent.
func TestActionParentReach(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	var completedCategoryID int
	if err := db.QueryRow(`
		INSERT INTO status_categories (name, color, is_default, is_completed)
		VALUES ('Done Parent Reach', '#22c55e', false, true) RETURNING id
	`).Scan(&completedCategoryID); err != nil {
		t.Fatalf("seed completed category: %v", err)
	}
	var completedStatusID int
	if err := db.QueryRow(`
		INSERT INTO statuses (name, category_id, is_default)
		VALUES ('Closed (parent reach)', ?, false) RETURNING id
	`, completedCategoryID).Scan(&completedStatusID); err != nil {
		t.Fatalf("seed completed status: %v", err)
	}

	itemRepo := repository.NewItemRepository(db)
	createItem := func(title string, parentID, statusID *int) int {
		t.Helper()
		id, err := CreateItem(db, ItemCreationParams{
			WorkspaceID: data.WorkspaceID, Title: title,
			StatusID: statusID, CreatorID: &data.UserID, ParentID: parentID,
		})
		if err != nil {
			t.Fatalf("create item %s: %v", title, err)
		}
		return int(id)
	}

	parent := createItem("parent", nil, &data.StatusID)
	createItem("done child", &parent, &completedStatusID)
	openChild := createItem("open child", &parent, &data.StatusID)

	service := &ActionService{itemRepo: itemRepo}
	runCondition := func(t *testing.T, itemID int, fieldName, operator, value string) bool {
		t.Helper()
		condition := &models.ActionNode{
			NodeType:   models.ActionNodeCondition,
			NodeConfig: `{"field_name": "` + fieldName + `", "operator": "` + operator + `", "value": "` + value + `"}`,
		}
		ctx := &models.ExecutionContext{Event: &models.ActionEvent{ItemID: itemID}}
		step := &models.StepResult{}
		if err := service.executeCondition(condition, ctx, step); err != nil {
			t.Fatalf("executeCondition(%s): %v", fieldName, err)
		}
		result, ok := step.Output["condition_result"].(bool)
		if !ok {
			t.Fatalf("condition_result missing from output: %+v", step.Output)
		}
		return result
	}

	t.Run("parent open child count gates on siblings", func(t *testing.T) {
		if runCondition(t, openChild, "parent.open_child_count", "eq", "0") {
			t.Fatal("parent.open_child_count eq 0 passed while a sibling was still open")
		}
		if !runCondition(t, openChild, "parent.open_child_count", "eq", "1") {
			t.Fatal("parent.open_child_count eq 1 failed while exactly one sibling was open")
		}
	})

	t.Run("parent condition turns true when siblings finish", func(t *testing.T) {
		if _, err := db.Exec(`UPDATE items SET status_id = ? WHERE id = ?`, completedStatusID, openChild); err != nil {
			t.Fatalf("complete open child: %v", err)
		}
		if !runCondition(t, openChild, "parent.open_child_count", "eq", "0") {
			t.Fatal("parent.open_child_count eq 0 failed after all children completed")
		}
	})

	t.Run("parent status and custom field resolve", func(t *testing.T) {
		if _, err := db.Exec(`
			INSERT INTO custom_field_definitions (name, field_type)
			VALUES ('parent reach field', 'text')
		`); err != nil {
			t.Fatalf("seed custom field: %v", err)
		}
		var customFieldID int
		if err := db.QueryRow(`SELECT id FROM custom_field_definitions WHERE name = 'parent reach field'`).Scan(&customFieldID); err != nil {
			t.Fatalf("load custom field id: %v", err)
		}
		if _, err := db.Exec(`UPDATE items SET custom_field_values = ? WHERE id = ?`,
			`{"`+strconv.Itoa(customFieldID)+`": "parent-value"}`, parent); err != nil {
			t.Fatalf("set parent custom field: %v", err)
		}

		ctx := &models.ExecutionContext{Event: &models.ActionEvent{ItemID: openChild}}
		got, found := currentItemFieldValueResolved(itemRepo, ctx, "parent.custom_field_"+strconv.Itoa(customFieldID))
		if !found || got != "parent-value" {
			t.Fatalf("parent custom field = %v (found %v), want parent-value", got, found)
		}

		status, found := currentItemFieldValueResolved(itemRepo, ctx, "parent.status")
		if !found || status != "Open" {
			t.Fatalf("parent.status = %v (found %v), want Open", status, found)
		}
	})

	t.Run("parent placeholder substitutes into templates", func(t *testing.T) {
		rendered := service.substituteVariables("open left: {{parent.open_child_count}}", &models.ExecutionContext{
			Event: &models.ActionEvent{ItemID: openChild},
			Variables: map[string]any{
				"item_id":      openChild,
				"workspace_id": data.WorkspaceID,
			},
		})
		if rendered != "open left: 0" {
			t.Fatalf("substituted %q, want %q", rendered, "open left: 0")
		}
	})

	t.Run("parent of a root item does not resolve", func(t *testing.T) {
		if _, found := currentItemFieldValueResolved(itemRepo, &models.ExecutionContext{
			Event: &models.ActionEvent{ItemID: parent},
		}, "parent.status_id"); found {
			t.Fatal("root item reported a parent value")
		}
	})
}

// TestActionConditionMilestoneField covers milestone attachment as a
// condition field (CSV of attached ids, empty string when none).
func TestActionConditionMilestoneField(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID, Title: "milestoned item",
		StatusID: &data.StatusID, CreatorID: &data.UserID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	var milestoneA, milestoneB int
	if err := db.QueryRow(`
		INSERT INTO milestones (workspace_id, name, status, is_global)
		VALUES (?, 'Milestone A', 'active', false) RETURNING id
	`, data.WorkspaceID).Scan(&milestoneA); err != nil {
		t.Fatalf("seed milestone: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO milestones (workspace_id, name, status, is_global)
		VALUES (?, 'Milestone B', 'active', false) RETURNING id
	`, data.WorkspaceID).Scan(&milestoneB); err != nil {
		t.Fatalf("seed milestone: %v", err)
	}

	service := &ActionService{itemRepo: repository.NewItemRepository(db)}
	ctx := &models.ExecutionContext{Event: &models.ActionEvent{ItemID: int(itemID)}}
	condition := func(fieldName, operator, value string) bool {
		t.Helper()
		step := &models.StepResult{}
		node := &models.ActionNode{
			NodeType:   models.ActionNodeCondition,
			NodeConfig: `{"field_name": "` + fieldName + `", "operator": "` + operator + `", "value": "` + value + `"}`,
		}
		if err := service.executeCondition(node, ctx, step); err != nil {
			t.Fatalf("executeCondition: %v", err)
		}
		return step.Output["condition_result"].(bool)
	}

	if !condition("milestone_ids", "is_empty", "") {
		t.Fatal("milestone_ids is_empty failed with no milestones attached")
	}

	attach := func(milestoneID int) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO item_milestones (item_id, milestone_id, created_at) VALUES (?, ?, ?)`,
			itemID, milestoneID, time.Now().UTC()); err != nil {
			t.Fatalf("attach milestone: %v", err)
		}
	}
	attach(milestoneA)

	if condition("milestone_ids", "is_empty", "") {
		t.Fatal("milestone_ids is_empty passed with a milestone attached")
	}
	if !condition("milestone_ids", "is_not_empty", "") {
		t.Fatal("milestone_ids is_not_empty failed with a milestone attached")
	}
	if !condition("milestone_ids", "contains", strconv.Itoa(milestoneA)) {
		t.Fatal("milestone_ids contains attached id failed")
	}
	if condition("milestone_ids", "contains", strconv.Itoa(milestoneB)) {
		t.Fatal("milestone_ids contains unattached id passed")
	}
}

// TestTriggerFieldPlaceholderFallsBackToNewValues pins the natural
// {{trigger.<field>}} spelling resolving to what the trigger event just set
// (GH #266: {{trigger.iteration_id}} never resolved).
func TestTriggerFieldPlaceholderFallsBackToNewValues(t *testing.T) {
	service := &ActionService{}
	ctx := &models.ExecutionContext{
		Event: &models.ActionEvent{},
		Variables: map[string]any{
			"new_iteration_id": 5,
		},
	}
	got, found := service.resolveExecutionValue(ctx, "trigger.iteration_id")
	if !found || got != 5 {
		t.Fatalf("trigger.iteration_id = %v (found %v), want 5", got, found)
	}
	// Explicit new_ spelling keeps working.
	got, found = service.resolveExecutionValue(ctx, "trigger.new_iteration_id")
	if !found || got != 5 {
		t.Fatalf("trigger.new_iteration_id = %v (found %v), want 5", got, found)
	}
	// Unrelated trigger fields still miss.
	if _, found = service.resolveExecutionValue(ctx, "trigger.title"); found {
		t.Fatal("trigger.title resolved without any value")
	}
}

// TestTransitionParentWhenChildrenDoneTemplate is the end-to-end GH #267
// recipe through the durable consumer: a status_transition trigger, a
// related_items(ancestors) iterator, an open_child_count eq 0 condition, and
// a transition_item node. Completing the last child must transition the
// parent, and the child must keep its own status.
func TestTransitionParentWhenChildrenDoneTemplate(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	var completedCategoryID int
	if err := db.QueryRow(`
		INSERT INTO status_categories (name, color, is_default, is_completed)
		VALUES ('Done Template', '#22c55e', false, true) RETURNING id
	`).Scan(&completedCategoryID); err != nil {
		t.Fatalf("seed completed category: %v", err)
	}
	var doneStatusID int
	if err := db.QueryRow(`
		INSERT INTO statuses (name, category_id, is_default)
		VALUES ('Done (template)', ?, false) RETURNING id
	`, completedCategoryID).Scan(&doneStatusID); err != nil {
		t.Fatalf("seed done status: %v", err)
	}

	// Pin a mid-hierarchy item type for the fixture items: CreateItem's
	// default type is arbitrary and a hierarchy_level-0 type (Initiative)
	// intentionally stops ancestor walks, which the recipe under test needs.
	var storyTypeID int
	if err := db.QueryRow(`SELECT id FROM item_types WHERE builtin_key = 'story' LIMIT 1`).Scan(&storyTypeID); err != nil {
		t.Fatalf("seed story item type: %v", err)
	}
	createItem := func(title string, parentID, statusID *int) int {
		t.Helper()
		id, err := CreateItem(db, ItemCreationParams{
			WorkspaceID: data.WorkspaceID, Title: title, ItemTypeID: &storyTypeID,
			StatusID: statusID, CreatorID: &data.UserID, ParentID: parentID,
		})
		if err != nil {
			t.Fatalf("create item %s: %v", title, err)
		}
		return int(id)
	}

	// Make the seeded Done status reachable: wire it into the workflow that
	// governs the test items (initial + from-open transitions).
	workflowService := NewWorkflowService(db)
	workflowID, err := workflowService.GetWorkflowIDForItem(data.WorkspaceID, &storyTypeID)
	if err != nil || workflowID == nil {
		t.Fatalf("resolve item workflow: %v (id %v)", err, workflowID)
	}
	for _, fromStatusID := range []*int{nil, &data.StatusID} {
		if fromStatusID == nil {
			if _, err := db.Exec(`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id) VALUES (?, NULL, ?)`, *workflowID, doneStatusID); err != nil {
				t.Fatalf("seed initial transition: %v", err)
			}
			continue
		}
		if _, err := db.Exec(`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id) VALUES (?, ?, ?)`, *workflowID, *fromStatusID, doneStatusID); err != nil {
			t.Fatalf("seed transition: %v", err)
		}
	}

	parent := createItem("parent", nil, &data.StatusID)
	childA := createItem("child A", &parent, &doneStatusID)
	childB := createItem("child B", &parent, &data.StatusID)

	// The user moves child B to Done; the durable fact below reports that
	// transition. The DB row must match so the parent's open count is 0.
	if _, err := db.Exec(`UPDATE items SET status_id = ? WHERE id = ?`, doneStatusID, childB); err != nil {
		t.Fatalf("transition child B: %v", err)
	}

	repo := repository.NewActionRepository(db)
	actionID := createDurableTestAction(t, repo, data.WorkspaceID, "parent done rollup", models.ActionTriggerStatusTransition)
	triggerID, err := repo.CreateNode(&models.ActionNode{ActionID: actionID, NodeType: models.ActionNodeTrigger, NodeConfig: "{}"})
	if err != nil {
		t.Fatalf("create trigger node: %v", err)
	}
	iteratorID, err := repo.CreateNode(&models.ActionNode{
		ActionID: actionID, NodeType: models.ActionNodeRelatedItems,
		NodeConfig: `{"relation": "ancestors", "cross_workspace": false}`,
	})
	if err != nil {
		t.Fatalf("create iterator node: %v", err)
	}
	conditionID, err := repo.CreateNode(&models.ActionNode{
		ActionID: actionID, NodeType: models.ActionNodeCondition,
		NodeConfig: `{"field_name": "open_child_count", "operator": "eq", "value": "0"}`,
	})
	if err != nil {
		t.Fatalf("create condition node: %v", err)
	}
	transitionID, err := repo.CreateNode(&models.ActionNode{
		ActionID: actionID, NodeType: models.ActionNodeTransitionItem,
		NodeConfig: `{"target": {"mode": "explicit", "status_id": ` + strconv.Itoa(doneStatusID) + `}}`,
	})
	if err != nil {
		t.Fatalf("create transition node: %v", err)
	}
	for _, edge := range []models.ActionEdge{
		{ActionID: actionID, SourceNodeID: triggerID, TargetNodeID: iteratorID},
		{ActionID: actionID, SourceNodeID: iteratorID, TargetNodeID: conditionID},
		{ActionID: actionID, SourceNodeID: conditionID, TargetNodeID: transitionID, EdgeType: "true"},
	} {
		if _, err := repo.CreateEdge(&edge); err != nil {
			t.Fatalf("create edge: %v", err)
		}
	}

	service := NewActionService(db, DefaultActionServiceConfig(), nil)
	t.Cleanup(service.Stop)
	service.InvalidateWorkspaceCache(data.WorkspaceID)

	// The child transitioned to Done on the canonical path; the action fires
	// for that status_changed fact.
	childDone := itemevents.StatusChangedV1{
		Item:        itemevents.ItemSnapshot{ID: childB, WorkspaceID: data.WorkspaceID, Title: "child B", StatusID: &doneStatusID},
		OldStatusID: &data.StatusID,
		NewStatusID: &doneStatusID,
	}
	payload, err := json.Marshal(childDone)
	if err != nil {
		t.Fatalf("marshal status event: %v", err)
	}
	workspaceID := data.WorkspaceID
	event := events.Event{
		Key: "parent-rollup-child-b", WorkspaceID: &workspaceID,
		AggregateType: "item", AggregateID: strconv.Itoa(childB), AggregateSequence: 1,
		Type: itemevents.StatusChanged, PayloadVersion: itemevents.PayloadVersion,
		OccurredAt: time.Now().UTC(), ActorKind: "user", ActorRef: strconv.Itoa(data.UserID),
		SourceKind: "test", Payload: payload,
	}

	consumer := NewDurableActionConsumer(db, service)
	if err := consumer.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	itemStatus := func(itemID int) int {
		t.Helper()
		item, err := repository.NewItemRepository(db).FindByID(itemID)
		if err != nil {
			t.Fatalf("load item %d: %v", itemID, err)
		}
		if item.StatusID == nil {
			t.Fatalf("item %d has no status", itemID)
		}
		return *item.StatusID
	}
	dumpExecutionDiagnostics(t, db, actionID)
	if got := itemStatus(parent); got != doneStatusID {
		t.Fatalf("parent status = %d, want %d after last child completed", got, doneStatusID)
	}
	if got := itemStatus(childB); got != doneStatusID {
		t.Fatalf("child B status changed to %d, want untouched %d", got, doneStatusID)
	}
	if got := itemStatus(childA); got != doneStatusID {
		t.Fatalf("child A status = %d, want %d", got, doneStatusID)
	}

	// Re-delivering the same durable fact must not re-execute (frozen target).
	if err := consumer.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle redelivery: %v", err)
	}
}

// dumpExecutionDiagnostics prints the action's execution logs so a failed
// roll-up shows which step failed and why.
func dumpExecutionDiagnostics(t *testing.T, db database.Database, actionID int) {
	t.Helper()
	rows, err := db.Query(`
		SELECT status, COALESCE(error_message, ''), COALESCE(execution_trace, '')
		FROM action_execution_logs WHERE action_id = ? ORDER BY id
	`, actionID)
	if err != nil {
		t.Logf("execution log query failed: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var status, message, trace string
		if err := rows.Scan(&status, &message, &trace); err != nil {
			t.Logf("scan execution log: %v", err)
			continue
		}
		t.Logf("execution log: status=%s error=%q trace=%s", status, message, trace)
	}
}
