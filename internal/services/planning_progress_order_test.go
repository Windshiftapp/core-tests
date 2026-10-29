//go:build test

package services

import (
	"slices"
	"testing"

	"windshift/internal/repository"
)

func TestPlanningProgressOrdersCategoriesByWorkflow(t *testing.T) {
	db := createPlanningTestDB(t)
	env := setupPlanningTestEnv(t, db)
	// Service fixtures use the production item creation path.
	for _, name := range []string{"Done", "In Progress", "Open"} {
		statusID := mustQueryID(t, db, `SELECT id FROM statuses WHERE name = ?`, name)
		if _, err := CreateItem(db, ItemCreationParams{
			WorkspaceID: env.WorkspaceID,
			Title:       name,
			StatusID:    &statusID,
		}); err != nil {
			t.Fatalf("create %s item: %v", name, err)
		}
	}

	progress, err := NewPlanningService(db).buildProgressReport(repository.ItemFilters{}, []int{env.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	want := []StatusBreakdown{
		{CategoryName: "To Do", CategoryBuiltinKey: "to_do", CategoryColor: "#d1d5db", ItemCount: 1},
		{CategoryName: "In Progress", CategoryBuiltinKey: "in_progress", CategoryColor: "#3b82f6", ItemCount: 1},
		{CategoryName: "Done", CategoryBuiltinKey: "done", CategoryColor: "#22c55e", ItemCount: 1, IsCompleted: true},
	}
	if !slices.Equal(want, progress.StatusBreakdown) {
		t.Fatalf("status breakdown = %#v, want %#v", progress.StatusBreakdown, want)
	}
}
