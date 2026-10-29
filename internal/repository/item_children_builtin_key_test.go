//go:build test

package repository

import (
	"context"
	"testing"

	"windshift/internal/testutils"
)

// The item projections consumed by the detail views must carry the status
// builtin key: the frontend resolves built-in status labels through it
// (objectDisplayName) and silently falls back to the raw DB name without it.
func TestGetChildrenContextPopulatesStatusBuiltinKey(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	repo := NewItemRepository(tdb.GetDatabase())
	testData := setupHierarchyTestData(t, tdb)

	children, err := repo.GetChildrenContext(context.Background(), testData.ParentID)
	if err != nil {
		t.Fatalf("GetChildrenContext returned error: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("Expected 2 children, got %d", len(children))
	}
	for _, child := range children {
		if child.StatusBuiltinKey == "" {
			t.Errorf("child %d has empty StatusBuiltinKey; built-in statuses must carry their stable key for localization", child.ID)
		}
		if child.StatusName == "" {
			t.Errorf("child %d has empty StatusName", child.ID)
		}
		if child.StatusColor == "" {
			t.Errorf("child %d has empty StatusColor", child.ID)
		}
	}
}

// FindLinkedItems feeds the linked-items list on the item detail page. The
// row shape must match scanItemRowBase exactly (status name, category color,
// and builtin key) or the scan fails or misassigns columns.
func TestFindLinkedItemsPopulatesStatusProjection(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	linkRepo := NewItemLinkRepository(tdb.GetDatabase())
	testData := setupHierarchyTestData(t, tdb)

	var linkTypeID int
	if err := tdb.DB.QueryRow("SELECT id FROM link_types LIMIT 1").Scan(&linkTypeID); err != nil {
		t.Fatalf("Failed to get link type: %v", err)
	}
	if _, err := tdb.DB.Exec(
		"INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id, created_by) VALUES (?, 'item', ?, 'item', ?, ?)",
		linkTypeID, testData.ParentID, testData.ChildID1, testData.UserID,
	); err != nil {
		t.Fatalf("Failed to create item link: %v", err)
	}

	linked, err := linkRepo.FindLinkedItems(testData.ParentID, nil, "both")
	if err != nil {
		t.Fatalf("FindLinkedItems returned error: %v", err)
	}
	if len(linked) != 1 {
		t.Fatalf("Expected 1 linked item, got %d", len(linked))
	}
	linkedItem := linked[0]
	if linkedItem.ID != testData.ChildID1 {
		t.Errorf("Expected linked item %d, got %d", testData.ChildID1, linkedItem.ID)
	}
	if linkedItem.StatusBuiltinKey == "" {
		t.Errorf("linked item %d has empty StatusBuiltinKey", linkedItem.ID)
	}
	if linkedItem.StatusName == "" {
		t.Errorf("linked item %d has empty StatusName", linkedItem.ID)
	}
	if linkedItem.StatusColor == "" {
		t.Errorf("linked item %d has empty StatusColor", linkedItem.ID)
	}
}
