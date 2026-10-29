//go:build test

package repository

import (
	"context"
	"testing"
	"time"

	"windshift/internal/testutils"
)

// Regression for GitHub #271: ancestor rows returned by the hierarchy-gap
// queries carry the status and priority of the real items, so the Tree and
// Roadmap views can render context parents instead of "No status".
func TestGetAncestorsIncludeStatusAndPriority(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	seed := tdb.SeedTestData(t)

	insertItem := func(t *testing.T, number int, parentID *int) int {
		t.Helper()
		return testutils.InsertID(t, db, `
			INSERT INTO items (
				workspace_id, workspace_item_number, title, description, parent_id,
				status_id, priority_id, frac_index, created_at, updated_at
			) VALUES (?, ?, ?, '', ?, ?, ?, ?, ?, ?)
		`, seed.WorkspaceID, number, "ancestor context item", parentID,
			seed.StatusID, seed.PriorityID, testutils.NextTestFracIndex(),
			time.Now().UTC(), time.Now().UTC())
	}

	epicID := insertItem(t, 1, nil)
	childID := insertItem(t, 2, &epicID)
	grandchildID := insertItem(t, 3, &childID)

	repo := NewItemRepository(db)

	t.Run("batch ancestors keep status and priority", func(t *testing.T) {
		chains, err := repo.GetAncestorsForItemsContext(context.Background(), []int{grandchildID}, 10)
		if err != nil {
			t.Fatalf("GetAncestorsForItemsContext: %v", err)
		}
		chain := chains[grandchildID]
		if len(chain) != 2 {
			t.Fatalf("chain length = %d, want epic and child", len(chain))
		}
		// Ordered root -> parent.
		epic, child := chain[0], chain[1]
		if epic.ID != epicID || child.ID != childID {
			t.Fatalf("chain IDs = [%d %d], want [%d %d]", epic.ID, child.ID, epicID, childID)
		}
		for _, ancestor := range chain {
			if ancestor.StatusID == nil || *ancestor.StatusID != seed.StatusID {
				t.Fatalf("ancestor %d status_id = %v, want %d", ancestor.ID, ancestor.StatusID, seed.StatusID)
			}
			if ancestor.PriorityID == nil || *ancestor.PriorityID != seed.PriorityID {
				t.Fatalf("ancestor %d priority_id = %v, want %d", ancestor.ID, ancestor.PriorityID, seed.PriorityID)
			}
		}
	})

	t.Run("hierarchy ancestors stay lightweight but bounded", func(t *testing.T) {
		// GetAncestorsForHierarchyContext is the lightweight projection used by
		// cycle-safety and deletion walks: it intentionally omits status and
		// priority (minimal fixtures lack those columns), so only structure is
		// asserted here.
		ancestors, err := repo.GetAncestorsForHierarchyContext(context.Background(), grandchildID, 0)
		if err != nil {
			t.Fatalf("GetAncestorsForHierarchyContext: %v", err)
		}
		if len(ancestors) != 2 {
			t.Fatalf("ancestors length = %d, want epic and child", len(ancestors))
		}
		if ancestors[0].ID != epicID || ancestors[1].ID != childID {
			t.Fatalf("ancestor IDs = [%d %d], want [%d %d]", ancestors[0].ID, ancestors[1].ID, epicID, childID)
		}
	})
}
