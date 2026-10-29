//go:build test

package repository

import (
	"testing"
	"time"

	"windshift/internal/testutils"
)

// ListChildBriefings feeds the "Catch Me Up" hierarchy context. It must return
// direct children only, ordered by board position, carry the fields the
// briefing renders, and respect the caller's cap.
func TestListChildBriefingsReturnsDirectChildren(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	repo := NewItemRepository(tdb.GetDatabase())
	testData := setupHierarchyTestData(t, tdb)

	// A grandchild must never appear: Catch Me Up does not cascade.
	now := time.Now()
	if _, err := tdb.DB.Exec(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, status_id, is_task,
		                   parent_id, frac_index, path, created_at, updated_at)
		VALUES (?, 4, 'Grandchild Item', '', (SELECT status_id FROM items WHERE id = ?), false, ?, 'h3', '/1/2/4/', ?, ?)
	`, testData.WorkspaceID, testData.ChildID1, testData.ChildID1, now, now); err != nil {
		t.Fatalf("Failed to create grandchild item: %v", err)
	}

	// Assign a child so the assignee join is exercised.
	if _, err := tdb.DB.Exec("UPDATE items SET assignee_id = ? WHERE id = ?", testData.UserID, testData.ChildID2); err != nil {
		t.Fatalf("Failed to set child assignee: %v", err)
	}

	children, err := repo.ListChildBriefings(testData.WorkspaceID, testData.ParentID, 20)
	if err != nil {
		t.Fatalf("ListChildBriefings returned error: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("Expected 2 direct children (no grandchildren), got %d", len(children))
	}

	first := children[0]
	if first.ID != testData.ChildID1 {
		t.Errorf("Expected first child %d, got %d", testData.ChildID1, first.ID)
	}
	if first.Title != "Child Item 1" {
		t.Errorf("Expected title %q, got %q", "Child Item 1", first.Title)
	}
	if first.StatusName == "" {
		t.Error("Expected a resolved status name for the first child")
	}
	if first.Description != "Child 1 Description" {
		t.Errorf("Expected child description %q, got %q", "Child 1 Description", first.Description)
	}
	if first.AssigneeName != "" {
		t.Errorf("Expected no assignee for the first child, got %q", first.AssigneeName)
	}

	second := children[1]
	if second.ID != testData.ChildID2 {
		t.Errorf("Expected second child %d, got %d", testData.ChildID2, second.ID)
	}
	if second.AssigneeName != "Test User" {
		t.Errorf("Expected assignee %q, got %q", "Test User", second.AssigneeName)
	}
}

// The cap is applied by the query, so a parent with many children cannot
// overflow the briefing context.
func TestListChildBriefingsRespectsLimit(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	repo := NewItemRepository(tdb.GetDatabase())
	testData := setupHierarchyTestData(t, tdb)

	children, err := repo.ListChildBriefings(testData.WorkspaceID, testData.ParentID, 1)
	if err != nil {
		t.Fatalf("ListChildBriefings returned error: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("Expected the limit to cap results at 1, got %d", len(children))
	}
	if children[0].ID != testData.ChildID1 {
		t.Errorf("Expected the first board-position child %d, got %d", testData.ChildID1, children[0].ID)
	}
}

// Children belong to the parent's workspace; a workspace mismatch must not leak
// children into the briefing.
func TestListChildBriefingsScopesToWorkspace(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	repo := NewItemRepository(tdb.GetDatabase())
	testData := setupHierarchyTestData(t, tdb)

	children, err := repo.ListChildBriefings(testData.WorkspaceID+1, testData.ParentID, 20)
	if err != nil {
		t.Fatalf("ListChildBriefings returned error: %v", err)
	}
	if len(children) != 0 {
		t.Fatalf("Expected no children from another workspace, got %d", len(children))
	}
}
