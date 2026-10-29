//go:build test

package repository

import (
	"testing"

	"windshift/internal/testutils"
)

func TestTestRunRepositoryFindPageBoundsAndCountsResults(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.DB
	workspaceID := testutils.InsertID(t, db, `INSERT INTO workspaces (name, key) VALUES ('Test run page', 'TRP')`)
	planID := testutils.InsertID(t, db, `INSERT INTO test_sets (workspace_id, name) VALUES (?, 'Plan')`, workspaceID)
	ids := make([]int, 3)
	for index := range ids {
		ids[index] = testutils.InsertID(t, db, `INSERT INTO test_runs (workspace_id, set_id, name) VALUES (?, ?, ?)`, workspaceID, planID, "Run")
	}

	repo := NewTestRunRepository(db)
	first, total, err := repo.FindPage(workspaceID, TestRunFilters{IncludeEnded: true, Limit: 2})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if total != 3 || len(first) != 2 || first[0].ID != ids[2] || first[1].ID != ids[1] {
		t.Fatalf("first page = total %d rows %+v", total, first)
	}
	second, total, err := repo.FindPage(workspaceID, TestRunFilters{IncludeEnded: true, Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if total != 3 || len(second) != 1 || second[0].ID != ids[0] {
		t.Fatalf("second page = total %d rows %+v", total, second)
	}
}
