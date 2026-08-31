//go:build test

package repository

import (
	"context"
	"testing"

	"windshift/internal/cql"
	"windshift/internal/testutils"
)

func TestFindAllWithDetailsCQLNameFiltersJoinReferencedTables(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })

	workspaceID := testutils.InsertID(t, tdb.DB, `
		INSERT INTO workspaces (name, key, description, active)
		VALUES ('Name Filter Workspace', 'NFW', '', true)`)
	iterationID := testutils.InsertID(t, tdb.DB, `
		INSERT INTO iterations (name, description, start_date, end_date, status, is_global, workspace_id)
		VALUES ('Release Iteration', '', '2026-08-01', '2026-08-31', 'active', false, ?)`, workspaceID)
	projectID := testutils.InsertID(t, tdb.DB, `
		INSERT INTO time_projects (name, description, status)
		VALUES ('Delivery Project', '', 'Active')`)

	var priorityID int
	if err := tdb.QueryRow("SELECT id FROM priorities WHERE name = 'High'").Scan(&priorityID); err != nil {
		t.Fatalf("load High priority: %v", err)
	}
	matchingID := testutils.InsertID(t, tdb.DB, `
		INSERT INTO items (
			workspace_id, workspace_item_number, title, description, priority_id,
			iteration_id, project_id, frac_index
		) VALUES (?, 1, 'Matching item', '', ?, ?, ?, '0|a')`,
		workspaceID, priorityID, iterationID, projectID)
	testutils.InsertID(t, tdb.DB, `
		INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index)
		VALUES (?, 2, 'Unmatched item', '', '0|b')`, workspaceID)

	tests := []struct {
		name  string
		query string
	}{
		{name: "priority", query: `priority = "High"`},
		{name: "iteration", query: `iterationname = "Release Iteration"`},
		{name: "project", query: `projectname = "Delivery Project"`},
	}

	evaluator := cql.NewEvaluator(nil, nil, tdb.GetDriverName())
	repo := NewItemRepository(tdb.GetDatabase())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			where, args, err := evaluator.EvaluateToSQL(tt.query)
			if err != nil {
				t.Fatalf("evaluate %q: %v", tt.query, err)
			}

			items, total, err := repo.FindAllWithDetailsContext(context.Background(), ItemListParams{
				WorkspaceIDs: []int{workspaceID},
				Filters:      ItemFilters{QLQuery: where, QLArgs: args},
				Pagination:   PaginationParams{Limit: 100},
			})
			if err != nil {
				t.Fatalf("list items with %q: %v", tt.query, err)
			}
			if total != 1 || len(items) != 1 || items[0].ID != matchingID {
				t.Fatalf("results = total %d items %#v, want only item %d", total, items, matchingID)
			}
		})
	}
}
