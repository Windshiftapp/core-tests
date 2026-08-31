//go:build test

package dto

import (
	"testing"

	"windshift/internal/services"
)

func TestMapServiceTransitionsToResponsePreservesFromAllDiscriminator(t *testing.T) {
	transitions := MapServiceTransitionsToResponse([]services.WorkflowTransitionResult{
		{
			ID:              42,
			FromAllStatuses: true,
			ToStatusID:      7,
			ToStatusName:    "Done",
		},
	})

	if len(transitions) != 1 {
		t.Fatalf("transition count: got %d, want 1", len(transitions))
	}
	got := transitions[0]
	if !got.FromAllStatuses {
		t.Fatal("from_all_statuses: got false, want true")
	}
	if got.FromStatusID != nil {
		t.Fatalf("from_status_id: got %v, want null", *got.FromStatusID)
	}
	if got.ToStatusID != 7 {
		t.Fatalf("to_status_id: got %d, want 7", got.ToStatusID)
	}
}
