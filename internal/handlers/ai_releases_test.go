package handlers

import (
	"errors"
	"strings"
	"testing"

	"windshift/internal/services"
)

func TestBuildReleaseNotesUserPromptIncludesGroundedCompletedItems(t *testing.T) {
	prompt, err := buildReleaseNotesUserPrompt(
		&services.MilestoneResult{Name: "0.8.2"},
		&services.MilestoneProgressReport{
			TotalItems:      2,
			CompletedItems:  1,
			PercentComplete: 50,
			StatusBreakdown: []services.StatusBreakdown{
				{CategoryName: "Done", ItemCount: 1, IsCompleted: true},
				{CategoryName: "To Do", ItemCount: 1},
			},
			ItemsByCategory: map[string][]services.ProgressItem{
				"Done":  {{WorkspaceKey: "WI", ItemNumber: 42, Title: "Add grounded release notes"}},
				"To Do": {{WorkspaceKey: "WI", ItemNumber: 43, Title: "Unreleased work"}},
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("buildReleaseNotesUserPrompt: %v", err)
	}
	if !strings.Contains(prompt, "WI-42: Add grounded release notes") {
		t.Fatalf("prompt does not contain completed item: %s", prompt)
	}
	if strings.Contains(prompt, "WI-43") {
		t.Fatalf("prompt contains incomplete item: %s", prompt)
	}
	if !strings.Contains(prompt, "using only the facts below") {
		t.Fatalf("prompt does not contain grounding constraint: %s", prompt)
	}
}

func TestBuildReleaseNotesUserPromptRejectsEmptyGrounding(t *testing.T) {
	_, err := buildReleaseNotesUserPrompt(
		&services.MilestoneResult{Name: "0.8.2"},
		&services.MilestoneProgressReport{
			TotalItems:      1,
			StatusBreakdown: []services.StatusBreakdown{{CategoryName: "Done", ItemCount: 1}},
			ItemsByCategory: map[string][]services.ProgressItem{
				"Done": {{WorkspaceKey: "WI", ItemNumber: 42, Title: "Completed feature"}},
			},
		},
		nil,
	)
	if !errors.Is(err, errNoCompletedReleaseItems) {
		t.Fatalf("error = %v, want errNoCompletedReleaseItems", err)
	}
}
