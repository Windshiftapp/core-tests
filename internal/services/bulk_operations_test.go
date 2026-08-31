package services

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/database"
)

type bulkFixture struct {
	db            database.Database
	userID        int
	workspaceID   int
	sourceID      int
	targetID      int
	incompleteIDs []int
	completedItem int
}

func newBulkTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "bulk-operations.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return db
}

func bulkInsertID(t *testing.T, db database.Database, query string, args ...any) int {
	t.Helper()
	result, err := db.ExecWrite(query, args...)
	if err != nil {
		t.Fatalf("fixture insert: %v\nquery: %s", err, query)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	return int(id)
}

func seedBulkFixture(t *testing.T, incompleteCount int) bulkFixture {
	t.Helper()
	db := newBulkTestDB(t)
	userID := bulkInsertID(t, db, `
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('bulk@example.test', 'bulk-user', 'Bulk', 'User')
	`)
	workspaceID := bulkInsertID(t, db, `
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Bulk workspace', 'BLK', '', true, false)
	`)
	categoryOpen := bulkInsertID(t, db, `
		INSERT INTO status_categories (name, color, description, is_completed)
		VALUES ('Bulk open', '#123456', '', false)
	`)
	categoryDone := bulkInsertID(t, db, `
		INSERT INTO status_categories (name, color, description, is_completed)
		VALUES ('Bulk done', '#654321', '', true)
	`)
	openStatus := bulkInsertID(t, db, `INSERT INTO statuses (name, description, category_id) VALUES ('Bulk Open', '', ?)`, categoryOpen)
	doneStatus := bulkInsertID(t, db, `INSERT INTO statuses (name, description, category_id) VALUES ('Bulk Done', '', ?)`, categoryDone)
	sourceID := bulkInsertID(t, db, `
		INSERT INTO iterations (name, description, start_date, end_date, status, is_global, workspace_id)
		VALUES ('Source iteration', '', '2026-07-01', '2026-07-14', 'active', false, ?)
	`, workspaceID)
	targetID := bulkInsertID(t, db, `
		INSERT INTO iterations (name, description, start_date, end_date, status, is_global, workspace_id)
		VALUES ('Target iteration', '', '2026-07-15', '2026-07-28', 'planned', false, ?)
	`, workspaceID)

	// Create items through the production path. CreatorID is deliberately nil
	// so CreateItem does not enqueue async creation-history rows: the tests
	// below assert exact item_history counts produced by the bulk/complete
	// operation under test, not by fixture setup.
	incompleteIDs := make([]int, incompleteCount)
	for i := range incompleteCount {
		created, err := CreateItem(db, ItemCreationParams{
			WorkspaceID: workspaceID,
			Title:       fmt.Sprintf("Bulk item %d", i+1),
			StatusID:    &openStatus,
			IterationID: &sourceID,
		})
		if err != nil {
			t.Fatalf("insert bulk item: %v", err)
		}
		incompleteIDs[i] = int(created)
	}
	completedItem64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: workspaceID,
		Title:       "Already done",
		StatusID:    &doneStatus,
		IterationID: &sourceID,
	})
	if err != nil {
		t.Fatalf("insert completed bulk item: %v", err)
	}
	completedItem := int(completedItem64)
	return bulkFixture{
		db: db, userID: userID, workspaceID: workspaceID, sourceID: sourceID,
		targetID: targetID, incompleteIDs: incompleteIDs, completedItem: completedItem,
	}
}

func TestBulkUpdateItemsAtomicFlexiblePatchAndIdempotentRetry(t *testing.T) {
	fixture := seedBulkFixture(t, 3)
	var permissionCalls atomic.Int64
	service := NewItemUpdateService(fixture.db)
	request := BulkUpdateItemsRequest{
		ItemIDs: fixture.incompleteIDs,
		Fields: map[string]any{
			"title":            "Bulk edited",
			"story_points":     8,
			"estimate_minutes": 90,
		},
		UserID: fixture.userID,
		AuthorizeWorkspace: func(workspaceID int) (bool, error) {
			permissionCalls.Add(1)
			return workspaceID == fixture.workspaceID, nil
		},
	}

	result, err := service.BulkUpdateItems(context.Background(), request)
	if err != nil {
		t.Fatalf("BulkUpdateItems: %v", err)
	}
	if result.UpdatedCount != 3 || result.UnchangedCount != 0 || len(result.Results) != 3 {
		t.Fatalf("bulk result = updated:%d unchanged:%d results:%d, want 3/0/3",
			result.UpdatedCount, result.UnchangedCount, len(result.Results))
	}
	if permissionCalls.Load() != 1 {
		t.Fatalf("workspace permission calls = %d, want 1", permissionCalls.Load())
	}
	var changedItems, historyRows int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM items WHERE title = 'Bulk edited' AND story_points = 8 AND estimate_minutes = 90`).Scan(&changedItems); err != nil {
		t.Fatalf("count changed items: %v", err)
	}
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM item_history WHERE item_id IN (?, ?, ?)`,
		fixture.incompleteIDs[0], fixture.incompleteIDs[1], fixture.incompleteIDs[2]).Scan(&historyRows); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if changedItems != 3 || historyRows != 9 {
		t.Fatalf("persisted changes = items:%d history:%d, want 3/9", changedItems, historyRows)
	}

	retry, err := service.BulkUpdateItems(context.Background(), request)
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if retry.UpdatedCount != 0 || retry.UnchangedCount != 3 || len(retry.Results) != 0 {
		t.Fatalf("retry result = updated:%d unchanged:%d results:%d, want 0/3/0",
			retry.UpdatedCount, retry.UnchangedCount, len(retry.Results))
	}
	var retryHistoryRows int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM item_history WHERE item_id IN (?, ?, ?)`,
		fixture.incompleteIDs[0], fixture.incompleteIDs[1], fixture.incompleteIDs[2]).Scan(&retryHistoryRows); err != nil {
		t.Fatalf("count retry history: %v", err)
	}
	if retryHistoryRows != historyRows {
		t.Fatalf("retry added history rows: before=%d after=%d", historyRows, retryHistoryRows)
	}
}

func TestBulkUpdateItemsValidationAndPermissionFailuresRollback(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		fixture := seedBulkFixture(t, 2)
		_, err := NewItemUpdateService(fixture.db).BulkUpdateItems(context.Background(), BulkUpdateItemsRequest{
			ItemIDs:            fixture.incompleteIDs,
			Fields:             map[string]any{"title": "   "},
			UserID:             fixture.userID,
			AuthorizeWorkspace: func(int) (bool, error) { return true, nil },
		})
		if !IsBulkItemValidationError(err) {
			t.Fatalf("error = %v, want validation error", err)
		}
		assertBulkTitlesUnchanged(t, fixture)
	})

	t.Run("permission", func(t *testing.T) {
		fixture := seedBulkFixture(t, 2)
		_, err := NewItemUpdateService(fixture.db).BulkUpdateItems(context.Background(), BulkUpdateItemsRequest{
			ItemIDs:            fixture.incompleteIDs,
			Fields:             map[string]any{"title": "Forbidden edit"},
			UserID:             fixture.userID,
			AuthorizeWorkspace: func(int) (bool, error) { return false, nil },
		})
		if !errors.Is(err, ErrBulkItemForbidden) {
			t.Fatalf("error = %v, want ErrBulkItemForbidden", err)
		}
		assertBulkTitlesUnchanged(t, fixture)
	})

	t.Run("field allowlist", func(t *testing.T) {
		fixture := seedBulkFixture(t, 1)
		_, err := NewItemUpdateService(fixture.db).BulkUpdateItems(context.Background(), BulkUpdateItemsRequest{
			ItemIDs:            fixture.incompleteIDs,
			Fields:             map[string]any{"status_id": 1},
			UserID:             fixture.userID,
			AuthorizeWorkspace: func(int) (bool, error) { return true, nil },
		})
		if !IsBulkItemFieldError(err) {
			t.Fatalf("error = %v, want bulk field error", err)
		}
	})
}

func TestBulkPatchItemsAppliesDistinctPatchesAtomically(t *testing.T) {
	fixture := seedBulkFixture(t, 3)
	service := NewItemUpdateService(fixture.db)
	request := BulkPatchItemsRequest{
		Patches: []BulkItemPatch{
			{ItemID: fixture.incompleteIDs[0], Fields: map[string]any{"start_date": "2026-08-10", "end_date": "2026-08-20"}},
			{ItemID: fixture.incompleteIDs[1], Fields: map[string]any{"start_date": "2026-08-12", "end_date": "2026-08-18"}},
		},
		UserID:             fixture.userID,
		AuthorizeWorkspace: func(workspaceID int) (bool, error) { return workspaceID == fixture.workspaceID, nil },
	}

	result, err := service.BulkPatchItems(context.Background(), request)
	if err != nil {
		t.Fatalf("BulkPatchItems: %v", err)
	}
	if result.UpdatedCount != 2 || result.UnchangedCount != 0 {
		t.Fatalf("result = updated:%d unchanged:%d, want 2/0", result.UpdatedCount, result.UnchangedCount)
	}

	for i, want := range []struct{ start, end string }{
		{"2026-08-10", "2026-08-20"},
		{"2026-08-12", "2026-08-18"},
	} {
		var start, end string
		if err := fixture.db.QueryRow(`SELECT start_date, end_date FROM items WHERE id = ?`, fixture.incompleteIDs[i]).Scan(&start, &end); err != nil {
			t.Fatalf("load patched item %d: %v", i, err)
		}
		if !strings.HasPrefix(start, want.start) || !strings.HasPrefix(end, want.end) {
			t.Fatalf("item %d dates = %s..%s, want %s..%s", i, start, end, want.start, want.end)
		}
	}
}

func TestBulkPatchItemsValidationFailureRollsBackEveryPatch(t *testing.T) {
	fixture := seedBulkFixture(t, 2)
	_, err := NewItemUpdateService(fixture.db).BulkPatchItems(context.Background(), BulkPatchItemsRequest{
		Patches: []BulkItemPatch{
			{ItemID: fixture.incompleteIDs[0], Fields: map[string]any{"start_date": "2026-08-10"}},
			{ItemID: fixture.incompleteIDs[1], Fields: map[string]any{"title": "   "}},
		},
		UserID:             fixture.userID,
		AuthorizeWorkspace: func(int) (bool, error) { return true, nil },
	})
	if !IsBulkItemValidationError(err) {
		t.Fatalf("error = %v, want validation error", err)
	}

	var dated int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM items WHERE id IN (?, ?) AND start_date IS NOT NULL`, fixture.incompleteIDs[0], fixture.incompleteIDs[1]).Scan(&dated); err != nil {
		t.Fatalf("count dated items: %v", err)
	}
	if dated != 0 {
		t.Fatalf("dated items after rollback = %d, want 0", dated)
	}
}

func TestBulkPatchItemsPermissionFailureRollsBackEveryPatch(t *testing.T) {
	fixture := seedBulkFixture(t, 2)
	_, err := NewItemUpdateService(fixture.db).BulkPatchItems(context.Background(), BulkPatchItemsRequest{
		Patches: []BulkItemPatch{
			{ItemID: fixture.incompleteIDs[0], Fields: map[string]any{"start_date": "2026-08-10"}},
			{ItemID: fixture.incompleteIDs[1], Fields: map[string]any{"end_date": "2026-08-20"}},
		},
		UserID:             fixture.userID,
		AuthorizeWorkspace: func(int) (bool, error) { return false, nil },
	})
	if !errors.Is(err, ErrBulkItemForbidden) {
		t.Fatalf("error = %v, want ErrBulkItemForbidden", err)
	}

	var dated int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM items WHERE id IN (?, ?) AND (start_date IS NOT NULL OR end_date IS NOT NULL)`, fixture.incompleteIDs[0], fixture.incompleteIDs[1]).Scan(&dated); err != nil {
		t.Fatalf("count dated items: %v", err)
	}
	if dated != 0 {
		t.Fatalf("dated items after permission denial = %d, want 0", dated)
	}
}

func TestCompleteIterationBoundedAtRealisticCardinalities(t *testing.T) {
	for _, itemCount := range []int{10, 100, 500} {
		t.Run(fmt.Sprintf("%d_items", itemCount), func(t *testing.T) {
			fixture := seedBulkFixture(t, itemCount)
			var permissionCalls atomic.Int64
			started := time.Now()
			result, err := NewIterationCompletionService(fixture.db).Complete(context.Background(), CompleteIterationRequest{
				IterationID: fixture.sourceID, TargetIterationID: &fixture.targetID, UserID: fixture.userID,
				AuthorizeWorkspace: func(workspaceID int) (bool, error) {
					permissionCalls.Add(1)
					return workspaceID == fixture.workspaceID, nil
				},
				AuthorizeGlobal: func() (bool, error) { return false, nil },
			})
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			elapsed := time.Since(started)
			if result.MovedCount != itemCount || result.SQLStatements != 11 {
				t.Fatalf("completion = moved:%d SQL:%d, want %d/11", result.MovedCount, result.SQLStatements, itemCount)
			}
			if permissionCalls.Load() != 1 {
				t.Fatalf("workspace permission calls = %d, want 1", permissionCalls.Load())
			}
			var moved, history, completedStillSource int
			if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM items WHERE iteration_id = ? AND id != ?`, fixture.targetID, fixture.completedItem).Scan(&moved); err != nil {
				t.Fatalf("count moved: %v", err)
			}
			if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM item_history WHERE field_name = 'iteration_id'`).Scan(&history); err != nil {
				t.Fatalf("count history: %v", err)
			}
			if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM items WHERE id = ? AND iteration_id = ?`, fixture.completedItem, fixture.sourceID).Scan(&completedStillSource); err != nil {
				t.Fatalf("check completed item: %v", err)
			}
			var iterationStatus string
			if err := fixture.db.QueryRow(`SELECT status FROM iterations WHERE id = ?`, fixture.sourceID).Scan(&iterationStatus); err != nil {
				t.Fatalf("load iteration status: %v", err)
			}
			if moved != itemCount || history != itemCount || completedStillSource != 1 || iterationStatus != "completed" {
				t.Fatalf("persisted completion = moved:%d history:%d completed-source:%d status:%s",
					moved, history, completedStillSource, iterationStatus)
			}
			if elapsed > 2*time.Second {
				t.Fatalf("%d-item completion exceeded 2s budget: %s", itemCount, elapsed)
			}
			if inUse := fixture.db.GetDB().Stats().InUse; inUse != 0 {
				t.Fatalf("database connections still in use after completion: %d", inUse)
			}
			t.Logf("%d items: 1 request-equivalent, %d SQL statements, %s, pool in-use 0", itemCount, result.SQLStatements, elapsed)
		})
	}
}

func TestCompleteIterationRetryAndLimitAreDeterministic(t *testing.T) {
	t.Run("retry", func(t *testing.T) {
		fixture := seedBulkFixture(t, 10)
		request := CompleteIterationRequest{
			IterationID: fixture.sourceID, TargetIterationID: &fixture.targetID, UserID: fixture.userID,
			AuthorizeWorkspace: func(int) (bool, error) { return true, nil },
			AuthorizeGlobal:    func() (bool, error) { return false, nil },
		}
		service := NewIterationCompletionService(fixture.db)
		if _, err := service.Complete(context.Background(), request); err != nil {
			t.Fatalf("first completion: %v", err)
		}
		retry, err := service.Complete(context.Background(), request)
		if err != nil {
			t.Fatalf("retry completion: %v", err)
		}
		if !retry.AlreadyCompleted || retry.MovedCount != 0 || retry.SQLStatements != 1 {
			t.Fatalf("retry = already:%t moved:%d SQL:%d, want true/0/1", retry.AlreadyCompleted, retry.MovedCount, retry.SQLStatements)
		}
		var history int
		if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM item_history WHERE field_name = 'iteration_id'`).Scan(&history); err != nil {
			t.Fatalf("count retry history: %v", err)
		}
		if history != 10 {
			t.Fatalf("history after retry = %d, want 10", history)
		}
	})

	t.Run("limit rollback", func(t *testing.T) {
		fixture := seedBulkFixture(t, 501)
		_, err := NewIterationCompletionService(fixture.db).Complete(context.Background(), CompleteIterationRequest{
			IterationID: fixture.sourceID, TargetIterationID: &fixture.targetID, UserID: fixture.userID,
			AuthorizeWorkspace: func(int) (bool, error) { return true, nil },
			AuthorizeGlobal:    func() (bool, error) { return false, nil },
		})
		if !errors.Is(err, ErrIterationCompletionLimit) {
			t.Fatalf("error = %v, want ErrIterationCompletionLimit", err)
		}
		var sourceItems int
		if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM items WHERE iteration_id = ?`, fixture.sourceID).Scan(&sourceItems); err != nil {
			t.Fatalf("count source items: %v", err)
		}
		var status string
		if err := fixture.db.QueryRow(`SELECT status FROM iterations WHERE id = ?`, fixture.sourceID).Scan(&status); err != nil {
			t.Fatalf("load source status: %v", err)
		}
		if sourceItems != 502 || status != "active" {
			t.Fatalf("limit rollback = source items:%d status:%s, want 502/active", sourceItems, status)
		}
	})
}

func TestBulkOperationMetricsReportRollingLatencyAndPressure(t *testing.T) {
	metrics := NewBulkOperationMetrics()
	for i, duration := range []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond, 20 * time.Millisecond, 50 * time.Millisecond} {
		metrics.Observe(BulkOperationObservation{
			Kind: "iteration_completion", RequestedItems: 100, ChangedItems: 100,
			SQLStatements: 11, SideEffectsEmitted: 100, PoolInUse: i,
			Duration: duration, Failed: i == 4,
		})
	}
	stats := metrics.Stats().Operations["iteration_completion"]
	if stats.Requests != 5 || stats.Failures != 1 || stats.RequestedItems != 500 || stats.SQLStatements != 55 {
		t.Fatalf("metrics totals = %+v", stats)
	}
	if stats.SideEffectsEmitted != 500 || stats.PeakObservedPoolInUse != 4 || stats.LatencySamples != 5 {
		t.Fatalf("metrics pressure = %+v", stats)
	}
	if stats.LatencyP95Milliseconds != 50 || stats.LatencyP99Milliseconds != 50 {
		t.Fatalf("metrics latency = p95 %.3f p99 %.3f, want 50/50", stats.LatencyP95Milliseconds, stats.LatencyP99Milliseconds)
	}
}

func assertBulkTitlesUnchanged(t *testing.T, fixture bulkFixture) {
	t.Helper()
	var changed int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM items WHERE title IN ('Forbidden edit', '')`).Scan(&changed); err != nil {
		t.Fatalf("count changed titles: %v", err)
	}
	if changed != 0 {
		t.Fatalf("rollback left %d changed titles", changed)
	}
}
