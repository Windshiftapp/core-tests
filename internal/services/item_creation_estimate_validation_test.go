//go:build test

package services

import (
	"errors"
	"testing"

	"windshift/internal/testutils"
	"windshift/internal/validation"
)

// Negative estimates must be rejected at every creation entry point
// (REST, portal, forms, Jira import, agent tools share CreateItem), matching
// the update-path contract enforced by validation.ItemFieldValidator.
func TestCreateItemRejectsNegativeEstimates(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	tests := []struct {
		name        string
		storyPoints *float64
		estimate    *int
		wantField   string
		wantMessage string
	}{
		{
			name:        "negative story points",
			storyPoints: fPtr(-2.5),
			wantField:   "story_points",
			wantMessage: "Story points cannot be negative",
		},
		{
			name:        "negative estimate minutes",
			estimate:    iPtr(-30),
			wantField:   "estimate_minutes",
			wantMessage: "Estimate cannot be negative",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
				WorkspaceID:     data.WorkspaceID,
				Title:           "Negative estimate item",
				StatusID:        &data.StatusID,
				CreatorID:       &data.UserID,
				StoryPoints:     tt.storyPoints,
				EstimateMinutes: tt.estimate,
			})
			if id != 0 {
				t.Fatalf("CreateItem returned item id %d, want 0", id)
			}
			var validationErr *validation.ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("CreateItem error = %v, want *validation.ValidationError", err)
			}
			if validationErr.Field != tt.wantField || validationErr.Message != tt.wantMessage {
				t.Fatalf("CreateItem error = %q/%q, want %q/%q",
					validationErr.Field, validationErr.Message, tt.wantField, tt.wantMessage)
			}
		})
	}
}

// Zero and positive estimates are accepted and persisted verbatim.
func TestCreateItemAcceptsZeroAndPositiveEstimates(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	id, err := CreateItem(db, ItemCreationParams{
		WorkspaceID:     data.WorkspaceID,
		Title:           "Estimated item",
		StatusID:        &data.StatusID,
		CreatorID:       &data.UserID,
		StoryPoints:     fPtr(0),
		EstimateMinutes: iPtr(90),
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	var storyPoints float64
	var estimateMinutes int
	if err := db.QueryRow(
		"SELECT story_points, estimate_minutes FROM items WHERE id = ?", id,
	).Scan(&storyPoints, &estimateMinutes); err != nil {
		t.Fatalf("load created item: %v", err)
	}
	if storyPoints != 0 || estimateMinutes != 90 {
		t.Fatalf("persisted estimates = (%v, %v), want (0, 90)", storyPoints, estimateMinutes)
	}
}

func fPtr(v float64) *float64 { return &v }
func iPtr(v int) *int         { return &v }
