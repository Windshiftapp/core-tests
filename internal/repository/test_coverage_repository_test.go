//go:build test

package repository

import (
	"slices"
	"testing"

	"windshift/internal/testutils"
)

func TestTestCoverageRepositoryUsesTestsBuiltinKey(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	var requirementTypeID int
	if err := tdb.QueryRow("SELECT id FROM item_types ORDER BY id LIMIT 1").Scan(&requirementTypeID); err != nil {
		t.Fatalf("find requirement item type: %v", err)
	}
	requirementID := testutils.InsertID(t, tdb, `
		INSERT INTO items (workspace_id, workspace_item_number, item_type_id, title, frac_index)
		VALUES (?, 71, ?, 'Coverage requirement', 'coverage-requirement')
	`, data.WorkspaceID, requirementTypeID)
	testCaseID := testutils.InsertID(t, tdb, `
		INSERT INTO test_cases (workspace_id, title, priority)
		VALUES (?, 'Requirement test', 'medium')
	`, data.WorkspaceID)

	var testsLinkTypeID, movedLinkTypeID int
	if err := tdb.QueryRow("SELECT id FROM link_types WHERE builtin_key = 'tests'").Scan(&testsLinkTypeID); err != nil {
		t.Fatalf("find Tests link type: %v", err)
	}
	if err := tdb.QueryRow("SELECT COALESCE(MAX(id), 0) + 100 FROM link_types").Scan(&movedLinkTypeID); err != nil {
		t.Fatalf("allocate moved link type id: %v", err)
	}
	if _, err := tdb.Exec("UPDATE link_types SET id = ? WHERE id = ?", movedLinkTypeID, testsLinkTypeID); err != nil {
		t.Fatalf("move Tests link type away from its seed id: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id)
		VALUES (?, 'item', ?, 'test_case', ?)
	`, movedLinkTypeID, requirementID, testCaseID); err != nil {
		t.Fatalf("link requirement to test case: %v", err)
	}

	repo := NewTestCoverageRepository(tdb.GetDatabase())
	total, covered, err := repo.GetCoverageSummary(data.WorkspaceID, []int{requirementTypeID})
	if err != nil {
		t.Fatalf("get coverage summary: %v", err)
	}
	if total != 1 || covered != 1 {
		t.Fatalf("coverage summary = (%d total, %d covered), want (1, 1)", total, covered)
	}

	requirements, err := repo.ListRequirements(RequirementListParams{
		WorkspaceID: data.WorkspaceID,
		TypeIDs:     []int{requirementTypeID},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("list requirements: %v", err)
	}
	if len(requirements) != 1 || requirements[0].ItemID != requirementID || !requirements[0].IsCovered || requirements[0].LinkedTestCount != 1 {
		t.Fatalf("requirements = %+v, want linked requirement %d", requirements, requirementID)
	}
}

func TestRequirementCoverageFiltersAndPagination(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	var typeID, linkTypeID int
	if err := tdb.QueryRow("SELECT id FROM item_types ORDER BY id LIMIT 1").Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	if err := tdb.QueryRow("SELECT id FROM link_types WHERE builtin_key = 'tests'").Scan(&linkTypeID); err != nil {
		t.Fatal(err)
	}
	coveredID := testutils.InsertID(t, tdb, `INSERT INTO items (workspace_id, workspace_item_number, item_type_id, title, frac_index, created_at) VALUES (?, 71, ?, 'Covered requirement', 'a', '2026-01-02 00:00:00')`, data.WorkspaceID, typeID)
	uncoveredID := testutils.InsertID(t, tdb, `INSERT INTO items (workspace_id, workspace_item_number, item_type_id, title, frac_index, created_at) VALUES (?, 72, ?, 'Uncovered requirement', 'b', '2026-01-01 00:00:00')`, data.WorkspaceID, typeID)
	caseID := testutils.InsertID(t, tdb, `INSERT INTO test_cases (workspace_id, title, priority) VALUES (?, 'Coverage test', 'medium')`, data.WorkspaceID)
	if _, err := tdb.Exec(`INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id) VALUES (?, 'test_case', ?, 'item', ?)`, linkTypeID, caseID, coveredID); err != nil {
		t.Fatal(err)
	}
	repo := NewTestCoverageRepository(tdb.GetDatabase())
	for _, tc := range []struct {
		name, filter, search string
		offset               int
		wantIDs              []int
		total                int
	}{
		{name: "all", wantIDs: []int{coveredID, uncoveredID}, total: 2},
		{name: "covered", filter: "true", wantIDs: []int{coveredID}, total: 1},
		{name: "uncovered", filter: "false", wantIDs: []int{uncoveredID}, total: 1},
		{name: "search", search: "Uncovered", wantIDs: []int{uncoveredID}, total: 1},
		{name: "search excludes covered", filter: "true", search: "Uncovered", wantIDs: []int{}, total: 0},
		{name: "offset", offset: 1, wantIDs: []int{uncoveredID}, total: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := RequirementListParams{WorkspaceID: data.WorkspaceID, TypeIDs: []int{typeID}, CoveredFilter: tc.filter, Search: tc.search, Limit: 10, Offset: tc.offset}
			rows, err := repo.ListRequirements(params)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]int, 0, len(rows))
			for _, row := range rows {
				ids = append(ids, row.ItemID)
				wantCount := 0
				if row.ItemID == coveredID {
					wantCount = 1
				}
				if row.LinkedTestCount != wantCount || row.IsCovered != (wantCount > 0) {
					t.Errorf("coverage = %+v, want linked count %d", row, wantCount)
				}
				if row.WorkspaceKey == "" || row.ItemTypeName == "" {
					t.Errorf("missing joined metadata: %+v", row)
				}
			}
			if !slices.Equal(ids, tc.wantIDs) {
				t.Fatalf("IDs = %v, want %v", ids, tc.wantIDs)
			}
			count, err := repo.CountRequirements(params)
			if err != nil {
				t.Fatal(err)
			}
			if count != tc.total {
				t.Fatalf("count = %d, want %d", count, tc.total)
			}
		})
	}
}
