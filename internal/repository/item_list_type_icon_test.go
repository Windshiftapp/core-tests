//go:build test

package repository

import (
	"context"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// itemWithOmittedType seeds a workspace whose effective configuration set does
// not enumerate the seeded Initiative type, plus one item of that type. The
// workspace item-type catalog intentionally omits it, so list surfaces cannot
// rely on that catalog to resolve the icon.
func itemWithOmittedType(t *testing.T) (db database.Database, workspaceID, itemID int, icon, color string) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db = tdb.GetDatabase()

	workspaceID = testutils.InsertID(t, db,
		`INSERT INTO workspaces (key, name, is_personal) VALUES (?, ?, ?)`,
		"TYPICON", "Type Icon Workspace", false)

	var initiativeID int
	if err := db.QueryRow(
		`SELECT id, icon, color FROM item_types WHERE name = ?`, "Initiative",
	).Scan(&initiativeID, &icon, &color); err != nil {
		t.Fatalf("load Initiative item type: %v", err)
	}

	catalog, err := NewItemTypeRepository(db).ListForWorkspace(workspaceID)
	if err != nil {
		t.Fatalf("ListForWorkspace: %v", err)
	}
	for _, itemType := range catalog {
		if itemType.ID == initiativeID {
			t.Fatalf("Initiative unexpectedly present in the workspace catalog; fixture no longer covers the omitted type")
		}
	}

	now := time.Now().UTC()
	itemID = testutils.InsertID(t, db, `
		INSERT INTO items (workspace_id, workspace_item_number, item_type_id, title, description, frac_index, created_at, updated_at)
		VALUES (?, ?, ?, ?, '', ?, ?, ?)`,
		workspaceID, 1, initiativeID, "Initiative icon probe", "a0", now, now)
	return db, workspaceID, itemID, icon, color
}

// The list projection must carry the item type's icon and color so rows render
// the real type even when the effective configuration set excludes it.
func TestFindAllWithDetailsCarriesItemTypeIcon(t *testing.T) {
	db, workspaceID, itemID, icon, color := itemWithOmittedType(t)

	items, total, err := NewItemRepository(db).FindAllWithDetailsContext(context.Background(), ItemListParams{
		WorkspaceIDs: []int{workspaceID},
		Pagination:   PaginationParams{Limit: 10},
	})
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("items = %d (total %d), want 1", len(items), total)
	}
	if items[0].ID != itemID {
		t.Fatalf("item id = %d, want %d", items[0].ID, itemID)
	}
	if items[0].ItemTypeIcon != icon {
		t.Fatalf("item type icon = %q, want %q", items[0].ItemTypeIcon, icon)
	}
	if items[0].ItemTypeColor != color {
		t.Fatalf("item type color = %q, want %q", items[0].ItemTypeColor, color)
	}
}

// Batch-by-id loads patch already-rendered collection rows after live updates,
// so they must carry the same icon/color as the list projection.
func TestFindByIDsWithDetailsCarriesItemTypeIcon(t *testing.T) {
	db, _, itemID, icon, color := itemWithOmittedType(t)

	items, err := NewItemRepository(db).FindByIDsWithDetails([]int{itemID})
	if err != nil {
		t.Fatalf("find items by ids: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].ItemTypeIcon != icon {
		t.Fatalf("item type icon = %q, want %q", items[0].ItemTypeIcon, icon)
	}
	if items[0].ItemTypeColor != color {
		t.Fatalf("item type color = %q, want %q", items[0].ItemTypeColor, color)
	}
}
