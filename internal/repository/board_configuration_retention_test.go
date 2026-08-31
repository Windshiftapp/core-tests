//go:build test

package repository

import (
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

func TestBoardConfigurationRepositoryCompletedItemRetention(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	var workspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key) VALUES ('Retention board', 'RTN') RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	repo := NewBoardConfigurationRepository(tdb.GetDatabase())
	days := 30
	configID, err := repo.Create(nil, &workspaceID, &models.BoardConfigurationRequest{
		Columns:                    []models.BoardColumnRequest{},
		CompletedItemRetentionDays: &days,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	created, err := repo.GetByID(configID)
	if err != nil {
		t.Fatalf("GetByID after create: %v", err)
	}
	if created.CompletedItemRetentionDays == nil || *created.CompletedItemRetentionDays != 30 {
		t.Fatalf("retention after create = %v, want 30", created.CompletedItemRetentionDays)
	}

	days = 90
	if err := repo.Update(configID, &models.BoardConfigurationRequest{
		Columns:                    []models.BoardColumnRequest{},
		CompletedItemRetentionDays: &days,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, err := repo.GetByID(configID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if updated.CompletedItemRetentionDays == nil || *updated.CompletedItemRetentionDays != 90 {
		t.Fatalf("retention after update = %v, want 90", updated.CompletedItemRetentionDays)
	}
}
