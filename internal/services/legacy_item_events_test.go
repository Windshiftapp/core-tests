//go:build test

package services

import (
	"testing"

	"windshift/internal/models"
)

type recordingNotifier struct {
	events []*NotificationEvent
}

func (n *recordingNotifier) EmitEvent(event *NotificationEvent) {
	n.events = append(n.events, event)
}

type recordingActionEmitter struct {
	events []*models.ActionEvent
}

func (a *recordingActionEmitter) EmitActionEvent(event *models.ActionEvent) {
	a.events = append(a.events, event)
}

type recordingWebhook struct {
	events []string
}

func (w *recordingWebhook) DispatchEvent(eventType string, item *models.Item) {
	w.events = append(w.events, eventType)
}

func legacySampleItem(id int, name string) *models.Item {
	return &models.Item{
		ID:                  id,
		WorkspaceID:         1,
		WorkspaceKey:        "TEST",
		WorkspaceItemNumber: id,
		Title:               name,
		StatusID:            intPtr(2),
	}
}

func TestLegacyItemUpdatedEmitter_StatusAndAssigneeChange(t *testing.T) {
	notifier := &recordingNotifier{}
	action := &recordingActionEmitter{}
	webhook := &recordingWebhook{}
	emitter := NewLegacyItemUpdatedEmitter(nil, notifier.EmitEvent, action, webhook)

	original := legacySampleItem(1, "Before")
	updated := legacySampleItem(1, "After")
	emitter.EmitItemUpdated(original, updated, true, true, 7, nil, "alice")

	if len(notifier.events) != 2 {
		t.Fatalf("notification events = %d, want 2 (status + assignee)", len(notifier.events))
	}
	if got := notifier.events[0].EventType; got != models.EventStatusChanged {
		t.Fatalf("first notification = %s, want EventStatusChanged", got)
	}
	if got := notifier.events[1].EventType; got != models.EventItemAssigned {
		t.Fatalf("second notification = %s, want EventItemAssigned", got)
	}
	if got := notifier.events[0].TemplateData["user.name"]; got != "alice" {
		t.Fatalf("status user.name = %v, want alice", got)
	}

	if len(action.events) != 1 || action.events[0].EventType != models.ActionTriggerStatusTransition {
		t.Fatalf("action events = %+v, want a single status transition", action.events)
	}

	wantWebhooks := []string{"status.changed", "item.assigned", "item.updated"}
	if len(webhook.events) != len(wantWebhooks) {
		t.Fatalf("webhook events = %v, want %v", webhook.events, wantWebhooks)
	}
	for i := range wantWebhooks {
		if webhook.events[i] != wantWebhooks[i] {
			t.Fatalf("webhook events = %v, want %v", webhook.events, wantWebhooks)
		}
	}
}

func TestLegacyItemUpdatedEmitter_PlainUpdate(t *testing.T) {
	notifier := &recordingNotifier{}
	action := &recordingActionEmitter{}
	webhook := &recordingWebhook{}
	emitter := NewLegacyItemUpdatedEmitter(nil, notifier.EmitEvent, action, webhook)

	original := legacySampleItem(2, "Before")
	updated := legacySampleItem(2, "After")
	emitter.EmitItemUpdated(original, updated, false, false, 7, nil, "bob")

	if len(notifier.events) != 1 {
		t.Fatalf("notification events = %d, want 1", len(notifier.events))
	}
	if got := notifier.events[0].EventType; got != models.EventItemUpdated {
		t.Fatalf("notification = %s, want EventItemUpdated", got)
	}
	if len(action.events) != 1 || action.events[0].EventType != models.ActionTriggerItemUpdated {
		t.Fatalf("action events = %+v, want a single item update", action.events)
	}
	if len(webhook.events) != 1 || webhook.events[0] != "item.updated" {
		t.Fatalf("webhook events = %v, want [item.updated]", webhook.events)
	}
}

func TestLegacyItemUpdatedEmitter_AttachesLaterComponents(t *testing.T) {
	notifier := &recordingNotifier{}
	webhook := &recordingWebhook{}
	emitter := NewLegacyItemUpdatedEmitter(nil, notifier.EmitEvent, nil, nil)

	emitter.SetAction(nil)
	emitter.SetWebhook(webhook)
	original := legacySampleItem(3, "Before")
	updated := legacySampleItem(3, "After")
	emitter.EmitItemUpdated(original, updated, false, true, 7, nil, "carol")

	if len(notifier.events) != 1 || notifier.events[0].EventType != models.EventItemAssigned {
		t.Fatalf("notification events = %+v, want a single assignee notification", notifier.events)
	}
	if len(webhook.events) != 2 {
		t.Fatalf("webhook events = %v, want [item.assigned item.updated]", webhook.events)
	}
}
