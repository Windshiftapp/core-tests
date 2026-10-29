//go:build test

package repository

import (
	"slices"
	"testing"

	"windshift/internal/testutils"
)

// TestItemSmartSearchMatchesTextOrKey covers the `search` list filter used by
// the board and list views: it must find title/description text and, when the
// query is a full KEY-NUMBER reference, the item key too.
func TestItemSmartSearchMatchesTextOrKey(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = db.Close() })

	workspaceID := testutils.InsertID(t, db, `INSERT INTO workspaces (name, key, active, is_personal) VALUES ('Smart search', 'SMART', true, false)`)
	otherWorkspaceID := testutils.InsertID(t, db, `INSERT INTO workspaces (name, key, active, is_personal) VALUES ('Other smart', 'OTHER', true, false)`)

	titleItemID := testutils.InsertID(t, db, `INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index) VALUES (?, 1, 'Login bug on mobile', '', ?)`, workspaceID, testutils.NextTestFracIndex())
	descriptionItemID := testutils.InsertID(t, db, `INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index) VALUES (?, 2, 'Unrelated title', 'searchable description phrase', ?)`, workspaceID, testutils.NextTestFracIndex())
	keyItemID := testutils.InsertID(t, db, `INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index) VALUES (?, 3, 'Keyed item', '', ?)`, workspaceID, testutils.NextTestFracIndex())
	otherKeyItemID := testutils.InsertID(t, db, `INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index) VALUES (?, 3, 'Other keyed item', '', ?)`, otherWorkspaceID, testutils.NextTestFracIndex())
	// A prose title that happens to end in a dash-number must still match as
	// text even though it also parses as a key-shaped query (WI-1417).
	proseKeyItemID := testutils.InsertID(t, db, `INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index) VALUES (?, 4, 'release-2026-09-18 checklist', '', ?)`, workspaceID, testutils.NextTestFracIndex())

	for _, tt := range []struct {
		name  string
		query string
		want  []int
	}{
		{name: "matches title text", query: "login bug", want: []int{titleItemID}},
		{name: "matches description text", query: "searchable description", want: []int{descriptionItemID}},
		{name: "matches full key", query: "SMART-3", want: []int{keyItemID}},
		{name: "matches lowercase key", query: "smart-3", want: []int{keyItemID}},
		{name: "matches key in other workspace", query: "OTHER-3", want: []int{otherKeyItemID}},
		{name: "key-shaped prose matches text", query: "release-2026-09-18", want: []int{proseKeyItemID}},
		{name: "unknown key matches nothing", query: "SMART-999"},
		{name: "no match returns nothing", query: "nothing here"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			items, _, err := NewItemRepository(db).FindAllWithDetailsContext(t.Context(), ItemListParams{
				WorkspaceIDs: []int{workspaceID, otherWorkspaceID},
				Filters:      ItemFilters{SearchQuery: tt.query},
				Pagination:   PaginationParams{Limit: 50},
			})
			if err != nil {
				t.Fatalf("smart search: %v", err)
			}
			got := make([]int, 0, len(items))
			for _, item := range items {
				got = append(got, item.ID)
			}
			slices.Sort(got)
			want := slices.Clone(tt.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("smart search %q IDs = %v, want %v", tt.query, got, want)
			}
		})
	}
}
