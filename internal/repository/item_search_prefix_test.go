//go:build test

package repository

import (
	"slices"
	"testing"

	"windshift/internal/testutils"
)

// TestItemTextSearchUsesTokenPrefixes covers the WI-413 indexed search
// contract: a query matches whole words by prefix across title and
// description, every term must match, and the FTS index stays in sync with
// items. Count and page results come from the same predicate, so Total must
// equal the number of returned IDs.
func TestItemTextSearchUsesTokenPrefixes(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = db.Close() })

	workspaceID := testutils.InsertID(t, db, `INSERT INTO workspaces (name, key, active, is_personal) VALUES ('Prefix search', 'PREFIX', true, false)`)
	titleItemID := testutils.InsertID(t, db, `INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index) VALUES (?, 1, 'We changed the Outlook version', '', ?)`, workspaceID, testutils.NextTestFracIndex())
	descriptionItemID := testutils.InsertID(t, db, `INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index) VALUES (?, 2, 'Unrelated title', 'The Acrobat reader crashes on start', ?)`, workspaceID, testutils.NextTestFracIndex())
	otherItemID := testutils.InsertID(t, db, `INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index) VALUES (?, 3, 'Billing question', 'nothing relevant', ?)`, workspaceID, testutils.NextTestFracIndex())

	search := func(query string) ([]int, int) {
		t.Helper()
		items, total, err := NewItemRepository(db).FindAllWithDetailsContext(t.Context(), ItemListParams{
			WorkspaceIDs: []int{workspaceID},
			Filters:      ItemFilters{TextQuery: query},
			Pagination:   PaginationParams{Limit: 50},
		})
		if err != nil {
			t.Fatalf("search %q: %v", query, err)
		}
		got := make([]int, 0, len(items))
		for _, item := range items {
			got = append(got, item.ID)
		}
		slices.Sort(got)
		return got, total
	}

	for _, tt := range []struct {
		name  string
		query string
		want  []int
	}{
		{name: "prefix matches title", query: "Outlo", want: []int{titleItemID}},
		{name: "prefix is case insensitive", query: "outlo", want: []int{titleItemID}},
		{name: "prefix matches description", query: "Acro", want: []int{descriptionItemID}},
		{name: "all terms must match", query: "outlo versio", want: []int{titleItemID}},
		{name: "one unmatched term excludes", query: "outlo zebra"},
		{name: "mid word does not match", query: "tlook"},
		{name: "unrelated query matches nothing", query: "zzzz"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, total := search(tt.query)
			want := slices.Clone(tt.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("search %q IDs = %v, want %v", tt.query, got, want)
			}
			if total != len(want) {
				t.Fatalf("search %q Total = %d, want %d", tt.query, total, len(want))
			}
		})
	}

	// The index follows title updates: the new word matches and the old word
	// no longer does.
	if _, err := db.ExecWrite(`UPDATE items SET title = 'Printer queue stuck' WHERE id = ?`, titleItemID); err != nil {
		t.Fatalf("update title: %v", err)
	}
	if got, _ := search("printer"); !slices.Equal(got, []int{titleItemID}) {
		t.Fatalf("search after title update = %v, want [%d]", got, titleItemID)
	}
	if got, _ := search("outlook"); len(got) != 0 {
		t.Fatalf("search for old title after update = %v, want none", got)
	}
	// Updating a non-indexed column must not disturb the index.
	if _, err := db.ExecWrite(`UPDATE items SET last_active_at = CURRENT_TIMESTAMP WHERE id = ?`, titleItemID); err != nil {
		t.Fatalf("update last_active_at: %v", err)
	}
	if got, _ := search("printer"); !slices.Equal(got, []int{titleItemID}) {
		t.Fatalf("search after unrelated update = %v, want [%d]", got, titleItemID)
	}
	// Deleting a row removes it from the index.
	if _, err := db.ExecWrite(`DELETE FROM items WHERE id = ?`, otherItemID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, _ := search("billing"); len(got) != 0 {
		t.Fatalf("search after delete = %v, want none", got)
	}
}

// TestItemTextSearchFallsBackToLike covers queries the FTS path cannot serve:
// terms shorter than the minimum prefix and scripts neither engine segments.
// They must still match through the original LIKE scan instead of returning
// nothing.
func TestItemTextSearchFallsBackToLike(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = db.Close() })

	workspaceID := testutils.InsertID(t, db, `INSERT INTO workspaces (name, key, active, is_personal) VALUES ('Fallback search', 'FALLBK', true, false)`)
	shortItemID := testutils.InsertID(t, db, `INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index) VALUES (?, 1, 'Zebra crossing', '', ?)`, workspaceID, testutils.NextTestFracIndex())
	cjkItemID := testutils.InsertID(t, db, `INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index) VALUES (?, 2, 'Mixed script', '我们改变了Outlook版本', ?)`, workspaceID, testutils.NextTestFracIndex())

	search := func(query string) []int {
		t.Helper()
		items, _, err := NewItemRepository(db).FindAllWithDetailsContext(t.Context(), ItemListParams{
			WorkspaceIDs: []int{workspaceID},
			Filters:      ItemFilters{TextQuery: query},
			Pagination:   PaginationParams{Limit: 50},
		})
		if err != nil {
			t.Fatalf("search %q: %v", query, err)
		}
		got := make([]int, 0, len(items))
		for _, item := range items {
			got = append(got, item.ID)
		}
		slices.Sort(got)
		return got
	}

	if got := search("z"); !slices.Equal(got, []int{shortItemID}) {
		t.Fatalf("one character search = %v, want [%d]", got, shortItemID)
	}
	if got := search("版本"); !slices.Equal(got, []int{cjkItemID}) {
		t.Fatalf("cjk search = %v, want [%d]", got, cjkItemID)
	}
}

// TestItemTextSearchFoldsAccents verifies accented words match an unaccented
// query. SQLite's unicode61 tokenizer folds accents; PostgreSQL folds them only
// when the unaccent extension is installable, so the test skips when it is not.
func TestItemTextSearchFoldsAccents(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = db.Close() })

	if db.Engine == "postgres" {
		var installed int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pg_extension WHERE extname = 'unaccent'`).Scan(&installed); err != nil {
			t.Fatalf("check unaccent: %v", err)
		}
		if installed == 0 {
			t.Skip("unaccent extension is not installed")
		}
	}

	workspaceID := testutils.InsertID(t, db, `INSERT INTO workspaces (name, key, active, is_personal) VALUES ('Accent search', 'ACCENT', true, false)`)
	itemID := testutils.InsertID(t, db, `INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index) VALUES (?, 1, 'Müller hat geantwortet', '', ?)`, workspaceID, testutils.NextTestFracIndex())

	items, _, err := NewItemRepository(db).FindAllWithDetailsContext(t.Context(), ItemListParams{
		WorkspaceIDs: []int{workspaceID},
		Filters:      ItemFilters{TextQuery: "muller"},
		Pagination:   PaginationParams{Limit: 50},
	})
	if err != nil {
		t.Fatalf("accent search: %v", err)
	}
	if len(items) != 1 || items[0].ID != itemID {
		t.Fatalf("accent search returned %d items, want item %d", len(items), itemID)
	}
}
