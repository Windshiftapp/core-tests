package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"windshift/internal/models"
)

// TestAgentRunner_ReviewFlagged_UnrecoveredUnknownTool drives the fakeagent
// through a run where the model invoked a nonexistent tool and never
// recovered; the runner must synthesize exactly one "review_flagged" event.
func TestAgentRunner_ReviewFlagged_UnrecoveredUnknownTool(t *testing.T) {
	bin := buildFakeAgent(t)
	r := &AgentRunner{
		Command:       bin,
		Env:           map[string]string{"FAKEAGENT_MODE": "toolfail"},
		InitialPrompt: "do the thing",
		ShutdownGrace: 2 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var events []string
	result := r.Run(ctx, RunInput{RunID: 1}, collectEvents(&events))
	if result.Status != models.AgentRunStatusSucceeded {
		t.Fatalf("status: want succeeded, got %q (err=%q)", result.Status, result.Error)
	}

	var flagged []string
	for _, ev := range events {
		if strings.HasPrefix(ev, "review_flagged|") {
			flagged = append(flagged, ev)
		}
	}
	if len(flagged) != 1 {
		t.Fatalf("want exactly one review_flagged event, got %d: %v", len(flagged), flagged)
	}
	if !strings.Contains(flagged[0], "unknown_tool") {
		t.Errorf("review_flagged payload should name the class: %q", flagged[0])
	}
}

// TestAgentRunner_ReviewFlagged_RecoveredNoFlag confirms the recovery guard:
// an invalid-args failure followed by a same-tool success must NOT flag.
func TestAgentRunner_ReviewFlagged_RecoveredNoFlag(t *testing.T) {
	bin := buildFakeAgent(t)
	r := &AgentRunner{
		Command:       bin,
		Env:           map[string]string{"FAKEAGENT_MODE": "toolrecover"},
		InitialPrompt: "do the thing",
		ShutdownGrace: 2 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var events []string
	result := r.Run(ctx, RunInput{RunID: 2}, collectEvents(&events))
	if result.Status != models.AgentRunStatusSucceeded {
		t.Fatalf("status: want succeeded, got %q (err=%q)", result.Status, result.Error)
	}
	for _, ev := range events {
		if strings.HasPrefix(ev, "review_flagged|") {
			t.Fatalf("recovered run must not flag; got %q", ev)
		}
	}
}
