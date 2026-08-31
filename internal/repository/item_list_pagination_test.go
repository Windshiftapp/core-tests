//go:build test

package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

func TestFindAllWithDetailsPageContextCursorPaginatesDefaultWorkspaceOrder(t *testing.T) {
	db := newItemListTestDB(t, "item-list-cursor")
	workspaceResult, err := db.ExecWrite(`INSERT INTO workspaces (name, key, description, active) VALUES (?, ?, '', true)`, "Cursor", "CURSOR")
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	workspaceID64, err := workspaceResult.LastInsertId()
	if err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	workspaceID := int(workspaceID64)
	for number, rank := range []string{"0|a1", "0|a2", "0|a3"} {
		if _, err := db.ExecWrite(`
			INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index, created_at, updated_at)
			VALUES (?, ?, ?, '', ?, ?, ?)
		`, workspaceID, number+1, "cursor-"+rank, rank, time.Now().UTC(), time.Now().UTC()); err != nil {
			t.Fatalf("insert %s: %v", rank, err)
		}
	}

	workspaceFilter := &workspaceID
	params := ItemListParams{
		WorkspaceIDs: []int{workspaceID},
		Filters:      ItemFilters{WorkspaceID: workspaceFilter},
		Pagination:   PaginationParams{Limit: 2, CursorMode: true},
	}
	repo := NewItemRepository(db)
	first, err := repo.FindAllWithDetailsPageContext(context.Background(), params)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if first.Total != 3 || len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = total %d items %d next %q, want 3 items and cursor", first.Total, len(first.Items), first.NextCursor)
	}

	params.Pagination.Cursor = first.NextCursor
	second, err := repo.FindAllWithDetailsPageContext(context.Background(), params)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].Title != "cursor-0|a3" || second.NextCursor != "" {
		t.Fatalf("second page = items %#v next %q, want final cursor-0|a3 page", second.Items, second.NextCursor)
	}
}

func TestFindAllWithDetailsPageContextCursorSurvivesGlobalRankMigration(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.DB
	workspaceID := createFracIndexTestWorkspace(t, db)
	for number, rank := range []string{"0|a1", "0|a2", "0|a3", "0|a4"} {
		if _, err := db.ExecWrite(`
			INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index, created_at, updated_at)
			VALUES (?, ?, ?, '', ?, ?, ?)
		`, workspaceID, number+1, "migration-"+rank, rank, time.Now().UTC(), time.Now().UTC()); err != nil {
			t.Fatalf("insert %s: %v", rank, err)
		}
	}

	workspaceFilter := &workspaceID
	params := ItemListParams{
		WorkspaceIDs: []int{workspaceID},
		Filters:      ItemFilters{WorkspaceID: workspaceFilter},
		Pagination:   PaginationParams{Limit: 2, CursorMode: true},
	}
	repo := NewItemRepository(db)
	first, err := repo.FindAllWithDetailsPageContext(context.Background(), params)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Items) != 2 || first.Items[1].Title != "migration-0|a2" || first.NextCursor == "" {
		t.Fatalf("first page = %#v next %q, want through migration-0|a2", first.Items, first.NextCursor)
	}

	// Move the cursor row and all later rows into the target bucket while also
	// assigning fresh balanced payloads. A continuation that trusted the stale
	// token rank would now return already-seen rows or skip unseen rows.
	worker := NewGlobalRankMigrationWorker(db, "cursor-migration", 3, time.Minute)
	result, err := worker.Run(context.Background())
	if err != nil {
		t.Fatalf("migrate cursor frontier: %v", err)
	}
	if result.Completed || result.Migrated != 3 {
		t.Fatalf("migration batch = %+v, want three rows and an active frontier", result)
	}
	decodedCursor, err := decodeItemListCursor(first.NextCursor)
	if err != nil {
		t.Fatalf("decode first-page cursor: %v", err)
	}
	var currentCursorRank string
	if err := db.QueryRow("SELECT frac_index FROM items WHERE id = ?", decodedCursor.ID).Scan(&currentCursorRank); err != nil {
		t.Fatalf("read normalized cursor rank: %v", err)
	}
	if currentCursorRank == decodedCursor.Rank {
		t.Fatalf("migration did not normalize cursor rank %q", currentCursorRank)
	}

	params.Pagination.Cursor = first.NextCursor
	second, err := repo.FindAllWithDetailsPageContext(context.Background(), params)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Items) != 2 || second.Items[0].Title != "migration-0|a3" || second.Items[1].Title != "migration-0|a4" {
		t.Fatalf("second page = %#v, want only rows after the migrated cursor", second.Items)
	}
}

func TestFindAllWithDetailsPageContextCursorSurvivesLowToHighCompletion(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.DB
	workspaceID := createFracIndexTestWorkspace(t, db)
	for number, rank := range []string{"2|a1", "2|a2", "2|a3", "2|a4"} {
		if _, err := db.ExecWrite(`
			INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index, created_at, updated_at)
			VALUES (?, ?, ?, '', ?, ?, ?)
		`, workspaceID, number+1, "low-high-"+rank, rank, time.Now().UTC(), time.Now().UTC()); err != nil {
			t.Fatalf("insert %s: %v", rank, err)
		}
	}
	if _, err := db.Exec("UPDATE global_rank_state SET active_bucket = 2 WHERE id = 1"); err != nil {
		t.Fatalf("set active bucket: %v", err)
	}

	workspaceFilter := &workspaceID
	params := ItemListParams{
		WorkspaceIDs: []int{workspaceID},
		Filters:      ItemFilters{WorkspaceID: workspaceFilter},
		Pagination:   PaginationParams{Limit: 2, CursorMode: true},
	}
	repo := NewItemRepository(db)
	first, err := repo.FindAllWithDetailsPageContext(context.Background(), params)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Items) != 2 || first.Items[1].Title != "low-high-2|a2" || first.NextCursor == "" {
		t.Fatalf("first page = %#v next %q, want through low-high-2|a2", first.Items, first.NextCursor)
	}

	worker := NewGlobalRankMigrationWorker(db, "cursor-low-high", 4, time.Minute)
	result, err := worker.Run(context.Background())
	if err != nil {
		t.Fatalf("complete low-to-high migration: %v", err)
	}
	if !result.Completed || result.State.ActiveBucket != GlobalRankBucket0 {
		t.Fatalf("migration result = %+v, want stable bucket zero", result)
	}

	params.Pagination.Cursor = first.NextCursor
	second, err := repo.FindAllWithDetailsPageContext(context.Background(), params)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Items) != 2 || second.Items[0].Title != "low-high-2|a3" || second.Items[1].Title != "low-high-2|a4" {
		t.Fatalf("second page = %#v, want rows after the pre-migration cursor", second.Items)
	}
}

func TestItemListCursorPredicateUsesWorkspaceRankIndex(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	where, args, err := itemListCursorWhere(itemListCursor{Rank: "0|a2", ID: 2})
	if err != nil {
		t.Fatalf("build cursor predicate: %v", err)
	}
	query := "SELECT i.id FROM items i WHERE i.workspace_id = ?" + where + " ORDER BY i.frac_index ASC LIMIT 50"
	args = append([]interface{}{1}, args...)
	plan := ""
	if tdb.DB.GetDriverName() == "postgres" {
		tx, err := tdb.DB.Begin()
		if err != nil {
			t.Fatalf("begin explain transaction: %v", err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec("SET LOCAL enable_seqscan = off"); err != nil {
			t.Fatalf("disable sequential scan: %v", err)
		}
		rows, err := tx.Query("EXPLAIN "+query, args...)
		if err != nil {
			t.Fatalf("explain cursor query: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatalf("scan PostgreSQL plan: %v", err)
			}
			plan += line + "\n"
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate PostgreSQL plan: %v", err)
		}
	} else {
		rows, err := tdb.DB.Query("EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatalf("explain cursor query: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatalf("scan SQLite plan: %v", err)
			}
			plan += detail + "\n"
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate SQLite plan: %v", err)
		}
	}
	if !strings.Contains(plan, "idx_items_workspace_frac_index") {
		t.Fatalf("cursor query plan does not use workspace/rank index:\n%s", plan)
	}
}

func TestDefaultItemListOrderUsesWorkspaceRankIndexOnSQLite(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	if tdb.DB.GetDriverName() != "sqlite" {
		t.Skip("SQLite query-plan regression")
	}

	repo := NewItemRepository(tdb.GetDatabase())
	query := "SELECT i.id FROM items i WHERE i.workspace_id = ?" + repo.defaultOrderBy() + " LIMIT 51"
	rows, err := tdb.DB.Query("EXPLAIN QUERY PLAN "+query, 1)
	if err != nil {
		t.Fatalf("explain default item list: %v", err)
	}
	defer rows.Close()

	var plan string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan SQLite plan: %v", err)
		}
		plan += detail + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate SQLite plan: %v", err)
	}
	if !strings.Contains(plan, "SEARCH i USING COVERING INDEX idx_items_workspace_frac_index") {
		t.Fatalf("default item-list plan does not seek through workspace/rank index:\n%s", plan)
	}
}

func TestFindAllWithDetailsPageContextIgnoresCursorForCollectionPlan(t *testing.T) {
	db := newItemListTestDB(t, "item-list-collection-cursor")
	workspaceResult, err := db.ExecWrite(`INSERT INTO workspaces (name, key, description, active) VALUES (?, ?, '', true)`, "Collection", "COLLECTION")
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	workspaceID64, err := workspaceResult.LastInsertId()
	if err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	workspaceID := int(workspaceID64)
	for number, title := range []string{"collection-first", "collection-second"} {
		if _, err := db.ExecWrite(`
			INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index, created_at, updated_at)
			VALUES (?, ?, ?, '', ?, ?, ?)
		`, workspaceID, number+1, title, "0|"+string(rune('a'+number)), time.Now().UTC(), time.Now().UTC()); err != nil {
			t.Fatalf("insert %s: %v", title, err)
		}
	}

	result, err := NewItemRepository(db).FindAllWithDetailsPageContext(context.Background(), ItemListParams{
		WorkspaceIDs: []int{workspaceID},
		Filters: ItemFilters{
			QLQuery: "i.title LIKE ?",
			QLArgs:  []interface{}{"collection-%"},
		},
		Pagination: PaginationParams{Limit: 1, Cursor: "not-a-cursor"},
	})
	if err != nil {
		t.Fatalf("collection list with cursor: %v", err)
	}
	if result.Total != 2 || len(result.Items) != 1 || result.Items[0].Title != "collection-first" {
		t.Fatalf("collection result = total %d items %#v next %q, want page/offset behavior", result.Total, result.Items, result.NextCursor)
	}
	if result.NextCursor != "" {
		t.Fatalf("collection next cursor = %q, want empty", result.NextCursor)
	}
}

func TestFindAllWithDetailsOrdersCanonicalRanksAcrossPages(t *testing.T) {
	db := newItemListTestDB(t, "item-list-null-rank-order")
	// Repository-layer setup is intentionally direct here: the API always
	// generates a canonical non-null rank, and the repository preserves that
	// bytewise order across offset pages.
	workspaceResult, err := db.ExecWrite(`INSERT INTO workspaces (name, key, description, active) VALUES (?, ?, '', true)`, "Ranked", "RANKED")
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	workspaceID64, err := workspaceResult.LastInsertId()
	if err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	workspaceID := int(workspaceID64)

	insert := func(number int, title, rank string) {
		t.Helper()
		if _, err := db.ExecWrite(`
			INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index, created_at, updated_at)
			VALUES (?, ?, ?, '', ?, ?, ?)
		`, workspaceID, number, title, rank, time.Now().UTC(), time.Now().UTC()); err != nil {
			t.Fatalf("insert %s: %v", title, err)
		}
	}
	insert(1, "first", "0|a")
	insert(2, "second", "0|b")
	insert(3, "last", "0|c")

	workspaceFilter := &workspaceID
	items, total, err := NewItemRepository(db).FindAllWithDetailsContext(context.Background(), ItemListParams{
		WorkspaceIDs: []int{workspaceID},
		Filters:      ItemFilters{WorkspaceID: workspaceFilter},
		Pagination:   PaginationParams{Limit: 1, Offset: 2},
	})
	if err != nil {
		t.Fatalf("list deep page: %v", err)
	}
	if total != 3 || len(items) != 1 || items[0].Title != "last" {
		t.Fatalf("deep page = total %d items %#v, want total 3 and last", total, items)
	}
}

func TestFindAllWithDetailsRefreshesWorkspaceTotalAfterPostCommitInvalidation(t *testing.T) {
	db := newItemListTestDB(t, "item-list-count-invalidation")
	// This is a repository test, so the minimal production-schema seed avoids
	// coupling the count-cache assertion to HTTP authentication and routing.
	workspaceResult, err := db.ExecWrite(`INSERT INTO workspaces (name, key, description, active) VALUES (?, ?, '', true)`, "Counts", "COUNTS")
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	workspaceID64, err := workspaceResult.LastInsertId()
	if err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	workspaceID := int(workspaceID64)
	if _, err := db.ExecWrite(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index)
		VALUES (?, 1, 'existing', '', '0|a')
	`, workspaceID); err != nil {
		t.Fatalf("insert existing item: %v", err)
	}
	InvalidateItemListCountCache(db, workspaceID)

	repo := NewItemRepository(db)
	workspaceFilter := &workspaceID
	params := ItemListParams{
		WorkspaceIDs: []int{workspaceID},
		Filters:      ItemFilters{WorkspaceID: workspaceFilter},
		Pagination:   PaginationParams{Limit: 50},
	}
	_, total, err := repo.FindAllWithDetailsContext(context.Background(), params)
	if err != nil {
		t.Fatalf("initial list: %v", err)
	}
	if total != 1 {
		t.Fatalf("initial total = %d, want 1", total)
	}

	rank := "0|b"
	err = database.WithTx(db, func(tx database.Tx) error {
		_, err := repo.Create(tx, &models.Item{
			WorkspaceID:         workspaceID,
			WorkspaceItemNumber: 2,
			Title:               "created",
			FracIndex:           &rank,
		})
		return err
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	InvalidateItemListCountCache(db, workspaceID)

	_, total, err = repo.FindAllWithDetailsContext(context.Background(), params)
	if err != nil {
		t.Fatalf("list after create: %v", err)
	}
	if total != 2 {
		t.Fatalf("total after create = %d, want 2", total)
	}
}

func TestItemListPagePlanKeepsCollectionAndFilteredListsOnSharedPlan(t *testing.T) {
	db := newItemListTestDB(t, "item-list-page-plan")
	repo := NewItemRepository(db)
	countFrom := "FROM items i JOIN workspaces w ON i.workspace_id = w.id"

	workspaceID := 42
	plain := ItemListParams{
		WorkspaceIDs: []int{7, workspaceID},
		Filters:      ItemFilters{WorkspaceID: &workspaceID},
	}
	plan := repo.buildItemListPagePlan(plain, countFrom, "WHERE 1=1 AND i.workspace_id IN (?,?) AND i.workspace_id = ?", []interface{}{7, workspaceID, workspaceID})
	if plan.workspaceCountID != workspaceID {
		t.Fatalf("plain workspace count ID = %d, want %d", plan.workspaceCountID, workspaceID)
	}
	if plan.pageFromClause != "FROM items i " || plan.pageWhereClause != "WHERE i.workspace_id = ?" {
		t.Fatalf("plain workspace page plan = %#v, want direct items filter", plan)
	}

	statusID := 3
	filtered := plain
	filtered.Filters.StatusID = &statusID
	filteredPlan := repo.buildItemListPagePlan(filtered, countFrom, "WHERE 1=1 AND i.workspace_id IN (?,?) AND i.workspace_id = ? AND i.status_id = ?", []interface{}{7, workspaceID, workspaceID, statusID})
	if filteredPlan.workspaceCountID != 0 {
		t.Fatalf("filtered workspace count ID = %d, want non-cacheable", filteredPlan.workspaceCountID)
	}
	if filteredPlan.pageFromClause != countFrom || filteredPlan.pageWhereClause == "WHERE i.workspace_id = ?" {
		t.Fatalf("filtered page plan = %#v, want shared filtered plan", filteredPlan)
	}

	collection := plain
	collection.Filters = ItemFilters{WorkspaceID: &workspaceID, QLQuery: "i.title LIKE ?", QLArgs: []interface{}{"collection%"}}
	collectionPlan := repo.buildItemListPagePlan(collection, countFrom, "WHERE 1=1 AND i.workspace_id IN (?,?) AND i.workspace_id = ? AND i.title LIKE ?", []interface{}{7, workspaceID, workspaceID, "collection%"})
	if collectionPlan.workspaceCountID != 0 {
		t.Fatalf("collection count ID = %d, want non-cacheable", collectionPlan.workspaceCountID)
	}
	if collectionPlan.pageFromClause != countFrom {
		t.Fatalf("collection page plan = %#v, want shared filtered plan", collectionPlan)
	}
}
