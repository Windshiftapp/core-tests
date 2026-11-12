//go:build test

package repository

import (
	"context"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// These tests guard the WI-1910 bound on filtered/collection/QL list totals.
// The count runs over the full filter join set on every request, so it is
// capped instead of aggregating the whole matching set. Results below the cap
// stay exact; results at the cap report the cap.

// insertBulkItems inserts count items into workspaceID in one statement. The
// recursive CTE keeps the fixture cheap even when count exceeds the cap.
func insertBulkItems(t *testing.T, db database.Database, workspaceID, statusID, count int) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := db.ExecWrite(`
		WITH RECURSIVE seq(n) AS (
			SELECT 1
			UNION ALL
			SELECT n + 1 FROM seq WHERE n < ?
		)
		INSERT INTO items (
			workspace_id, workspace_item_number, title, description,
			status_id, frac_index, created_at, updated_at
		)
		SELECT ?, n, 'bulk item ' || n, '', ?, '0|' || n, ?, ? FROM seq
	`, count, workspaceID, statusID, now, now); err != nil {
		t.Fatalf("insert %d bulk items: %v", count, err)
	}
}

func filteredListParams(workspaceID, statusID int, limit int) ItemListParams {
	return ItemListParams{
		WorkspaceIDs: []int{workspaceID},
		Filters:      ItemFilters{StatusID: &statusID},
		Pagination:   PaginationParams{Limit: limit},
	}
}

func TestFilteredItemListCountStaysExactBelowBound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	const itemCount = 25
	insertBulkItems(t, db, seed.WorkspaceID, seed.StatusID, itemCount)

	repo := NewItemRepository(db)
	page, err := repo.FindAllWithDetailsPageContext(
		context.Background(),
		filteredListParams(seed.WorkspaceID, seed.StatusID, 10),
	)
	if err != nil {
		t.Fatalf("list filtered items: %v", err)
	}
	if page.Total != itemCount {
		t.Fatalf("filtered total = %d, want exact %d below the cap", page.Total, itemCount)
	}
	if len(page.Items) != 10 {
		t.Fatalf("filtered page items = %d, want 10", len(page.Items))
	}
}

func TestFilteredItemListCountIsBounded(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	insertBulkItems(t, db, seed.WorkspaceID, seed.StatusID, boundedItemListCountLimit+1)

	counting := testutils.CountQueries(db)
	repo := NewItemRepository(counting)
	page, err := repo.FindAllWithDetailsPageContext(
		context.Background(),
		filteredListParams(seed.WorkspaceID, seed.StatusID, 10),
	)
	if err != nil {
		t.Fatalf("list filtered items: %v", err)
	}
	if page.Total != boundedItemListCountLimit {
		t.Fatalf("filtered total = %d, want capped %d", page.Total, boundedItemListCountLimit)
	}
	if len(page.Items) != 10 {
		t.Fatalf("filtered page items = %d, want 10", len(page.Items))
	}
	// The bound is only useful if the count query carries it. A missing LIMIT
	// means the aggregate ran over every matching row.
	if got := counting.CountQueriesContaining("bounded_item_count"); got != 1 {
		t.Fatalf("bounded count queries = %d, want 1", got)
	}
}

func TestFilteredItemListCountBoundDoesNotAffectPageSelection(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	insertBulkItems(t, db, seed.WorkspaceID, seed.StatusID, boundedItemListCountLimit+5)

	repo := NewItemRepository(db)
	params := filteredListParams(seed.WorkspaceID, seed.StatusID, 5)
	params.Pagination.Offset = boundedItemListCountLimit

	page, err := repo.FindAllWithDetailsPageContext(context.Background(), params)
	if err != nil {
		t.Fatalf("list deep filtered page: %v", err)
	}
	if len(page.Items) != 5 {
		t.Fatalf("deep filtered page items = %d, want 5 beyond the count cap", len(page.Items))
	}
}
