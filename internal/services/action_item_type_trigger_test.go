//go:build test

package services

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"windshift/internal/events"
	"windshift/internal/itemevents"
	"windshift/internal/models"
)

func actionEventTypeIDPtr(v int) *int { return &v }

// Regression for GH #266: item.updated events carry only changed fields in
// NewValues, so an item-type-filtered trigger must match on the event's
// ItemTypeID instead of expecting item_type_id among the changed fields.
func TestItemTypeTriggerFilterUsesEventItemTypeID(t *testing.T) {
	service := &ActionService{}
	action := &models.Action{
		TriggerType: models.ActionTriggerItemUpdated,
		TriggerConfig: `{
			"item_type_id": 5,
			"field_name": "iteration_id"
		}`,
	}

	tests := []struct {
		name  string
		event *models.ActionEvent
		want  bool
	}{
		{
			name: "iteration change with matching event ItemTypeID fires",
			event: &models.ActionEvent{
				EventType:  models.ActionTriggerItemUpdated,
				ItemID:     17,
				ItemTypeID: actionEventTypeIDPtr(5),
				NewValues:  map[string]any{"iteration_id": 3},
			},
			want: true,
		},
		{
			name: "iteration change with different event ItemTypeID does not fire",
			event: &models.ActionEvent{
				EventType:  models.ActionTriggerItemUpdated,
				ItemID:     17,
				ItemTypeID: actionEventTypeIDPtr(6),
				NewValues:  map[string]any{"iteration_id": 3},
			},
			want: false,
		},
		{
			name: "legacy event with type only in NewValues still fires",
			event: &models.ActionEvent{
				EventType: models.ActionTriggerItemUpdated,
				ItemID:    17,
				NewValues: map[string]any{"item_type_id": 5, "iteration_id": 3},
			},
			want: true,
		},
		{
			name: "legacy event with no type information anywhere does not fire",
			event: &models.ActionEvent{
				EventType: models.ActionTriggerItemUpdated,
				ItemID:    17,
				NewValues: map[string]any{"iteration_id": 3},
			},
			want: false,
		},
		{
			name: "other field changed on matching type does not fire",
			event: &models.ActionEvent{
				EventType:  models.ActionTriggerItemUpdated,
				ItemID:     17,
				ItemTypeID: actionEventTypeIDPtr(5),
				NewValues:  map[string]any{"title": "renamed"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := service.matchesTrigger(action, tt.event); got != tt.want {
				t.Fatalf("matchesTrigger = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestItemTypeTriggerFilterWithoutFieldName(t *testing.T) {
	service := &ActionService{}
	action := &models.Action{
		TriggerType:   models.ActionTriggerItemUpdated,
		TriggerConfig: `{"item_type_id": 5}`,
	}

	if !service.matchesTrigger(action, &models.ActionEvent{
		EventType:  models.ActionTriggerItemUpdated,
		ItemID:     17,
		ItemTypeID: actionEventTypeIDPtr(5),
		NewValues:  map[string]any{"iteration_id": 3},
	}) {
		t.Fatal("matching type with any-field trigger was rejected")
	}
	if service.matchesTrigger(action, &models.ActionEvent{
		EventType:  models.ActionTriggerItemUpdated,
		ItemID:     17,
		ItemTypeID: actionEventTypeIDPtr(6),
		NewValues:  map[string]any{"iteration_id": 3},
	}) {
		t.Fatal("different item type matched any-field trigger")
	}
}

// The durable consumer rebuilds ActionEvents from item event payloads; the
// rebuilt event must carry the snapshot's item type so filtered triggers fire.
func TestDurableItemUpdatedEventCarriesItemTypeID(t *testing.T) {
	typeID := 5
	payload, err := json.Marshal(itemevents.UpdatedV1{
		Item: itemevents.ItemSnapshot{
			ID: 17, WorkspaceID: 2, Title: "moved item", ItemTypeID: &typeID,
		},
		Changes: []itemevents.FieldChange{
			{Field: "iteration_id", OldValue: 1, NewValue: 3},
		},
	})
	if err != nil {
		t.Fatalf("marshal item event: %v", err)
	}
	workspaceID := 2
	event := events.Event{
		ID: 91, Key: "item-updated-type-91", WorkspaceID: &workspaceID,
		AggregateType: "item", AggregateID: "17", AggregateSequence: 1,
		Type: itemevents.Updated, PayloadVersion: itemevents.PayloadVersion,
		OccurredAt: time.Date(2026, time.September, 15, 12, 0, 0, 0, time.UTC),
		ActorKind:  "user", ActorRef: strconv.Itoa(7), SourceKind: "test", Payload: payload,
	}

	actionEvent, _, err := actionEventFromDomainEvent(event)
	if err != nil {
		t.Fatalf("actionEventFromDomainEvent: %v", err)
	}
	if actionEvent.ItemTypeID == nil || *actionEvent.ItemTypeID != typeID {
		t.Fatalf("rebuilt event ItemTypeID = %v, want %d", actionEvent.ItemTypeID, typeID)
	}

	service := &ActionService{}
	action := &models.Action{
		TriggerType:   models.ActionTriggerItemUpdated,
		TriggerConfig: `{"item_type_id": 5, "field_name": "iteration_id"}`,
	}
	if !service.matchesTrigger(action, actionEvent) {
		t.Fatal("type-filtered trigger did not fire for iteration change on matching type")
	}
}

// Status-transition events must also expose the item type so a single filter
// contract holds across trigger kinds.
func TestStatusTransitionEventCarriesItemTypeID(t *testing.T) {
	created := &models.Item{ID: 17, WorkspaceID: 2, Title: "typed", ItemTypeID: actionEventTypeIDPtr(5)}
	event := newStatusTransitionActionEvent(created, created, 7)
	if event.ItemTypeID == nil || *event.ItemTypeID != 5 {
		t.Fatalf("status transition event ItemTypeID = %v, want 5", event.ItemTypeID)
	}
}
