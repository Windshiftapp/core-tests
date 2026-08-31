package repository

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

func TestFindAllWithDetailsContextHonorsCancellation(t *testing.T) {
	db := newItemListTestDB(t, "item-list-context")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := NewItemRepository(db).FindAllWithDetailsContext(ctx, ItemListParams{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestMeasuredItemReadsHonorCancellation(t *testing.T) {
	db := newItemListTestDB(t, "measured-item-read-context")
	repo := NewItemRepository(db)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "search",
			run: func() error {
				_, _, err := repo.SearchContext(ctx, "needle", []int{1}, PaginationParams{})
				return err
			},
		},
		{
			name: "backlog statuses",
			run: func() error {
				_, err := repo.GetBacklogStatusIDsContext(ctx, 0)
				return err
			},
		},
		{
			name: "ancestors",
			run: func() error {
				_, err := repo.GetAncestorsForHierarchyContext(ctx, 1, 30)
				return err
			},
		},
		{
			name: "descendants",
			run: func() error {
				_, err := repo.GetDescendantsWithMaxDepthContext(ctx, 1, 30)
				return err
			},
		},
		{
			name: "children",
			run: func() error {
				_, err := repo.GetChildrenContext(ctx, 1)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
		})
	}
}

func TestCanceledQueryReturnsPoolConnection(t *testing.T) {
	db := newItemListTestDB(t, "canceled-query-connection")
	sqlDB := db.GetDB()
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		var sum int64
		done <- db.QueryRowContext(ctx, `
			SELECT SUM(value) FROM (
				WITH RECURSIVE counter(value) AS (
					VALUES(1)
					UNION ALL
					SELECT value + 1 FROM counter WHERE value < 1000000000
				)
				SELECT value FROM counter
			)
		`).Scan(&sum)
	}()

	acquireDeadline := time.NewTimer(time.Second)
	defer acquireDeadline.Stop()
	acquireTicker := time.NewTicker(time.Millisecond)
	defer acquireTicker.Stop()
	for sqlDB.Stats().InUse == 0 {
		select {
		case <-acquireTicker.C:
		case <-acquireDeadline.C:
			t.Fatal("slow query did not acquire the only pool connection")
		}
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("query error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled query did not return promptly")
	}

	if stats := sqlDB.Stats(); stats.InUse != 0 {
		t.Fatalf("pool in-use connections = %d after cancellation, want 0", stats.InUse)
	}
}

func TestFindAllWithDetailsUsesLeanCountQuery(t *testing.T) {
	db := newItemListTestDB(t, "item-list-lean-count")

	items, total, err := NewItemRepository(db).FindAllWithDetailsContext(context.Background(), ItemListParams{})
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if total != len(items) {
		t.Fatalf("total = %d, items = %d", total, len(items))
	}
}

func TestFindAllWithDetailsUsesLatestCurrentStatusTransition(t *testing.T) {
	db := newItemListTestDB(t, "item-list-current-status-transition")

	var indexCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_item_history_current_status_latest'`).Scan(&indexCount); err != nil {
		t.Fatalf("check status history index: %v", err)
	}
	if indexCount != 1 {
		t.Fatalf("status history index count = %d, want 1", indexCount)
	}

	workspaceResult, err := db.ExecWrite(`INSERT INTO workspaces (name, key, description, active) VALUES (?, ?, '', true)`, "List Test", "LIST")
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	workspaceID64, err := workspaceResult.LastInsertId()
	if err != nil {
		t.Fatalf("workspace LastInsertId: %v", err)
	}
	workspaceID := int(workspaceID64)

	userResult, err := db.ExecWrite(`INSERT INTO users (email, username, first_name, last_name) VALUES (?, ?, ?, ?)`, "list@example.com", "list-test", "List", "Test")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, err := userResult.LastInsertId()
	if err != nil {
		t.Fatalf("user LastInsertId: %v", err)
	}

	var openStatusID, doneStatusID int
	if err := db.QueryRow(`SELECT id FROM statuses WHERE name = 'Open'`).Scan(&openStatusID); err != nil {
		t.Fatalf("load Open status: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM statuses WHERE name = 'Done'`).Scan(&doneStatusID); err != nil {
		t.Fatalf("load Done status: %v", err)
	}

	createdAt := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	insertItem := func(number, statusID int, title string) int {
		t.Helper()
		result, err := db.ExecWrite(`
			INSERT INTO items (workspace_id, workspace_item_number, title, description, status_id, frac_index, created_at, updated_at)
			VALUES (?, ?, ?, '', ?, ?, ?, ?)
		`, workspaceID, number, title, statusID, testutils.NextTestFracIndex(), createdAt.Format(time.RFC3339Nano), createdAt.Format(time.RFC3339Nano))
		if err != nil {
			t.Fatalf("insert item %s: %v", title, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("item LastInsertId: %v", err)
		}
		return int(id)
	}

	doneItemID := insertItem(1, doneStatusID, "Completed item")
	_ = insertItem(2, openStatusID, "Open item")
	oldTransition := createdAt.Add(24 * time.Hour)
	latestTransition := createdAt.Add(72 * time.Hour)
	for _, changedAt := range []time.Time{oldTransition, latestTransition} {
		if _, err := db.ExecWrite(`
			INSERT INTO item_history (item_id, user_id, field_name, old_value, new_value, changed_at)
			VALUES (?, ?, 'status_id', ?, ?, ?)
		`, doneItemID, userID, strconv.Itoa(openStatusID), strconv.Itoa(doneStatusID), changedAt.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("insert status history: %v", err)
		}
	}
	// A newer unrelated change must not become status_since.
	if _, err := db.ExecWrite(`
		INSERT INTO item_history (item_id, user_id, field_name, old_value, new_value, changed_at)
		VALUES (?, ?, 'title', 'before', 'after', ?)
	`, doneItemID, userID, latestTransition.Add(time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert unrelated history: %v", err)
	}

	completedSince := latestTransition.Add(-time.Minute).Format(time.RFC3339Nano)
	items, total, err := NewItemRepository(db).FindAllWithDetailsContext(context.Background(), ItemListParams{
		WorkspaceIDs: []int{workspaceID},
		Filters: ItemFilters{
			CompletedSince: &completedSince,
		},
	})
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("included item count = %d/%d, want 2/2", total, len(items))
	}
	for _, item := range items {
		if item.ID == doneItemID {
			if item.StatusSince == nil || !item.StatusSince.Equal(latestTransition) {
				t.Fatalf("completed item status_since = %v, want %v", item.StatusSince, latestTransition)
			}
		}
	}

	completedSince = latestTransition.Add(time.Minute).Format(time.RFC3339Nano)
	items, total, err = NewItemRepository(db).FindAllWithDetailsContext(context.Background(), ItemListParams{
		WorkspaceIDs: []int{workspaceID},
		Filters: ItemFilters{
			CompletedSince: &completedSince,
		},
	})
	if err != nil {
		t.Fatalf("list items after completion cutoff: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].Title != "Open item" {
		t.Fatalf("items after completion cutoff = %#v (total %d), want only open item", items, total)
	}

	planRows, err := db.Query(`
		EXPLAIN QUERY PLAN
		SELECT ih.changed_at
		FROM item_history ih
		WHERE ih.item_id = ? AND ih.field_name = 'status_id' AND ih.new_value = ?
		ORDER BY ih.changed_at DESC
		LIMIT 1
	`, doneItemID, strconv.Itoa(doneStatusID))
	if err != nil {
		t.Fatalf("explain status history lookup: %v", err)
	}
	defer func() { _ = planRows.Close() }()
	usedIndex := false
	for planRows.Next() {
		var id, parent, unused int
		var detail string
		if err := planRows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		if strings.Contains(detail, "idx_item_history_current_status_latest") {
			usedIndex = true
		}
	}
	if err := planRows.Err(); err != nil {
		t.Fatalf("iterate query plan: %v", err)
	}
	if !usedIndex {
		t.Fatal("latest current-status lookup did not use idx_item_history_current_status_latest")
	}
}

func BenchmarkCurrentStatusTransitionLookup(b *testing.B) {
	db := newItemListTestDB(b, "item-list-status-benchmark")

	workspaceResult, err := db.ExecWrite(`INSERT INTO workspaces (name, key, description, active) VALUES ('Benchmark', 'BENCH', '', true)`)
	if err != nil {
		b.Fatalf("insert workspace: %v", err)
	}
	workspaceID, err := workspaceResult.LastInsertId()
	if err != nil {
		b.Fatalf("workspace LastInsertId: %v", err)
	}
	userResult, err := db.ExecWrite(`INSERT INTO users (email, username, first_name, last_name) VALUES ('bench@example.com', 'bench', 'Bench', 'Mark')`)
	if err != nil {
		b.Fatalf("insert user: %v", err)
	}
	userID, err := userResult.LastInsertId()
	if err != nil {
		b.Fatalf("user LastInsertId: %v", err)
	}
	var doneStatusID int
	if err := db.QueryRow(`SELECT id FROM statuses WHERE name = 'Done'`).Scan(&doneStatusID); err != nil {
		b.Fatalf("load Done status: %v", err)
	}
	itemResult, err := db.ExecWrite(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, status_id, frac_index)
		VALUES (?, 1, 'Benchmark item', '', ?, ?)
	`, workspaceID, doneStatusID, testutils.NextTestFracIndex())
	if err != nil {
		b.Fatalf("insert item: %v", err)
	}
	itemID, err := itemResult.LastInsertId()
	if err != nil {
		b.Fatalf("item LastInsertId: %v", err)
	}

	const historyRows = 10_000
	err = database.WithTx(db, func(tx database.Tx) error {
		for i := range historyRows {
			fieldName := "title"
			newValue := "updated"
			if i%1000 == 0 {
				fieldName = "status_id"
				newValue = strconv.Itoa(doneStatusID)
			}
			if _, err := tx.Exec(`
				INSERT INTO item_history (item_id, user_id, field_name, old_value, new_value, changed_at)
				VALUES (?, ?, ?, '', ?, ?)
			`, itemID, userID, fieldName, newValue, time.Unix(int64(i), 0).UTC().Format(time.RFC3339Nano)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatalf("seed item history: %v", err)
	}

	bench := func(b *testing.B, query string) {
		b.Helper()
		b.ReportAllocs()
		b.ReportMetric(historyRows, "history-rows")
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				var changedAt string
				if err := db.QueryRow(query, itemID, strconv.Itoa(doneStatusID)).Scan(&changedAt); err != nil {
					b.Fatalf("lookup current status transition: %v", err)
				}
			}
		})
	}

	b.Run("legacy_history_index_parallel", func(b *testing.B) {
		bench(b, `
			SELECT MAX(ih.changed_at)
			FROM item_history ih INDEXED BY idx_item_history_item_id_changed_at
			WHERE ih.item_id = ? AND ih.field_name = 'status_id' AND ih.new_value = ?
		`)
	})
	b.Run("current_status_index_parallel", func(b *testing.B) {
		bench(b, `
			SELECT ih.changed_at
			FROM item_history ih INDEXED BY idx_item_history_current_status_latest
			WHERE ih.item_id = ? AND ih.field_name = 'status_id' AND ih.new_value = ?
			ORDER BY ih.changed_at DESC
			LIMIT 1
		`)
	})
}

func newItemListTestDB(t testing.TB, name string) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDBWithPoolSizes("file:"+name+"?mode=memory&cache=shared", 2, 1)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize database: %v", err)
	}
	return db
}
