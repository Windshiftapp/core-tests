//go:build test

package repository

import (
	"context"
	"testing"
	"time"

	"windshift/internal/testutils"
)

func TestHierarchyDescendantsContextBreadthFirst(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	seed := tdb.SeedTestData(t)

	insert := func(number int, parentID *int) int {
		t.Helper()
		return testutils.InsertID(t, db, `
			INSERT INTO items (
				workspace_id, workspace_item_number, title, description, parent_id,
				status_id, priority_id, frac_index, created_at, updated_at
			) VALUES (?, ?, ?, '', ?, ?, ?, ?, ?, ?)
		`, seed.WorkspaceID, number, "hier item", parentID,
			seed.StatusID, seed.PriorityID, testutils.NextTestFracIndex(),
			time.Now().UTC(), time.Now().UTC())
	}

	epic := insert(1, nil)
	storyA := insert(2, &epic)
	storyB := insert(3, &epic)
	task := insert(4, &storyA)
	subtask := insert(5, &task)

	repo := NewItemRepository(db)
	groups, err := repo.HierarchyDescendantsContext(context.Background(), HierarchyReadParams{
		RootIDs:      []int{epic},
		WorkspaceIDs: []int{seed.WorkspaceID},
		PerRootLimit: 100,
	})
	if err != nil {
		t.Fatalf("HierarchyDescendantsContext: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	group := groups[0]
	if group.Total != 4 {
		t.Fatalf("total = %d, want 4", group.Total)
	}
	depths := map[int]int{}
	for _, entry := range group.Items {
		depths[entry.ID] = entry.Depth
	}
	if depths[storyA] != 1 || depths[storyB] != 1 || depths[task] != 2 || depths[subtask] != 3 {
		t.Fatalf("depths = %v", depths)
	}
}

// TestHierarchyDescendantsContextHasMoreRespectsOffset guards the per-root
// pagination contract: a page that exhausts the root must report has_more
// false, so the tree stops offering an empty "load more".
func TestHierarchyDescendantsContextHasMoreRespectsOffset(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	seed := tdb.SeedTestData(t)

	insert := func(number int, parentID *int) int {
		t.Helper()
		return testutils.InsertID(t, db, `
			INSERT INTO items (
				workspace_id, workspace_item_number, title, description, parent_id,
				status_id, priority_id, frac_index, created_at, updated_at
			) VALUES (?, ?, ?, '', ?, ?, ?, ?, ?, ?)
		`, seed.WorkspaceID, number, "hier item", parentID,
			seed.StatusID, seed.PriorityID, testutils.NextTestFracIndex(),
			time.Now().UTC(), time.Now().UTC())
	}

	root := insert(1, nil)
	for i := 2; i <= 6; i++ {
		insert(i, &root)
	}

	repo := NewItemRepository(db)
	fetch := func(offset int) HierarchyRootGroup {
		t.Helper()
		groups, err := repo.HierarchyDescendantsContext(context.Background(), HierarchyReadParams{
			RootIDs:       []int{root},
			WorkspaceIDs:  []int{seed.WorkspaceID},
			PerRootLimit:  2,
			PerRootOffset: offset,
		})
		if err != nil {
			t.Fatalf("HierarchyDescendantsContext(offset=%d): %v", offset, err)
		}
		if len(groups) != 1 {
			t.Fatalf("groups at offset %d = %d, want 1", offset, len(groups))
		}
		return groups[0]
	}

	cases := []struct {
		offset  int
		want    int
		hasMore bool
	}{
		{offset: 0, want: 2, hasMore: true},
		{offset: 2, want: 2, hasMore: true},
		{offset: 4, want: 1, hasMore: false},
	}
	for _, tc := range cases {
		group := fetch(tc.offset)
		if len(group.Items) != tc.want || group.Total != 5 {
			t.Fatalf("offset %d: items=%d total=%d, want items=%d total=5", tc.offset, len(group.Items), group.Total, tc.want)
		}
		if group.HasMore != tc.hasMore {
			t.Fatalf("offset %d: has_more = %v, want %v", tc.offset, group.HasMore, tc.hasMore)
		}
	}
}
