package repository

import (
	"context"
	"testing"

	"windshift/internal/models"
)

// runner_image round-trips through Insert/Get and defaults to empty when unset
// (WI-450).
func TestWorkspaceAgentBindingRepository_RunnerImageRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, admin, agentA, agentB := openBindingTestDB(t)
	repo := NewWorkspaceAgentBindingRepository(db)

	poolID := 3
	withImage, err := repo.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    agentA,
		ActingUserKind:  "agent",
		TargetPoolID:    &poolID,
		RunnerImage:     "ghcr.io/acme/playwright:1",
		TokenScopes:     []string{"items:read"},
		CreatedByUserID: admin,
	})
	if err != nil {
		t.Fatalf("insert with image: %v", err)
	}
	got, err := repo.Get(ctx, withImage)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RunnerImage != "ghcr.io/acme/playwright:1" {
		t.Errorf("RunnerImage: want ghcr.io/acme/playwright:1, got %q", got.RunnerImage)
	}

	noImage, err := repo.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    agentB,
		ActingUserKind:  "agent",
		TokenScopes:     []string{"items:read"},
		CreatedByUserID: admin,
	})
	if err != nil {
		t.Fatalf("insert without image: %v", err)
	}
	got2, err := repo.Get(ctx, noImage)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got2.RunnerImage != "" {
		t.Errorf("RunnerImage default: want empty, got %q", got2.RunnerImage)
	}
}
