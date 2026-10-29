package services

import (
	"testing"

	"windshift/internal/testutils"
)

func TestWorkspaceServiceGetItemTypesReturnsUniqueHierarchyOrder(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	data := tdb.SeedTestData(t)

	// New workspaces bind a subset of the global item-type catalog to their
	// configuration set, so the workspace listing is the bound set rather than
	// every seeded item type.
	var catalogCount int
	if err := tdb.QueryRow(`
		SELECT COUNT(*)
		FROM item_types it
		WHERE EXISTS (
			SELECT 1
			FROM configuration_set_item_types csit
			WHERE csit.configuration_set_id = (
				SELECT COALESCE(wcs.configuration_set_id,
					(SELECT id FROM configuration_sets WHERE is_default = true ORDER BY id LIMIT 1))
				FROM workspaces w
				LEFT JOIN workspace_configuration_sets wcs ON wcs.workspace_id = w.id
				WHERE w.id = ?
			)
			AND csit.item_type_id = it.id
		)`, data.WorkspaceID).Scan(&catalogCount); err != nil {
		t.Fatalf("count workspace item types: %v", err)
	}
	if catalogCount == 0 {
		t.Fatal("seed bound no item types to the workspace")
	}

	got, err := NewWorkspaceService(tdb.DB).GetItemTypes(data.WorkspaceID)
	if err != nil {
		t.Fatalf("GetItemTypes: %v", err)
	}
	if len(got) != catalogCount {
		t.Fatalf("item type count = %d, want complete workspace catalog of %d", len(got), catalogCount)
	}

	seen := make(map[int]struct{}, len(got))
	for index, itemType := range got {
		if _, exists := seen[itemType.ID]; exists {
			t.Fatalf("item type %d (%s) returned more than once", itemType.ID, itemType.Name)
		}
		seen[itemType.ID] = struct{}{}

		if itemType.HierarchyLevel == -1 && index != len(got)-1 {
			t.Fatalf("level-independent item type %q is at index %d, want it last", itemType.Name, index)
		}
	}
}
