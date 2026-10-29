//go:build test

package sla

import (
	"encoding/json"
	"testing"
	"time"

	"windshift/internal/itemevents"
	"windshift/internal/models"
)

// A priority-only change must reselect the goal whose query matches the new
// priority, without needing any condition to change. Priority-specific
// durations are separate goals, not priority-scoped targets.
func TestPriorityOnlyChangeReselectsGoal(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	clock := &advanceClock{now: now}
	fixture.engine.SetClock(clock)

	var lowPriority int
	if err := fixture.db.QueryRow(`INSERT INTO priorities (name) VALUES ('SLA Low') RETURNING id`).Scan(&lowPriority); err != nil {
		t.Fatalf("insert low priority: %v", err)
	}

	fixture.createMetric(t,
		[]models.SLACondition{{Phase: models.SLAPhaseStart, Position: 0, ConditionType: conditionCreated, Config: json.RawMessage(`{}`)}},
		[]models.SLAGoal{
			{Position: 0, QLQuery: "priority = 'SLA High'", ImportStatus: "native",
				Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 600_000, CalendarID: fixture.calendarID}}},
			{Position: 1, QLQuery: "1 = 1", ImportStatus: "native",
				Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 3_600_000, CalendarID: fixture.calendarID}}},
		},
	)
	fixture.observe(t, fixture.createdFact())

	var duration int64
	if err := fixture.db.QueryRow(`SELECT goal_duration_ms FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&duration); err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if duration != 600_000 {
		t.Fatalf("initial goal duration = %d, want the 10-minute priority goal", duration)
	}

	// Change only the priority: the catch-all goal does not mention priority.
	clock.now = now.Add(time.Minute)
	if _, err := fixture.db.Exec(`UPDATE items SET priority_id = ? WHERE id = ?`, lowPriority, fixture.itemID); err != nil {
		t.Fatalf("update item priority: %v", err)
	}
	fixture.observe(t, itemevents.RecordedFact{
		Type: itemevents.Updated, ItemID: fixture.itemID, WorkspaceID: fixture.workspaceID,
		Snapshot: itemevents.ItemSnapshot{ID: fixture.itemID, WorkspaceID: fixture.workspaceID, StatusID: &fixture.statusOpen, PriorityID: &lowPriority},
		Changes:  []itemevents.FieldChange{{Field: "priority_id", OldValue: fixture.priorityAPI, NewValue: lowPriority}},
		Metadata: itemevents.Metadata{ActorKind: "user"},
	})

	if err := fixture.db.QueryRow(`SELECT goal_duration_ms FROM item_sla_cycles WHERE item_id = ?`, fixture.itemID).Scan(&duration); err != nil {
		t.Fatalf("reload cycle: %v", err)
	}
	if duration != 3_600_000 {
		t.Fatalf("goal duration after priority-only change = %d, want the 1-hour catch-all goal", duration)
	}
}
