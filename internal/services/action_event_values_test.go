//go:build test

package services

import (
	"encoding/json"
	"fmt"
	"testing"

	"windshift/internal/events"
	"windshift/internal/itemevents"
)

// Regression (bughunt, actions): canonical item.created and
// item.status_changed events carried raw *int pointers in NewValues.
// Template substitution rendered pointer addresses ("0xc000…") for
// {{new_status_id}} and friends, and CoerceInt rejected pointers, so a
// matching_terminal transition_item resolved the wrong terminal status.
func TestCanonicalItemEventsExposePlainNumericValues(t *testing.T) {
	statusID, priorityID, assigneeID, iterationID := 3, 2, 7, 5
	item := itemevents.ItemSnapshot{
		ID: 17, WorkspaceID: 1, WorkspaceItemNumber: 1,
		Title: "created item", StatusID: &statusID, PriorityID: &priorityID,
		AssigneeID: &assigneeID, IterationID: &iterationID,
	}

	t.Run("created event exposes plain numeric values", func(t *testing.T) {
		payloadBytes, err := json.Marshal(itemevents.CreatedV1{Item: item})
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		actionEvent, _, err := actionEventFromDomainEvent(events.Event{
			Type: itemevents.Created, PayloadVersion: 1, Payload: payloadBytes,
		})
		if err != nil {
			t.Fatalf("actionEventFromDomainEvent: %v", err)
		}
		for _, key := range []string{"status_id", "priority_id", "assignee_id", "iteration_id"} {
			if _, ok := actionEvent.NewValues[key].(*int); ok {
				t.Errorf("NewValues[%q] is a *int pointer, want a plain value", key)
			}
			if rendered := fmt.Sprintf("%v", actionEvent.NewValues[key]); len(rendered) > 2 && rendered[:2] == "0x" {
				t.Errorf("NewValues[%q] renders as pointer address %q", key, rendered)
			}
		}
		if actionEvent.NewValues["status_id"] != statusID {
			t.Errorf("NewValues[status_id] = %v, want %d", actionEvent.NewValues["status_id"], statusID)
		}
	})

	t.Run("status changed event exposes plain numeric values", func(t *testing.T) {
		payloadBytes, err := json.Marshal(itemevents.StatusChangedV1{
			Item: item, OldStatusID: oldInt(1), NewStatusID: &statusID,
		})
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		actionEvent, _, err := actionEventFromDomainEvent(events.Event{
			Type: itemevents.StatusChanged, PayloadVersion: 1, Payload: payloadBytes,
		})
		if err != nil {
			t.Fatalf("actionEventFromDomainEvent: %v", err)
		}
		if _, ok := actionEvent.NewValues["status_id"].(*int); ok {
			t.Error("NewValues[status_id] is a *int pointer, want a plain value")
		}
		if _, ok := actionEvent.OldValues["status_id"].(*int); ok {
			t.Error("OldValues[status_id] is a *int pointer, want a plain value")
		}
		if actionEvent.NewValues["status_id"] != statusID || actionEvent.OldValues["status_id"] != 1 {
			t.Errorf("status ids = old %v / new %v, want 1 / %d",
				actionEvent.OldValues["status_id"], actionEvent.NewValues["status_id"], statusID)
		}
	})
}

func oldInt(v int) *int { return &v }
