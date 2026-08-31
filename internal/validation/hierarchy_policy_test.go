//go:build test

package validation

import (
	"errors"
	"testing"

	"windshift/internal/testutils"
)

func TestValidateParentForItemType_GenericSubtaskContract(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	typeIDs := map[int]int{}
	for _, level := range []int{-1, 0, 1, 2} {
		var id int
		if err := tdb.QueryRow(`
			SELECT id
			FROM item_types
			WHERE hierarchy_level = ?
			ORDER BY is_default DESC, id
			LIMIT 1
		`, level).Scan(&id); err != nil {
			t.Fatalf("load item type at hierarchy level %d: %v", level, err)
		}
		typeIDs[level] = id
	}

	// Hierarchy fixtures are staged via SQL because package validation cannot
	// depend on the item-creation service (import cycle); the parentless
	// generic-subtask row is additionally a deliberate invalid state the
	// validator must reject. Every row carries an explicit canonical rank.
	parentIDs := map[int]int{}
	nextNumber := 100
	for _, level := range []int{-1, 0, 1, 2} {
		var id int
		if err := tdb.QueryRow(`
			INSERT INTO items (workspace_id, workspace_item_number, item_type_id, title, frac_index)
			VALUES (?, ?, ?, ?, ?)
			RETURNING id
		`, data.WorkspaceID, nextNumber, typeIDs[level], "Parent fixture", testutils.NextTestFracIndex()).Scan(&id); err != nil {
			t.Fatalf("insert parent fixture at hierarchy level %d: %v", level, err)
		}
		parentIDs[level] = id
		nextNumber++
	}

	t.Run("generic subtask requires a parent", func(t *testing.T) {
		err := ValidateParentForItemType(tdb.DB, typeIDs[-1], nil)
		assertHierarchyValidationError(t, err, "parent_id")
	})

	for _, parentLevel := range []int{0, 1, 2} {
		t.Run("generic subtask accepts regular parent", func(t *testing.T) {
			parentID := parentIDs[parentLevel]
			if err := ValidateParentForItemType(tdb.DB, typeIDs[-1], &parentID); err != nil {
				t.Fatalf("generic subtask below hierarchy level %d: %v", parentLevel, err)
			}
		})
	}

	t.Run("generic subtask is terminal", func(t *testing.T) {
		parentID := parentIDs[-1]
		err := ValidateParentForItemType(tdb.DB, typeIDs[2], &parentID)
		assertHierarchyValidationError(t, err, "parent_id")
	})

	t.Run("generic subtask cannot parent another generic subtask", func(t *testing.T) {
		parentID := parentIDs[-1]
		err := ValidateParentForItemType(tdb.DB, typeIDs[-1], &parentID)
		assertHierarchyValidationError(t, err, "parent_id")
	})

	t.Run("regular hierarchy still requires adjacent levels", func(t *testing.T) {
		parentID := parentIDs[0]
		if err := ValidateParentForItemType(tdb.DB, typeIDs[1], &parentID); err != nil {
			t.Fatalf("adjacent regular hierarchy rejected: %v", err)
		}

		err := ValidateParentForItemType(tdb.DB, typeIDs[2], &parentID)
		assertHierarchyValidationError(t, err, "parent_id")
	})
}

func TestValidateItemTypePlacement_GenericSubtaskCannotHaveChildren(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	var genericTypeID, levelZeroTypeID, levelOneTypeID, levelTwoTypeID int
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE hierarchy_level = -1 LIMIT 1`).Scan(&genericTypeID); err != nil {
		t.Fatalf("load generic subtask type: %v", err)
	}
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE hierarchy_level = 0 LIMIT 1`).Scan(&levelZeroTypeID); err != nil {
		t.Fatalf("load level-0 item type: %v", err)
	}
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE hierarchy_level = 1 LIMIT 1`).Scan(&levelOneTypeID); err != nil {
		t.Fatalf("load level-1 item type: %v", err)
	}
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE hierarchy_level = 2 LIMIT 1`).Scan(&levelTwoTypeID); err != nil {
		t.Fatalf("load level-2 item type: %v", err)
	}

	var parentID int
	if err := tdb.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, item_type_id, title, frac_index)
		VALUES (?, 199, ?, 'Parent', ?)
		RETURNING id
	`, data.WorkspaceID, levelZeroTypeID, testutils.NextTestFracIndex()).Scan(&parentID); err != nil {
		t.Fatalf("insert parent: %v", err)
	}

	var itemID int
	if err := tdb.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, item_type_id, title, parent_id, frac_index)
		VALUES (?, 200, ?, 'Item with child', ?, ?)
		RETURNING id
	`, data.WorkspaceID, levelOneTypeID, parentID, testutils.NextTestFracIndex()).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO items (workspace_id, workspace_item_number, item_type_id, title, parent_id, frac_index)
		VALUES (?, 201, ?, 'Existing child', ?, ?)
	`, data.WorkspaceID, levelTwoTypeID, itemID, testutils.NextTestFracIndex()); err != nil {
		t.Fatalf("insert child: %v", err)
	}

	err := ValidateItemTypePlacement(tdb.DB, itemID, genericTypeID, &parentID)
	assertHierarchyValidationError(t, err, "item_type_id")
}

func assertHierarchyValidationError(t *testing.T, err error, field string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected hierarchy validation error")
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want *ValidationError: %v", err, err)
	}
	if validationErr.Field != field {
		t.Fatalf("validation field = %q, want %q", validationErr.Field, field)
	}
}
