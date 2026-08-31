//go:build test

package services

import (
	"fmt"
	"testing"

	"windshift/internal/models"
)

type milestoneActionEventCollector struct {
	events []*models.ActionEvent
}

func (c *milestoneActionEventCollector) EmitActionEvent(event *models.ActionEvent) {
	c.events = append(c.events, event)
}

type milestoneWebhookCollector struct {
	eventTypes []string
	items      []*models.Item
}

func (c *milestoneWebhookCollector) DispatchEvent(eventType string, item *models.Item) {
	c.eventTypes = append(c.eventTypes, eventType)
	c.items = append(c.items, item)
}

func TestAutomatedMilestoneAttachmentEmitsSharedEffectsOnce(t *testing.T) {
	db := createNotificationEmissionTestDB(t)
	env := setupNotificationEmissionTestEnv(t, db)
	milestoneID := planningScopeInsertID(t, db, `
		INSERT INTO milestones (name, description, status, is_global, workspace_id)
		VALUES ('Automated release', '', 'planning', false, ?)
	`, env.workspaceID)

	liveUpdates := installItemChangeSpy(t)
	actions := &milestoneActionEventCollector{}
	webhooks := &milestoneWebhookCollector{}
	coordinator := NewEventCoordinator(db)
	coordinator.SetActionService(actions)
	coordinator.SetWebhookDispatcher(webhooks)
	updater := NewItemUpdateApplicationService(db, nil)
	updater.SetEmitter(coordinator)
	actionContext := ActionContext{
		TriggeredByAction: true,
		ExecutionChainID:  "chain-687",
		CascadeDepth:      3,
		SourceApplication: "workspace",
	}

	result, changed, err := updater.AddMilestoneWithContext(
		env.userID,
		env.userName,
		env.itemID,
		milestoneID,
		actionContext,
	)
	if err != nil {
		t.Fatalf("AddMilestoneWithContext: %v", err)
	}
	if !changed {
		t.Fatal("first attachment changed = false, want true")
	}
	if len(result.Item.Milestones) != 1 || result.Item.Milestones[0].ID != milestoneID {
		t.Fatalf("updated milestones = %+v, want milestone %d", result.Item.Milestones, milestoneID)
	}

	var junctionCount int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM item_milestones
		WHERE item_id = ? AND milestone_id = ?
	`, env.itemID, milestoneID).Scan(&junctionCount); err != nil {
		t.Fatalf("count milestone junctions: %v", err)
	}
	if junctionCount != 1 {
		t.Fatalf("milestone junction count = %d, want 1", junctionCount)
	}

	var historyCount, historyActor int
	var oldValue, newValue string
	if err := db.QueryRow(`
		SELECT COUNT(*), MIN(user_id), MIN(old_value), MIN(new_value)
		FROM item_history
		WHERE item_id = ? AND field_name = 'milestones'
	`, env.itemID).Scan(&historyCount, &historyActor, &oldValue, &newValue); err != nil {
		t.Fatalf("read milestone history: %v", err)
	}
	if historyCount != 1 || historyActor != env.userID || oldValue != "" || newValue != fmt.Sprint(milestoneID) {
		t.Fatalf(
			"milestone history = count %d actor %d old %q new %q",
			historyCount,
			historyActor,
			oldValue,
			newValue,
		)
	}

	live := liveUpdates.snapshot()
	if len(live) != 1 || live[0].itemID != env.itemID || live[0].kind != ItemChangeUpdated {
		t.Fatalf("live updates = %+v, want one item.updated for %d", live, env.itemID)
	}
	if len(actions.events) != 1 {
		t.Fatalf("action events = %d, want 1", len(actions.events))
	}
	action := actions.events[0]
	if action.EventType != models.ActionTriggerItemUpdated ||
		action.WorkspaceID != env.workspaceID ||
		action.ItemID != env.itemID ||
		action.ActorUserID != env.userID ||
		action.OldValues["milestones"] != "" ||
		action.NewValues["milestones"] != fmt.Sprint(milestoneID) ||
		!action.TriggeredByAction ||
		action.ExecutionChainID != actionContext.ExecutionChainID ||
		action.CascadeDepth != actionContext.CascadeDepth ||
		action.SourceApplication != actionContext.SourceApplication {
		t.Fatalf("action event = %+v", action)
	}
	if len(webhooks.eventTypes) != 1 ||
		webhooks.eventTypes[0] != "item.updated" ||
		len(webhooks.items) != 1 ||
		webhooks.items[0].ID != env.itemID {
		t.Fatalf("webhooks = %v items=%+v, want one item.updated", webhooks.eventTypes, webhooks.items)
	}

	_, changed, err = updater.AddMilestoneWithContext(
		env.userID,
		env.userName,
		env.itemID,
		milestoneID,
		actionContext,
	)
	if err != nil {
		t.Fatalf("duplicate AddMilestoneWithContext: %v", err)
	}
	if changed {
		t.Fatal("duplicate attachment changed = true, want false")
	}
	if len(liveUpdates.snapshot()) != 1 || len(actions.events) != 1 || len(webhooks.eventTypes) != 1 {
		t.Fatalf(
			"duplicate emitted effects: live=%v actions=%d webhooks=%v",
			liveUpdates.snapshot(),
			len(actions.events),
			webhooks.eventTypes,
		)
	}
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM item_history
		WHERE item_id = ? AND field_name = 'milestones'
	`, env.itemID).Scan(&historyCount); err != nil {
		t.Fatalf("recount milestone history: %v", err)
	}
	if historyCount != 1 {
		t.Fatalf("milestone history after duplicate = %d, want 1", historyCount)
	}
}

func TestAutomatedMilestoneAttachmentRollsBackAndRemainsRetryable(t *testing.T) {
	db := createNotificationEmissionTestDB(t)
	env := setupNotificationEmissionTestEnv(t, db)
	milestoneID := planningScopeInsertID(t, db, `
		INSERT INTO milestones (name, description, status, is_global, workspace_id)
		VALUES ('Retryable release', '', 'planning', false, ?)
	`, env.workspaceID)
	createTrigger := `
		CREATE TRIGGER reject_milestone_history
		BEFORE INSERT ON item_history
		WHEN NEW.field_name = 'milestones'
		BEGIN
			SELECT RAISE(ABORT, 'reject milestone history');
		END
	`
	dropTrigger := `DROP TRIGGER reject_milestone_history`
	if db.GetDriverName() == "postgres" {
		if _, err := db.Exec(`
			CREATE FUNCTION reject_milestone_history_write() RETURNS trigger AS $$
			BEGIN
				RAISE EXCEPTION 'reject milestone history';
			END;
			$$ LANGUAGE plpgsql
		`); err != nil {
			t.Fatalf("create history failure function: %v", err)
		}
		createTrigger = `
			CREATE TRIGGER reject_milestone_history
			BEFORE INSERT ON item_history
			FOR EACH ROW
			WHEN (NEW.field_name = 'milestones')
			EXECUTE FUNCTION reject_milestone_history_write()
		`
		dropTrigger = `
			DROP TRIGGER reject_milestone_history ON item_history;
			DROP FUNCTION reject_milestone_history_write()
		`
	}
	if _, err := db.Exec(createTrigger); err != nil {
		t.Fatalf("create history failure trigger: %v", err)
	}

	liveUpdates := installItemChangeSpy(t)
	actions := &milestoneActionEventCollector{}
	webhooks := &milestoneWebhookCollector{}
	coordinator := NewEventCoordinator(db)
	coordinator.SetActionService(actions)
	coordinator.SetWebhookDispatcher(webhooks)
	updater := NewItemUpdateApplicationService(db, nil)
	updater.SetEmitter(coordinator)

	_, changed, err := updater.AddMilestoneWithContext(
		env.userID,
		env.userName,
		env.itemID,
		milestoneID,
		ActionContext{TriggeredByAction: true},
	)
	if err == nil {
		t.Fatal("attachment succeeded despite rejected history write")
	}
	if changed {
		t.Fatal("failed attachment changed = true")
	}
	var junctionCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM item_milestones
		WHERE item_id = ? AND milestone_id = ?
	`, env.itemID, milestoneID).Scan(&junctionCount); err != nil {
		t.Fatalf("count rolled-back junctions: %v", err)
	}
	if junctionCount != 0 {
		t.Fatalf("junction count after rollback = %d, want 0", junctionCount)
	}
	if len(liveUpdates.snapshot()) != 0 || len(actions.events) != 0 || len(webhooks.eventTypes) != 0 {
		t.Fatal("failed mutation emitted effects")
	}

	if _, err := db.Exec(dropTrigger); err != nil {
		t.Fatalf("drop history failure trigger: %v", err)
	}
	_, changed, err = updater.AddMilestoneWithContext(
		env.userID,
		env.userName,
		env.itemID,
		milestoneID,
		ActionContext{TriggeredByAction: true},
	)
	if err != nil {
		t.Fatalf("retry attachment: %v", err)
	}
	if !changed {
		t.Fatal("retry attachment changed = false, want true")
	}
	if len(liveUpdates.snapshot()) != 1 || len(actions.events) != 1 || len(webhooks.eventTypes) != 1 {
		t.Fatalf(
			"retry effects: live=%v actions=%d webhooks=%v",
			liveUpdates.snapshot(),
			len(actions.events),
			webhooks.eventTypes,
		)
	}
}
