package repository

import (
	"testing"

	"windshift/internal/testutils"
)

func TestSearchLinkableItemsReturnsCanonicalKeyAndTypePresentation(t *testing.T) {
	db := newItemListTestDB(t, "linkable-item-presentation")

	var workspaceID int
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Windshift', 'WI', '', true, false)
		RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}

	var itemTypeID int
	if err := db.QueryRow(`
		INSERT INTO item_types (name, icon, color)
		VALUES ('Link Picker Bug', 'Bug', '#e5484d')
		RETURNING id
	`).Scan(&itemTypeID); err != nil {
		t.Fatalf("insert item type: %v", err)
	}

	if _, err := db.ExecWrite(`
		INSERT INTO items (
			workspace_id, workspace_item_number, item_type_id, title, description, frac_index
		) VALUES (?, 657, ?, 'Picker result', 'Canonical key fixture', ?)
	`, workspaceID, itemTypeID, testutils.NextTestFracIndex()); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	items, err := NewItemRepository(db).SearchLinkableItems(
		"Picker",
		[]int{workspaceID},
		nil,
		10,
	)
	if err != nil {
		t.Fatalf("search linkable items: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("result count = %d, want 1", len(items))
	}

	item := items[0]
	if item.WorkspaceKey != "WI" {
		t.Errorf("workspace key = %q, want WI", item.WorkspaceKey)
	}
	if item.WorkspaceItemNumber == nil || *item.WorkspaceItemNumber != 657 {
		t.Errorf("workspace item number = %v, want 657", item.WorkspaceItemNumber)
	}
	if item.ItemTypeIcon != "Bug" {
		t.Errorf("item type icon = %q, want Bug", item.ItemTypeIcon)
	}
	if item.ItemTypeColor != "#e5484d" {
		t.Errorf("item type color = %q, want #e5484d", item.ItemTypeColor)
	}
}
