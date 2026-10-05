//go:build test

package itemevents

import (
	"reflect"
	"testing"

	"windshift/internal/models"
)

func TestChangesReturnsStableTypedCanonicalFields(t *testing.T) {
	oldStatus, newStatus := 2, 3
	oldParent := 10
	before := &models.Item{
		ID: 7, WorkspaceID: 1, WorkspaceItemNumber: 9,
		Title: "Before", StatusID: &oldStatus, ParentID: &oldParent,
		CustomFieldValues: map[string]any{"20": "old", "10": float64(1)},
	}
	after := *before
	after.Title = "After"
	after.StatusID = &newStatus
	after.ParentID = nil
	after.CustomFieldValues = map[string]any{"10": float64(2), "30": true}

	got := Changes(before, &after)
	want := []FieldChange{
		{Field: "title", OldValue: "Before", NewValue: "After"},
		{Field: "status_id", OldValue: 2, NewValue: 3},
		{Field: "parent_id", OldValue: 10, NewValue: nil},
		{Field: "cf_10", OldValue: float64(1), NewValue: float64(2)},
		{Field: "cf_20", OldValue: "old", NewValue: nil},
		{Field: "cf_30", OldValue: nil, NewValue: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Changes() = %#v, want %#v", got, want)
	}
}

func TestNewEventSurfacesAgentRunAsSourceRef(t *testing.T) {
	tests := []struct {
		name     string
		metadata Metadata
		wantRef  string
	}{
		{
			name: "agent run with no other source",
			metadata: func() Metadata {
				m := Agent("user:7", "ai_chat")
				m.AgentRunID = 55
				return m
			}(),
			wantRef: "agent_run:55",
		},
		{
			name: "automation source ref wins over the run link",
			metadata: func() Metadata {
				m := Agent("user:7", "ai_chat")
				m.AgentRunID = 55
				m.SourceRef = "app:jira"
				return m
			}(),
			wantRef: "app:jira",
		},
		{
			name:     "no run leaves the source ref empty",
			metadata: Agent("user:7", "mcp"),
			wantRef:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := newEvent("item.updated", 3, 9, tt.metadata, map[string]any{"field": "title"})
			if err != nil {
				t.Fatalf("newEvent() error = %v", err)
			}
			if event.SourceRef != tt.wantRef {
				t.Fatalf("source ref = %q, want %q", event.SourceRef, tt.wantRef)
			}
		})
	}
}
