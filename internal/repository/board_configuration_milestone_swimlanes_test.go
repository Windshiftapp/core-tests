//go:build test

package repository

import (
	"reflect"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

func TestBoardConfigurationRepositoryMilestoneSwimlanes(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	var workspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key) VALUES ('Milestone board', 'MSB') RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	repo := NewBoardConfigurationRepository(tdb.GetDatabase())
	configID, err := repo.Create(nil, &workspaceID, &models.BoardConfigurationRequest{
		Columns: []models.BoardColumnRequest{},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The board-options JSON round-trips milestone-swimlane preferences.
	want := &models.ViewSettings{
		MilestoneSwimlanes: &models.MilestoneSwimlaneConfig{
			GlobalMilestoneIDs:    []int{4, 12},
			ShowCompleted:         false,
			ShowOpenFromCompleted: true,
		},
	}
	if err := repo.Update(configID, &models.BoardConfigurationRequest{
		Columns:      []models.BoardColumnRequest{},
		ViewSettings: want,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, err := repo.GetByID(configID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if updated.ViewSettings == nil {
		t.Fatal("view settings = nil, want milestone swimlane preferences")
	}
	if !reflect.DeepEqual(updated.ViewSettings.MilestoneSwimlanes, want.MilestoneSwimlanes) {
		t.Fatalf(
			"milestone swimlanes = %+v, want %+v",
			updated.ViewSettings.MilestoneSwimlanes,
			want.MilestoneSwimlanes,
		)
	}
}
