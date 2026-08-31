//go:build test

package services

import (
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestUpwardHierarchyWalksStopAfterLevelZero(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	var genericTypeID, levelZeroTypeID, levelOneTypeID int
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE hierarchy_level = -1 LIMIT 1`).Scan(&genericTypeID); err != nil {
		t.Fatalf("load generic subtask type: %v", err)
	}
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE hierarchy_level = 0 LIMIT 1`).Scan(&levelZeroTypeID); err != nil {
		t.Fatalf("load level-0 item type: %v", err)
	}
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE hierarchy_level = 1 LIMIT 1`).Scan(&levelOneTypeID); err != nil {
		t.Fatalf("load level-1 item type: %v", err)
	}

	var projectID int
	if err := tdb.QueryRow(`
		INSERT INTO time_projects (name, status)
		VALUES ('Project above level zero', 'Active')
		RETURNING id
	`).Scan(&projectID); err != nil {
		t.Fatalf("insert time project: %v", err)
	}

	aboveBoundaryID64, err := CreateItem(tdb.DB, ItemCreationParams{
		WorkspaceID:    data.WorkspaceID,
		ItemTypeID:     &levelOneTypeID,
		Title:          "Stored parent above level zero",
		ProjectID:      &projectID,
		InheritProject: false,
	})
	if err != nil {
		t.Fatalf("insert stored parent above boundary: %v", err)
	}
	aboveBoundaryID := int(aboveBoundaryID64)

	boundaryID64, err := CreateItem(tdb.DB, ItemCreationParams{
		WorkspaceID:    data.WorkspaceID,
		ItemTypeID:     &levelZeroTypeID,
		Title:          "Level zero boundary",
		ParentID:       &aboveBoundaryID,
		InheritProject: true,
	})
	if err != nil {
		t.Fatalf("insert level-zero boundary: %v", err)
	}
	boundaryID := int(boundaryID64)

	childID64, err := CreateItem(tdb.DB, ItemCreationParams{
		WorkspaceID:    data.WorkspaceID,
		ItemTypeID:     &genericTypeID,
		Title:          "Generic subtask",
		ParentID:       &boundaryID,
		InheritProject: true,
	})
	if err != nil {
		t.Fatalf("insert generic subtask: %v", err)
	}
	childID := int(childID64)

	service := NewHierarchyService(tdb.DB)

	ancestors, err := service.GetAncestors(childID)
	if err != nil {
		t.Fatalf("get lightweight ancestors: %v", err)
	}
	assertOnlyLevelZeroAncestor(t, ancestors, boundaryID)

	fullAncestors, err := repository.NewItemRepository(tdb.DB).GetAncestors(childID)
	if err != nil {
		t.Fatalf("get detailed ancestors: %v", err)
	}
	if len(fullAncestors) != 1 || fullAncestors[0].ID != boundaryID {
		t.Fatalf("detailed ancestors = %#v, want only level-zero item %d", fullAncestors, boundaryID)
	}

	root, err := service.GetRoot(childID)
	if err != nil {
		t.Fatalf("get root: %v", err)
	}
	if root == nil || root.ID != boundaryID {
		t.Fatalf("root = %#v, want level-zero item %d", root, boundaryID)
	}

	effectiveProjectID, mode, err := service.GetEffectiveProject(childID)
	if err != nil {
		t.Fatalf("resolve project above empty level-zero boundary: %v", err)
	}
	if effectiveProjectID != nil {
		t.Fatalf("effective project = %d, must not cross level-zero boundary to project %d", *effectiveProjectID, projectID)
	}
	if mode != "inherit" {
		t.Fatalf("inheritance mode = %q, want inherit", mode)
	}

	if _, err := tdb.Exec(`
		UPDATE items
		SET project_id = ?, inherit_project = false
		WHERE id = ?
	`, projectID, boundaryID); err != nil {
		t.Fatalf("assign project to level-zero boundary: %v", err)
	}

	effectiveProjectID, mode, err = service.GetEffectiveProject(childID)
	if err != nil {
		t.Fatalf("resolve project at level-zero boundary: %v", err)
	}
	if effectiveProjectID == nil || *effectiveProjectID != projectID {
		t.Fatalf("effective project = %v, want level-zero project %d", effectiveProjectID, projectID)
	}
	if mode != "inherit" {
		t.Fatalf("inheritance mode = %q, want inherit", mode)
	}
}

func assertOnlyLevelZeroAncestor(t *testing.T, ancestors []models.Item, boundaryID int) {
	t.Helper()
	if len(ancestors) != 1 || ancestors[0].ID != boundaryID {
		t.Fatalf("ancestors = %#v, want only level-zero item %d", ancestors, boundaryID)
	}
}
