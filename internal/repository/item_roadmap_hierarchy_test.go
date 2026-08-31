//go:build test

package repository

import (
	"context"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

func insertRoadmapHierarchyItem(t *testing.T, db database.Database, workspaceID, number int, parentID *int, startDate, endDate string) int {
	t.Helper()
	return testutils.InsertID(t, db, `
		INSERT INTO items (
			workspace_id, workspace_item_number, title, parent_id,
			start_date, end_date, frac_index, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, workspaceID, number, "roadmap hierarchy item", parentID, startDate, endDate,
		testutils.NextTestFracIndex(), time.Now().UTC(), time.Now().UTC())
}

func TestGetRoadmapHierarchyDatesReturnsCompleteDistinctSubtree(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	workspaceID := testutils.InsertID(t, db,
		`INSERT INTO workspaces (name, key, description, active) VALUES (?, ?, '', true)`,
		"Roadmap hierarchy", "RMH")

	rootID := insertRoadmapHierarchyItem(t, db, workspaceID, 1, nil, "2026-08-01", "2026-08-31")
	childID := insertRoadmapHierarchyItem(t, db, workspaceID, 2, &rootID, "2026-08-10", "2026-08-20")
	grandchildID := insertRoadmapHierarchyItem(t, db, workspaceID, 3, &childID, "2026-08-12", "2026-08-14")
	insertRoadmapHierarchyItem(t, db, workspaceID, 4, nil, "2026-09-01", "2026-09-02")

	rootWorkspaces, err := NewItemRepository(db).GetRoadmapHierarchyRootWorkspaceIDs(context.Background(), []int{rootID, childID, rootID, -1})
	if err != nil {
		t.Fatalf("GetRoadmapHierarchyRootWorkspaceIDs: %v", err)
	}
	if len(rootWorkspaces) != 2 || rootWorkspaces[rootID] != workspaceID || rootWorkspaces[childID] != workspaceID {
		t.Fatalf("root workspaces = %+v, want root and child mapped to workspace %d", rootWorkspaces, workspaceID)
	}

	items, truncated, err := NewItemRepository(db).GetRoadmapHierarchyDates(context.Background(), []int{rootID, childID})
	if err != nil {
		t.Fatalf("GetRoadmapHierarchyDates: %v", err)
	}
	if truncated {
		t.Fatal("hierarchy unexpectedly truncated")
	}
	if len(items) != 3 {
		t.Fatalf("items = %+v, want one distinct row for each of three hierarchy items", items)
	}
	if items[0].ID != rootID || items[1].ID != childID || items[2].ID != grandchildID {
		t.Fatalf("item IDs = [%d %d %d], want [%d %d %d]", items[0].ID, items[1].ID, items[2].ID, rootID, childID, grandchildID)
	}
	if items[2].ParentID == nil || *items[2].ParentID != childID || items[2].StartDate != "2026-08-12" || items[2].EndDate != "2026-08-14" {
		t.Fatalf("grandchild = %+v, want parent and exact scheduling dates", items[2])
	}
}
