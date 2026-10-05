//go:build test

package aitools

import "testing"

func TestEnvEventMetadataDescribesAgentProvenance(t *testing.T) {
	tests := []struct {
		name       string
		env        Env
		wantSource string
		wantRunID  int
	}{
		{
			name:       "chat names its surface and run",
			env:        Env{UserID: 7, Source: SourceAIChat, RunID: 55},
			wantSource: "ai_chat",
			wantRunID:  55,
		},
		{
			name:       "unset source falls back to agent",
			env:        Env{UserID: 7},
			wantSource: "agent",
			wantRunID:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := tt.env.eventMetadata()
			if metadata.ActorKind != "agent" {
				t.Fatalf("actor kind = %q, want agent", metadata.ActorKind)
			}
			if metadata.ActorRef != "user:7" {
				t.Fatalf("actor ref = %q, want user:7", metadata.ActorRef)
			}
			if metadata.SourceKind != tt.wantSource {
				t.Fatalf("source kind = %q, want %q", metadata.SourceKind, tt.wantSource)
			}
			if metadata.AgentRunID != tt.wantRunID {
				t.Fatalf("agent run id = %d, want %d", metadata.AgentRunID, tt.wantRunID)
			}
		})
	}
}

func TestEnvHistoryRunIDOnlyReportsRecordedRuns(t *testing.T) {
	withRun := Env{UserID: 7, RunID: 55}
	if got := withRun.historyRunID(); got == nil || *got != 55 {
		t.Fatalf("historyRunID() = %v, want 55", got)
	}

	withoutRun := Env{UserID: 7}
	if got := withoutRun.historyRunID(); got != nil {
		t.Fatalf("historyRunID() = %d, want nil", *got)
	}
}
