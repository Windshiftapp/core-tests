//go:build test

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"windshift/internal/cql"
	"windshift/internal/testutils"
)

// TestFindAllWithDetails_CQLBooleanColumns executes boolean selectors against
// real boolean columns on both engines. Postgres rejects `boolean_col = 0`
// ("operator does not exist: boolean = integer"), so the SQLite-only path hid
// the bug; this runs the production CQL pipeline and item query per driver.
func TestFindAllWithDetails_CQLBooleanColumns(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })

	now := time.Now().UTC()
	workspaceID := testutils.InsertID(t, tdb.DB, `
		INSERT INTO workspaces (name, key, description, active, created_at, updated_at)
		VALUES ('Boolean CQL', 'BLC', '', true, ?, ?)`, now, now)
	var itemTypeID int
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE name = 'Bug' LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("load Bug item type: %v", err)
	}
	openCategoryID := testutils.InsertID(t, tdb.DB, `
		INSERT INTO status_categories (name, color, is_completed) VALUES ('Boolean Open', '#3b82f6', false)`)
	doneCategoryID := testutils.InsertID(t, tdb.DB, `
		INSERT INTO status_categories (name, color, is_completed) VALUES ('Boolean Done', '#22c55e', true)`)
	openStatusID := testutils.InsertID(t, tdb.DB, `
		INSERT INTO statuses (name, category_id) VALUES ('Boolean Open', ?)`, openCategoryID)
	doneStatusID := testutils.InsertID(t, tdb.DB, `
		INSERT INTO statuses (name, category_id) VALUES ('Boolean Done', ?)`, doneCategoryID)

	createItem := func(number int, title string, statusID int) int {
		t.Helper()
		return testutils.InsertID(t, tdb.DB, `
			INSERT INTO items (
				workspace_id, workspace_item_number, item_type_id, title, description,
				status_id, is_task, frac_index, path, created_at, updated_at
			) VALUES (?, ?, ?, ?, '', ?, false, ?, ?, ?, ?)`,
			workspaceID, number, itemTypeID, title, statusID,
			testutils.NextTestFracIndex(), fmt.Sprintf("/boolean/%d/", number), now, now)
	}

	openID := createItem(1, "open", openStatusID)
	doneID := createItem(2, "done", doneStatusID)

	for _, tc := range []struct {
		name string
		ql   string
		want []int
	}{
		{"not completed", `statusCompleted = false`, []int{openID}},
		{"completed", `statusCompleted = true`, []int{doneID}},
		{"not equal completed", `statusCompleted != true`, []int{openID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evaluator := cql.NewEvaluator(nil, nil, tdb.GetDriverName())
			sqlWhere, sqlArgs, err := evaluator.EvaluateToSQL(tc.ql)
			if err != nil {
				t.Fatalf("evaluate CQL: %v", err)
			}
			items, total, err := NewItemRepository(tdb.GetDatabase()).FindAllWithDetailsContext(
				context.Background(),
				ItemListParams{
					WorkspaceIDs: []int{workspaceID},
					Filters:      ItemFilters{QLQuery: sqlWhere, QLArgs: sqlArgs},
					Pagination:   PaginationParams{Limit: 100},
				},
			)
			if err != nil {
				t.Fatalf("list CQL results: %v", err)
			}
			if total != len(tc.want) || len(items) != len(tc.want) {
				t.Fatalf("results = %d/%d, want %d", len(items), total, len(tc.want))
			}
			got := make(map[int]bool, len(items))
			for _, item := range items {
				got[item.ID] = true
			}
			for _, id := range tc.want {
				if !got[id] {
					t.Errorf("expected item %d in results", id)
				}
			}
		})
	}
}
