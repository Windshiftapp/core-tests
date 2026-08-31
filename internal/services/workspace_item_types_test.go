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

	var catalogCount int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM item_types`).Scan(&catalogCount); err != nil {
		t.Fatalf("count item types: %v", err)
	}

	got, err := NewWorkspaceService(tdb.DB).GetItemTypes(data.WorkspaceID)
	if err != nil {
		t.Fatalf("GetItemTypes: %v", err)
	}
	if len(got) != catalogCount {
		t.Fatalf("item type count = %d, want complete catalog of %d", len(got), catalogCount)
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
