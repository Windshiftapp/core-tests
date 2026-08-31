package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"windshift/internal/testutils"
)

func TestFindAllWithDetailsCustomFieldSortContracts(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	var workspaceID, selectFieldID, multiUserFieldID int
	for _, fixture := range []struct {
		query string
		dest  *int
	}{
		{`INSERT INTO workspaces (name, key) VALUES ('Custom sort', 'CFS') RETURNING id`, &workspaceID},
		{`INSERT INTO custom_field_definitions (name, field_type, options) VALUES ('Choice', 'select', '["One","Two","Ten"]') RETURNING id`, &selectFieldID},
		{`INSERT INTO custom_field_definitions (name, field_type) VALUES ('Reviewers', 'multi_user') RETURNING id`, &multiUserFieldID},
	} {
		if err := db.QueryRow(fixture.query).Scan(fixture.dest); err != nil {
			t.Fatalf("seed sort fixture: %v", err)
		}
	}

	for number, item := range []struct {
		title       string
		rank        string
		selectID    int
		multiUserID int
	}{
		{title: "Ten", rank: "a", selectID: 10, multiUserID: 3},
		{title: "One", rank: "b", selectID: 1, multiUserID: 2},
		{title: "Two", rank: "c", selectID: 2, multiUserID: 1},
	} {
		customValues := fmt.Sprintf(`{"%d":%d,"%d":[%d]}`, selectFieldID, item.selectID, multiUserFieldID, item.multiUserID)
		if _, err := db.ExecWrite(`
			INSERT INTO items (
				workspace_id, workspace_item_number, title, description,
				frac_index, custom_field_values, created_at, updated_at
			) VALUES (?, ?, ?, '', ?, ?, ?, ?)
		`, workspaceID, number+1, item.title, item.rank, customValues, time.Now().UTC(), time.Now().UTC()); err != nil {
			t.Fatalf("insert %q: %v", item.title, err)
		}
	}

	tests := []struct {
		name   string
		sortBy int
		want   []string
	}{
		{name: "scalar option IDs sort numerically", sortBy: selectFieldID, want: []string{"One", "Two", "Ten"}},
		{name: "multi-user arrays fall back to rank", sortBy: multiUserFieldID, want: []string{"Ten", "One", "Two"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, total, err := NewItemRepository(db).FindAllWithDetailsContext(context.Background(), ItemListParams{
				WorkspaceIDs: []int{workspaceID},
				Pagination:   PaginationParams{Limit: 10},
				SortBy:       fmt.Sprint(tt.sortBy),
				SortAsc:      true,
			})
			if err != nil {
				t.Fatalf("list sorted items: %v", err)
			}
			if total != 3 {
				t.Fatalf("total = %d, want 3", total)
			}
			titles := make([]string, len(items))
			for i, item := range items {
				titles[i] = item.Title
			}
			expectTitles(t, titles, tt.want)
		})
	}
}

func TestFindAllWithDetailsOrdersBubblePagesByEffectiveActivity(t *testing.T) {
	db := newItemListTestDB(t, "item-list-bubble-order")

	workspaceResult, err := db.ExecWrite(
		`INSERT INTO workspaces (name, key, description, active) VALUES (?, ?, '', true)`,
		"Bubble Test",
		"BUB",
	)
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	workspaceID64, err := workspaceResult.LastInsertId()
	if err != nil {
		t.Fatalf("workspace LastInsertId: %v", err)
	}
	workspaceID := int(workspaceID64)

	base := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)
	type seededItem struct {
		title        string
		fracIndex    string
		updatedAt    time.Time
		lastActiveAt *time.Time
	}
	newest := base.Add(3 * time.Hour)
	itemsToSeed := []seededItem{
		{title: "Old rank-first item", fracIndex: "a0", updatedAt: base, lastActiveAt: timePointer(base)},
		{title: "Newest lower-ID item", fracIndex: "a1", updatedAt: newest, lastActiveAt: timePointer(newest)},
		{title: "Newest higher-ID item", fracIndex: "a2", updatedAt: newest, lastActiveAt: timePointer(newest)},
		{title: "Fallback activity item", fracIndex: "a3", updatedAt: base.Add(2 * time.Hour)},
	}

	for number, item := range itemsToSeed {
		var lastActiveAt any
		if item.lastActiveAt != nil {
			lastActiveAt = item.lastActiveAt.Format(time.RFC3339Nano)
		}
		if _, err := db.ExecWrite(`
			INSERT INTO items (
				workspace_id, workspace_item_number, title, description,
				frac_index, created_at, updated_at, last_active_at
			) VALUES (?, ?, ?, '', ?, ?, ?, ?)
		`,
			workspaceID,
			number+1,
			item.title,
			item.fracIndex,
			base.Format(time.RFC3339Nano),
			item.updatedAt.Format(time.RFC3339Nano),
			lastActiveAt,
		); err != nil {
			t.Fatalf("insert %q: %v", item.title, err)
		}
	}

	repo := NewItemRepository(db)
	listPage := func(offset int) []string {
		t.Helper()
		items, total, err := repo.FindAllWithDetailsContext(context.Background(), ItemListParams{
			WorkspaceIDs: []int{workspaceID},
			Pagination:   PaginationParams{Limit: 2, Offset: offset},
			SortBy:       "last_active_at",
		})
		if err != nil {
			t.Fatalf("list page at offset %d: %v", offset, err)
		}
		if total != len(itemsToSeed) {
			t.Fatalf("total = %d, want %d", total, len(itemsToSeed))
		}
		titles := make([]string, len(items))
		for i, item := range items {
			titles[i] = item.Title
		}
		return titles
	}

	firstPage := listPage(0)
	secondPage := listPage(2)
	expectTitles(t, firstPage, []string{"Newest higher-ID item", "Newest lower-ID item"})
	expectTitles(t, secondPage, []string{"Fallback activity item", "Old rank-first item"})
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func expectTitles(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("titles = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("titles = %v, want %v", got, want)
		}
	}
}
