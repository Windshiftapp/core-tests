//go:build test

package services

import (
	"testing"

	"windshift/internal/itemevents"
	"windshift/internal/repository"
)

func TestHistorySourceForAgentStampsOnlyAgentActors(t *testing.T) {
	tests := []struct {
		name     string
		metadata itemevents.Metadata
		want     string
	}{
		{name: "ai chat", metadata: itemevents.Agent("user:7", "ai_chat"), want: "ai_chat"},
		{name: "mcp", metadata: itemevents.Agent("user:7", "mcp"), want: "mcp"},
		{name: "standard agent", metadata: itemevents.Agent("user:7", "standard_agent"), want: "standard_agent"},
		{name: "direct user write", metadata: itemevents.User(7, "application"), want: ""},
		{name: "automation", metadata: itemevents.System("automation"), want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := historySourceForAgent(tt.metadata); got != tt.want {
				t.Fatalf("historySourceForAgent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHistoryRunForAgentLinksOnlyAgentWritesWithARun(t *testing.T) {
	withRun := itemevents.Agent("user:7", "ai_chat")
	withRun.AgentRunID = 55
	withoutRun := itemevents.Agent("user:7", "mcp")
	direct := itemevents.User(7, "application")

	tests := []struct {
		name     string
		metadata itemevents.Metadata
		want     *int
	}{
		{name: "agent run", metadata: withRun, want: intPtr(55)},
		{name: "agent with no run", metadata: withoutRun, want: nil},
		{name: "direct write", metadata: direct, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := historyRunForAgent(tt.metadata)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("historyRunForAgent() = %d, want nil", *got)
				}
				return
			}
			if got == nil || *got != *tt.want {
				t.Fatalf("historyRunForAgent() = %v, want %d", got, *tt.want)
			}
		})
	}
}

func TestStampHistorySourceStampsEveryRowInTheBatch(t *testing.T) {
	metadata := itemevents.Agent("user:7", "ai_chat")
	metadata.AgentRunID = 55
	history := []repository.HistoryEntry{{ItemID: 1, FieldName: "title"}, {ItemID: 1, FieldName: "status_id"}}

	stampHistorySource(history, metadata)

	for i, entry := range history {
		if entry.Source != "ai_chat" {
			t.Fatalf("entry %d source = %q, want ai_chat", i, entry.Source)
		}
		if entry.AgentRunID == nil || *entry.AgentRunID != 55 {
			t.Fatalf("entry %d agent_run_id = %v, want 55", i, entry.AgentRunID)
		}
	}
}

func TestStampHistorySourceLeavesDirectWritesUntouched(t *testing.T) {
	history := []repository.HistoryEntry{{ItemID: 1, FieldName: "title"}}

	stampHistorySource(history, itemevents.User(7, "application"))

	if history[0].Source != "" || history[0].AgentRunID != nil {
		t.Fatalf("direct write stamped: source=%q agent_run_id=%v", history[0].Source, history[0].AgentRunID)
	}
}
