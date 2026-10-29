//go:build test

package services

import (
	"errors"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
	"windshift/internal/validation"
)

func TestCreateItem_GenericSubtaskRequiresRegularParent(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	var genericTypeID, parentTypeID int
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE hierarchy_level = -1 LIMIT 1`).Scan(&genericTypeID); err != nil {
		t.Fatalf("load generic subtask type: %v", err)
	}
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE hierarchy_level = 0 LIMIT 1`).Scan(&parentTypeID); err != nil {
		t.Fatalf("load level-0 item type: %v", err)
	}

	_, err := CreateItem(tdb.DB, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Orphan generic subtask",
		ItemTypeID:  &genericTypeID,
	})
	assertCreateHierarchyValidationError(t, err, "parent_id")

	parentID64, err := CreateItem(tdb.DB, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Regular parent",
		ItemTypeID:  &parentTypeID,
	})
	if err != nil {
		t.Fatalf("create regular parent: %v", err)
	}
	parentID := int(parentID64)

	genericID, err := CreateItem(tdb.DB, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Generic subtask",
		ItemTypeID:  &genericTypeID,
		ParentID:    &parentID,
	})
	if err != nil {
		t.Fatalf("create generic subtask below level 0: %v", err)
	}

	_, err = CreateItem(tdb.DB, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Child below generic subtask",
		ItemTypeID:  &genericTypeID,
		ParentID:    int64ToIntPointer(genericID),
	})
	assertCreateHierarchyValidationError(t, err, "parent_id")
}

func TestCreateItem_JiraStagingExceptionOnlyAllowsGenericSubtask(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	var genericTypeID, regularTypeID int
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE hierarchy_level = -1 LIMIT 1`).Scan(&genericTypeID); err != nil {
		t.Fatalf("load generic subtask type: %v", err)
	}
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE hierarchy_level = 2 LIMIT 1`).Scan(&regularTypeID); err != nil {
		t.Fatalf("load regular item type: %v", err)
	}

	if _, err := CreateItem(tdb.DB, ItemCreationParams{
		WorkspaceID:                   data.WorkspaceID,
		Title:                         "Staged Jira generic subtask",
		ItemTypeID:                    &genericTypeID,
		AllowUnparentedGenericSubtask: true,
	}); err != nil {
		t.Fatalf("stage Jira generic subtask: %v", err)
	}

	if _, err := CreateItem(tdb.DB, ItemCreationParams{
		WorkspaceID:                   data.WorkspaceID,
		Title:                         "Regular root",
		ItemTypeID:                    &regularTypeID,
		AllowUnparentedGenericSubtask: true,
	}); err != nil {
		t.Fatalf("Jira exception changed valid regular root behavior: %v", err)
	}
}

func TestItemTypeChange_GenericSubtaskPlacementUsesTargetFieldContract(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	var genericTypeID, regularTypeID int
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE hierarchy_level = -1 LIMIT 1`).Scan(&genericTypeID); err != nil {
		t.Fatalf("load generic subtask type: %v", err)
	}
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE hierarchy_level = 2 LIMIT 1`).Scan(&regularTypeID); err != nil {
		t.Fatalf("load regular item type: %v", err)
	}

	itemID64, err := CreateItem(tdb.DB, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		ItemTypeID:  &regularTypeID,
		Title:       "Root item",
	})
	if err != nil {
		t.Fatalf("insert root item: %v", err)
	}
	itemID := int(itemID64)

	_, err = NewItemTypeChangeService(tdb.DB).Analyze(&models.Item{
		ID:          itemID,
		WorkspaceID: data.WorkspaceID,
		ItemTypeID:  &regularTypeID,
	}, genericTypeID)
	if err == nil {
		t.Fatal("expected root-to-generic type change to be rejected")
	}
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want *validation.ValidationError: %v", err, err)
	}
	if validationErr.Field != "target_item_type_id" {
		t.Fatalf("validation field = %q, want target_item_type_id", validationErr.Field)
	}
}

func assertCreateHierarchyValidationError(t *testing.T, err error, field string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected hierarchy validation error")
	}
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want *validation.ValidationError: %v", err, err)
	}
	if validationErr.Field != field {
		t.Fatalf("validation field = %q, want %q", validationErr.Field, field)
	}
}

func int64ToIntPointer(value int64) *int {
	converted := int(value)
	return &converted
}
