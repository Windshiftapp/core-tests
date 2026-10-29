//go:build test

package repository

import (
	"slices"
	"testing"

	"windshift/internal/testutils"
)

func TestSearchLinkableItemsByKey(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = db.Close() })
	workspaceID := testutils.InsertID(t, db, `INSERT INTO workspaces (name, key, active, is_personal) VALUES ('Key search', 'CR', true, false)`)
	otherWorkspaceID := testutils.InsertID(t, db, `INSERT INTO workspaces (name, key, active, is_personal) VALUES ('Other workspace', 'OTHER', true, false)`)
	itemTypeID := testutils.InsertID(t, db, `INSERT INTO item_types (name) VALUES ('Key search type')`)
	otherTypeID := testutils.InsertID(t, db, `INSERT INTO item_types (name) VALUES ('Other search type')`)
	itemID := testutils.InsertID(t, db, `INSERT INTO items (workspace_id, workspace_item_number, item_type_id, title, description, frac_index) VALUES (?, 4, ?, 'Alpha target', 'Searchable description', ?)`, workspaceID, itemTypeID, testutils.NextTestFracIndex())
	otherID := testutils.InsertID(t, db, `INSERT INTO items (workspace_id, workspace_item_number, item_type_id, title, description, frac_index) VALUES (?, 4, ?, 'Beta target', '', ?)`, otherWorkspaceID, otherTypeID, testutils.NextTestFracIndex())

	testutils.InsertID(t, db, `INSERT INTO items (workspace_id, workspace_item_number, item_type_id, title, description, frac_index) VALUES (?, 40, ?, 'Key prefix decoy', '', ?)`, workspaceID, itemTypeID, testutils.NextTestFracIndex())
	mentionID := testutils.InsertID(t, db, `INSERT INTO items (workspace_id, workspace_item_number, item_type_id, title, description, frac_index) VALUES (?, 5, ?, 'Mentions CR-4', '', ?)`, workspaceID, itemTypeID, testutils.NextTestFracIndex())

	for _, tt := range []struct {
		name       string
		query      string
		workspaces []int
		types      []int
		limit      int
		want       []int
	}{
		{name: "uppercase key", query: "CR-4", workspaces: []int{workspaceID, otherWorkspaceID}, want: []int{itemID}},
		{name: "lowercase key", query: "cr-4", workspaces: []int{workspaceID}, want: []int{itemID}},
		{name: "mixed case key", query: "Cr-4", workspaces: []int{workspaceID}, want: []int{itemID}},
		{name: "incomplete key uses text search", query: "cr-", workspaces: []int{workspaceID}, want: []int{mentionID}},
		{name: "trimmed key", query: "  cr-4  ", workspaces: []int{workspaceID}, want: []int{itemID}},
		{name: "other accessible workspace", query: "other-4", workspaces: []int{workspaceID, otherWorkspaceID}, want: []int{otherID}},
		{name: "inaccessible workspace", query: "OTHER-4", workspaces: []int{workspaceID}},
		{name: "no accessible workspaces", query: "CR-4"},
		{name: "matching item type", query: "CR-4", workspaces: []int{workspaceID}, types: []int{itemTypeID}, want: []int{itemID}},
		{name: "excluded item type", query: "CR-4", workspaces: []int{workspaceID}, types: []int{otherTypeID}},
		{name: "type filter applies before limit", query: "target", workspaces: []int{workspaceID, otherWorkspaceID}, types: []int{otherTypeID}, limit: 1, want: []int{otherID}},
		{name: "multiple item types", query: "target", workspaces: []int{workspaceID, otherWorkspaceID}, types: []int{itemTypeID, otherTypeID}, want: []int{itemID, otherID}},
		{name: "zero item number uses text search", query: "CR-0", workspaces: []int{workspaceID}},
		{name: "negative item number uses text search", query: "CR--4", workspaces: []int{workspaceID}},
		{name: "unknown key", query: "CR-999", workspaces: []int{workspaceID}},
		{name: "title search", query: "target", workspaces: []int{workspaceID, otherWorkspaceID}, want: []int{itemID, otherID}},
		{name: "description search", query: "searchable", workspaces: []int{workspaceID}, want: []int{itemID}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			limit := tt.limit
			if limit == 0 {
				limit = 10
			}
			items, err := NewItemRepository(db).SearchLinkableItems(tt.query, tt.workspaces, tt.types, limit)
			if err != nil {
				t.Fatalf("search linkable items: %v", err)
			}
			var got []int
			for _, item := range items {
				got = append(got, item.ID)
			}
			if len(tt.types) == 0 {
				regular, _, err := NewItemRepository(db).Search(tt.query, tt.workspaces, PaginationParams{Limit: 10})
				if err != nil {
					t.Fatalf("regular item search: %v", err)
				}
				var regularIDs []int
				for _, item := range regular {
					regularIDs = append(regularIDs, item.ID)
				}
				slices.Sort(regularIDs)
				linkIDs := slices.Clone(got)
				slices.Sort(linkIDs)
				if !slices.Equal(linkIDs, regularIDs) {
					t.Errorf("link search IDs = %v, regular search IDs = %v", linkIDs, regularIDs)
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("result IDs = %v, want %v", got, tt.want)
			}
		})
	}
}
