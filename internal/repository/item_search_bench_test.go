//go:build test

package repository

import (
	"context"
	"fmt"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// BenchmarkItemTextSearch measures the indexed token-prefix search over a
// 100k-row items table. Run with:
//
//	../core-tests/overlay.sh . -- -tags=test -run '^$' -bench BenchmarkItemTextSearch -benchtime 5x ./internal/repository/...
func BenchmarkItemTextSearch(b *testing.B) {
	db := newItemListTestDB(b, "item-search-bench")
	workspaceID := createItemSearchBenchWorkspace(b, db)
	seedItemSearchBenchItems(b, db, workspaceID, 100_000)

	repo := NewItemRepository(db)
	params := ItemListParams{
		WorkspaceIDs: []int{workspaceID},
		Filters:      ItemFilters{TextQuery: "outlo versio"},
		Pagination:   PaginationParams{Limit: 20},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := repo.FindAllWithDetailsContext(context.Background(), params); err != nil {
			b.Fatalf("search: %v", err)
		}
	}
}

func createItemSearchBenchWorkspace(b *testing.B, db database.Database) int {
	b.Helper()
	result, err := db.ExecWrite(`INSERT INTO workspaces (name, key, description, active) VALUES ('Item search bench', 'ISBENCH', '', true)`)
	if err != nil {
		b.Fatalf("insert workspace: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		b.Fatalf("workspace id: %v", err)
	}
	return int(id)
}

func seedItemSearchBenchItems(b *testing.B, db database.Database, workspaceID, count int) {
	b.Helper()
	tx, err := db.Begin()
	if err != nil {
		b.Fatalf("begin seed transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		b.Fatalf("prepare seed insert: %v", err)
	}
	defer func() { _ = stmt.Close() }()
	for i := range count {
		description := "the quick brown fox jumps over the lazy dog"
		if i%1000 == 0 {
			description = "we changed the outlook version for this item"
		}
		if _, err := stmt.Exec(workspaceID, i+1, fmt.Sprintf("Item %d routine work", i), description, testutils.NextTestFracIndex()); err != nil {
			b.Fatalf("insert item %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatalf("commit seed transaction: %v", err)
	}
}
