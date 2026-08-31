//go:build test

package services

import (
	"testing"

	"windshift/internal/testutils"
)

func TestPageService_Move_BackfillClearsExistingSiblingKeysBeforeResequencing(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	service := NewPageService(tdb.GetDatabase())

	destination, err := service.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "Destination",
	})
	if err != nil {
		t.Fatalf("create destination parent: %v", err)
	}
	legacySibling, err := service.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		ParentID:    &destination.ID,
		Title:       "Legacy sibling",
	})
	if err != nil {
		t.Fatalf("create legacy sibling: %v", err)
	}
	firstKey := "a0"
	indexedSibling, err := service.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		ParentID:    &destination.ID,
		Title:       "Indexed sibling",
		FracIndex:   &firstKey,
	})
	if err != nil {
		t.Fatalf("create indexed sibling: %v", err)
	}
	movingPage, err := service.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "Moving page",
	})
	if err != nil {
		t.Fatalf("create moving page: %v", err)
	}

	moved, err := service.Move(
		data.UserID,
		movingPage.ID,
		&destination.ID,
		&legacySibling.ID,
		&indexedSibling.ID,
	)
	if err != nil {
		t.Fatalf("move between mixed-index siblings: %v", err)
	}
	if moved.ParentID == nil || *moved.ParentID != destination.ID {
		t.Fatalf("moved parent = %v, want %d", moved.ParentID, destination.ID)
	}

	children, err := service.ListChildren(data.WorkspaceID, &destination.ID)
	if err != nil {
		t.Fatalf("list destination children: %v", err)
	}
	if len(children) != 3 {
		t.Fatalf("destination children count = %d, want 3", len(children))
	}
	wantOrder := []int{legacySibling.ID, movingPage.ID, indexedSibling.ID}
	seenKeys := make(map[string]int, len(children))
	for i, child := range children {
		if child.ID != wantOrder[i] {
			t.Fatalf("destination child %d = page %d, want page %d", i, child.ID, wantOrder[i])
		}
		if child.FracIndex == nil || *child.FracIndex == "" {
			t.Fatalf("destination child %d has empty frac_index", child.ID)
		}
		if previousID, duplicate := seenKeys[*child.FracIndex]; duplicate {
			t.Fatalf("pages %d and %d share frac_index %q", previousID, child.ID, *child.FracIndex)
		}
		seenKeys[*child.FracIndex] = child.ID
	}
}

// TestPageService_Move_BackfillIgnoresArchivedSiblingKeys reproduces a move
// failure where an archived sibling still owned frac_index "a0" in the target
// sibling set. The backfill re-mints live-sibling keys starting from "a0", so
// before idx_pages_frac_index_scoped excluded archived rows the SetFracIndex
// on the first live sibling collided with the archived row's key.
func TestPageService_Move_BackfillIgnoresArchivedSiblingKeys(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	service := NewPageService(tdb.GetDatabase())

	destination, err := service.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "Destination",
	})
	if err != nil {
		t.Fatalf("create destination parent: %v", err)
	}

	// An archived sibling that still owns the first key of the sibling set.
	firstKey := "a0"
	archivedSibling, err := service.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		ParentID:    &destination.ID,
		Title:       "Archived sibling",
		FracIndex:   &firstKey,
	})
	if err != nil {
		t.Fatalf("create archived sibling: %v", err)
	}
	if err := service.Archive(data.UserID, archivedSibling.ID); err != nil {
		t.Fatalf("archive sibling: %v", err)
	}

	// A live legacy sibling (NULL frac_index) forces the backfill branch.
	legacySibling, err := service.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		ParentID:    &destination.ID,
		Title:       "Legacy sibling",
	})
	if err != nil {
		t.Fatalf("create legacy sibling: %v", err)
	}
	movingPage, err := service.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "Moving page",
	})
	if err != nil {
		t.Fatalf("create moving page: %v", err)
	}

	// Move after the legacy sibling: the backfill re-sequences the live set
	// starting at "a0", which must not collide with the archived row.
	moved, err := service.Move(
		data.UserID,
		movingPage.ID,
		&destination.ID,
		&legacySibling.ID,
		nil,
	)
	if err != nil {
		t.Fatalf("move alongside archived sibling: %v", err)
	}
	if moved.ParentID == nil || *moved.ParentID != destination.ID {
		t.Fatalf("moved parent = %v, want %d", moved.ParentID, destination.ID)
	}

	children, err := service.ListChildren(data.WorkspaceID, &destination.ID)
	if err != nil {
		t.Fatalf("list destination children: %v", err)
	}
	if len(children) != 2 || children[0].ID != legacySibling.ID || children[1].ID != movingPage.ID {
		t.Fatalf("child order = %+v, want live pages [%d %d]", children, legacySibling.ID, movingPage.ID)
	}
}

// TestPageService_Unarchive_ClearsFracIndexToAvoidCollision verifies that a
// page whose old frac_index was reused by a live sibling while it was archived
// re-enters the tree without tripping idx_pages_frac_index_scoped.
func TestPageService_Unarchive_ClearsFracIndexToAvoidCollision(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	service := NewPageService(tdb.GetDatabase())

	parent, err := service.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "Parent",
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	firstKey := "a0"
	toArchive, err := service.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		ParentID:    &parent.ID,
		Title:       "To archive",
		FracIndex:   &firstKey,
	})
	if err != nil {
		t.Fatalf("create page to archive: %v", err)
	}
	if err := service.Archive(data.UserID, toArchive.ID); err != nil {
		t.Fatalf("archive page: %v", err)
	}

	// A live sibling now mints the same "a0" key the archived page still owns.
	if _, err := service.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		ParentID:    &parent.ID,
		Title:       "Live sibling",
		FracIndex:   &firstKey,
	}); err != nil {
		t.Fatalf("create live sibling reusing key: %v", err)
	}

	restored, err := service.Unarchive(data.UserID, toArchive.ID)
	if err != nil {
		t.Fatalf("unarchive page holding reused key: %v", err)
	}
	if restored.FracIndex != nil {
		t.Fatalf("unarchived frac_index = %v, want nil (cleared)", restored.FracIndex)
	}
}

func TestPageService_Move_BackfillClearsMovingPagesExistingKey(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	service := NewPageService(tdb.GetDatabase())

	parent, err := service.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "Parent",
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	legacySibling, err := service.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		ParentID:    &parent.ID,
		Title:       "Legacy sibling",
	})
	if err != nil {
		t.Fatalf("create legacy sibling: %v", err)
	}
	firstKey := "a0"
	movingPage, err := service.Create(data.UserID, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		ParentID:    &parent.ID,
		Title:       "Moving page",
		FracIndex:   &firstKey,
	})
	if err != nil {
		t.Fatalf("create moving page: %v", err)
	}

	moved, err := service.Move(
		data.UserID,
		movingPage.ID,
		&parent.ID,
		&legacySibling.ID,
		nil,
	)
	if err != nil {
		t.Fatalf("reorder after legacy sibling: %v", err)
	}
	if moved.FracIndex == nil || *moved.FracIndex <= firstKey {
		t.Fatalf("moved frac_index = %v, want a key after %q", moved.FracIndex, firstKey)
	}

	children, err := service.ListChildren(data.WorkspaceID, &parent.ID)
	if err != nil {
		t.Fatalf("list children: %v", err)
	}
	if len(children) != 2 || children[0].ID != legacySibling.ID || children[1].ID != movingPage.ID {
		t.Fatalf("child order = %+v, want pages [%d %d]", children, legacySibling.ID, movingPage.ID)
	}
}
