//go:build test

package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
	"windshift/internal/validation"
)

func seedAssignmentTeam(t *testing.T, tdb *testutils.TestDB, name string, active bool) models.Team {
	t.Helper()
	var id int
	if err := tdb.QueryRow(
		`INSERT INTO teams (name, description, is_active) VALUES (?, '', ?) RETURNING id`,
		name, active,
	).Scan(&id); err != nil {
		t.Fatalf("seed team %q: %v", name, err)
	}
	return models.Team{ID: id, Name: name, IsActive: active}
}

func TestCreateItemAssignsTeamAndPersists(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	team := seedAssignmentTeam(t, tdb, "Platform", true)

	id, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Item with assigned team",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
		TeamID:      &team.ID,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	item, err := repository.NewItemRepository(tdb.GetDatabase()).FindByIDWithDetails(int(id))
	if err != nil {
		t.Fatalf("FindByIDWithDetails: %v", err)
	}
	if item.TeamID == nil || *item.TeamID != team.ID {
		t.Fatalf("item.TeamID = %v, want %d", item.TeamID, team.ID)
	}
	if item.TeamName != "Platform" {
		t.Fatalf("item.TeamName = %q, want %q", item.TeamName, "Platform")
	}
}

func TestCreateItemRejectsUnknownTeam(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	missingTeam := 999999
	id, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Item with unknown team",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
		TeamID:      &missingTeam,
	})
	if id != 0 {
		t.Fatalf("CreateItem returned item id %d, want 0", id)
	}
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("CreateItem error = %v, want *validation.ValidationError", err)
	}
	if validationErr.Field != "team_id" || validationErr.Message != "Team not found" {
		t.Fatalf("error = %q/%q, want team_id/Team not found", validationErr.Field, validationErr.Message)
	}
}

func TestCreateItemRejectsInactiveTeam(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	team := seedAssignmentTeam(t, tdb, "Retired", false)

	id, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Item with inactive team",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
		TeamID:      &team.ID,
	})
	if id != 0 {
		t.Fatalf("CreateItem returned item id %d, want 0", id)
	}
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("CreateItem error = %v, want *validation.ValidationError", err)
	}
	if validationErr.Field != "team_id" || validationErr.Message != "Team is inactive" {
		t.Fatalf("error = %q/%q, want team_id/Team is inactive", validationErr.Field, validationErr.Message)
	}
}

func TestUpdateItemTeamIsPersistedAndFilterable(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	team := seedAssignmentTeam(t, tdb, "Payments", true)
	db := tdb.GetDatabase()
	repo := repository.NewItemRepository(db)

	id, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Assign team later",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if err := repo.UpdateFields(tx, int(id), map[string]any{"team_id": team.ID}); err != nil {
		t.Fatalf("UpdateFields team_id: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit team update: %v", err)
	}

	item, err := repo.FindByIDWithDetails(int(id))
	if err != nil {
		t.Fatalf("FindByIDWithDetails: %v", err)
	}
	if item.TeamID == nil || *item.TeamID != team.ID {
		t.Fatalf("item.TeamID = %v, want %d", item.TeamID, team.ID)
	}

	page, err := repo.FindAllWithDetailsPageContext(context.Background(), repository.ItemListParams{
		WorkspaceIDs: []int{data.WorkspaceID},
		Filters:      repository.ItemFilters{TeamID: &team.ID},
		Pagination:   repository.PaginationParams{Limit: 10},
	})
	if err != nil {
		t.Fatalf("FindAllWithDetailsPageContext: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != int(id) {
		t.Fatalf("team filter returned total=%d items=%d, want one item %d", page.Total, len(page.Items), id)
	}
}

func TestItemUpdateDataDecodesTeamID(t *testing.T) {
	fields := map[string]json.RawMessage{
		"team_id": json.RawMessage(`7`),
	}
	updateData, err := itemUpdateData(fields)
	if err != nil {
		t.Fatalf("itemUpdateData: %v", err)
	}
	value, ok := updateData["team_id"].(int)
	if !ok || value != 7 {
		t.Fatalf("updateData[team_id] = %#v, want int(7)", updateData["team_id"])
	}

	updateData, err = itemUpdateData(map[string]json.RawMessage{
		"team_id": json.RawMessage(`null`),
	})
	if err != nil {
		t.Fatalf("itemUpdateData null: %v", err)
	}
	if updateData["team_id"] != nil {
		t.Fatalf("updateData[team_id] for null = %#v, want nil", updateData["team_id"])
	}
}
