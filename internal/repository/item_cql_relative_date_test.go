//go:build test

package repository

import (
	"context"
	"strconv"
	"testing"
	"time"

	"windshift/internal/cql"
	"windshift/internal/testutils"
)

func TestFindAllWithDetails_CQLRelativeCompletedAtAndMilestoneEmpty(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })

	now := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	workspaceID := testutils.InsertID(t, tdb.DB, `
		INSERT INTO workspaces (name, key, description, active, created_at, updated_at)
		VALUES ('Relative CQL', 'RLC', '', true, ?, ?)`, now, now)
	var itemTypeID int
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE name = 'Bug' LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("load Bug item type: %v", err)
	}
	openCategoryID := testutils.InsertID(t, tdb.DB, `
		INSERT INTO status_categories (name, color, is_completed) VALUES ('Relative Open', '#3b82f6', false)`)
	doneCategoryID := testutils.InsertID(t, tdb.DB, `
		INSERT INTO status_categories (name, color, is_completed) VALUES ('Relative Done', '#22c55e', true)`)
	openStatusID := testutils.InsertID(t, tdb.DB, `
		INSERT INTO statuses (name, category_id) VALUES ('Relative Open', ?)`, openCategoryID)
	doneStatusID := testutils.InsertID(t, tdb.DB, `
		INSERT INTO statuses (name, category_id) VALUES ('Relative Done', ?)`, doneCategoryID)
	var userID int
	if err := tdb.QueryRow("SELECT id FROM users LIMIT 1").Scan(&userID); err != nil {
		t.Fatalf("load test user: %v", err)
	}

	createItem := func(number int, title string, statusID int, createdAt time.Time) int {
		t.Helper()
		return testutils.InsertID(t, tdb.DB, `
			INSERT INTO items (
				workspace_id, workspace_item_number, item_type_id, title, description,
				status_id, is_task, frac_index, path, created_at, updated_at
			) VALUES (?, ?, ?, ?, '', ?, false, ?, ?, ?, ?)`,
			workspaceID, number, itemTypeID, title, statusID,
			testutils.NextTestFracIndex(), "/relative/"+strconv.Itoa(number)+"/", createdAt, createdAt)
	}

	recentID := createItem(1, "recent", doneStatusID, now.Add(-120*24*time.Hour))
	oldID := createItem(2, "old", doneStatusID, now.Add(-180*24*time.Hour))
	openID := createItem(3, "open", openStatusID, now.Add(-120*24*time.Hour))
	createdDoneID := createItem(4, "created done", doneStatusID, now.Add(-10*24*time.Hour))
	milestonedID := createItem(5, "milestoned", doneStatusID, now.Add(-120*24*time.Hour))

	recordStatus := func(itemID, oldStatus, newStatus int, changedAt time.Time) {
		t.Helper()
		_, err := tdb.Exec(`
			INSERT INTO item_history (item_id, user_id, field_name, old_value, new_value, changed_at)
			VALUES (?, ?, 'status_id', ?, ?, ?)`, itemID, userID, strconv.Itoa(oldStatus), strconv.Itoa(newStatus), changedAt)
		if err != nil {
			t.Fatalf("record status history: %v", err)
		}
	}
	recordStatus(recentID, openStatusID, doneStatusID, now.Add(-30*24*time.Hour))
	recordStatus(oldID, openStatusID, doneStatusID, now.Add(-91*24*time.Hour))
	recordStatus(milestonedID, openStatusID, doneStatusID, now.Add(-30*24*time.Hour))

	milestoneID := testutils.InsertID(t, tdb.DB, `
		INSERT INTO milestones (name, status, is_global, workspace_id) VALUES ('Release 1', 'planning', false, ?)`, workspaceID)
	if _, err := tdb.Exec(`INSERT INTO item_milestones (item_id, milestone_id) VALUES (?, ?)`, milestonedID, milestoneID); err != nil {
		t.Fatalf("attach milestone: %v", err)
	}

	evaluator := cql.NewEvaluator(nil, nil, tdb.GetDriverName())
	sqlWhere, sqlArgs, err := evaluator.EvaluateToSQLAt(
		`itemtypename = Bug AND completed_at >= -90d AND milestonename IS EMPTY`, now)
	if err != nil {
		t.Fatalf("evaluate CQL: %v", err)
	}

	items, total, err := NewItemRepository(tdb.GetDatabase()).FindAllWithDetailsContext(context.Background(), ItemListParams{
		WorkspaceIDs: []int{workspaceID},
		Filters:      ItemFilters{QLQuery: sqlWhere, QLArgs: sqlArgs},
		Pagination:   PaginationParams{Limit: 100},
	})
	if err != nil {
		t.Fatalf("list CQL results: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("results = %d/%d, want 2/2: %#v", total, len(items), items)
	}
	got := make(map[int]bool, len(items))
	for _, item := range items {
		got[item.ID] = true
	}
	for _, id := range []int{recentID, createdDoneID} {
		if !got[id] {
			t.Errorf("expected item %d in results", id)
		}
	}
	for _, id := range []int{oldID, openID, milestonedID} {
		if got[id] {
			t.Errorf("unexpected item %d in results", id)
		}
	}
}
