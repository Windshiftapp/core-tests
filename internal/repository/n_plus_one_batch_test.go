//go:build test

package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// These tests guard the WI-1909 N+1 removals: each batched read must issue one
// query no matter how many rows it is given, and must still return the same
// grouping the per-row loops produced.

func insertBatchTestItem(t *testing.T, db database.Database, seed testutils.TestDataSet, workspaceID, number int, parentID *int) int {
	t.Helper()
	return testutils.InsertID(t, db, `
		INSERT INTO items (
			workspace_id, workspace_item_number, title, description, parent_id,
			status_id, priority_id, frac_index, created_at, updated_at
		) VALUES (?, ?, ?, '', ?, ?, ?, ?, ?, ?)
	`, workspaceID, number, "n+1 fixture", parentID,
		seed.StatusID, seed.PriorityID, testutils.NextTestFracIndex(),
		time.Now().UTC(), time.Now().UTC())
}

func TestGetWorkspaceIDsResolvesAllItemsInOneQuery(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	otherWorkspace := testutils.InsertID(t, db,
		`INSERT INTO workspaces (name, key, description, active) VALUES (?, ?, '', true)`,
		"Other Workspace", "OTH")

	const itemCount = 10
	itemIDs := make([]int, 0, itemCount)
	wantWorkspace := make(map[int]int, itemCount)
	for i := 0; i < itemCount; i++ {
		workspaceID := seed.WorkspaceID
		if i%2 == 1 {
			workspaceID = otherWorkspace
		}
		id := insertBatchTestItem(t, db, seed, workspaceID, i+1, nil)
		itemIDs = append(itemIDs, id)
		wantWorkspace[id] = workspaceID
	}

	counting := testutils.CountQueries(db)
	repo := NewItemRepository(counting)

	resolved, err := repo.GetWorkspaceIDs(itemIDs)
	if err != nil {
		t.Fatalf("GetWorkspaceIDs: %v", err)
	}
	if len(resolved) != itemCount {
		t.Fatalf("resolved %d items, want %d", len(resolved), itemCount)
	}
	for id, want := range wantWorkspace {
		if resolved[id] != want {
			t.Errorf("item %d workspace = %d, want %d", id, resolved[id], want)
		}
	}
	if got := counting.Queries(); got != 1 {
		t.Fatalf("GetWorkspaceIDs issued %d queries for %d items, want 1", got, itemCount)
	}
}

func TestGetWorkspaceIDsOmitsUnknownItems(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	known := insertBatchTestItem(t, db, seed, seed.WorkspaceID, 1, nil)

	repo := NewItemRepository(db)
	resolved, err := repo.GetWorkspaceIDs([]int{known, 999999})
	if err != nil {
		t.Fatalf("GetWorkspaceIDs: %v", err)
	}
	if len(resolved) != 1 || resolved[known] != seed.WorkspaceID {
		t.Fatalf("resolved = %v, want only item %d in workspace %d", resolved, known, seed.WorkspaceID)
	}
	if _, ok := resolved[999999]; ok {
		t.Fatalf("unknown item must be absent from the map, got %v", resolved)
	}
}

func TestGetWorkspaceIDsEmptyInputIssuesNoQuery(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	tdb.SeedTestData(t)

	counting := testutils.CountQueries(tdb.GetDatabase())
	repo := NewItemRepository(counting)

	resolved, err := repo.GetWorkspaceIDs(nil)
	if err != nil {
		t.Fatalf("GetWorkspaceIDs: %v", err)
	}
	if len(resolved) != 0 {
		t.Fatalf("resolved = %v, want empty", resolved)
	}
	if got := counting.Queries(); got != 0 {
		t.Fatalf("empty input issued %d queries, want 0", got)
	}
}

func TestCountByFieldInGroupsInOneQuery(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	otherWorkspace := testutils.InsertID(t, db,
		`INSERT INTO workspaces (name, key, description, active) VALUES (?, ?, '', true)`,
		"Other Workspace", "OTH")

	for i := 0; i < 3; i++ {
		insertBatchTestItem(t, db, seed, seed.WorkspaceID, i+1, nil)
	}
	for i := 0; i < 2; i++ {
		insertBatchTestItem(t, db, seed, otherWorkspace, i+10, nil)
	}

	counting := testutils.CountQueries(db)
	repo := NewItemRepository(counting)

	counts, err := repo.CountByFieldIn("workspace_id", []int{seed.WorkspaceID, otherWorkspace, 999999})
	if err != nil {
		t.Fatalf("CountByFieldIn: %v", err)
	}
	if counts[seed.WorkspaceID] != 3 {
		t.Errorf("workspace %d count = %d, want 3", seed.WorkspaceID, counts[seed.WorkspaceID])
	}
	if counts[otherWorkspace] != 2 {
		t.Errorf("workspace %d count = %d, want 2", otherWorkspace, counts[otherWorkspace])
	}
	if _, ok := counts[999999]; ok {
		t.Errorf("workspace with no items must be absent, got %v", counts)
	}
	if got := counting.Queries(); got != 1 {
		t.Fatalf("CountByFieldIn issued %d queries, want 1", got)
	}
}

func TestCountByFieldInRejectsNonAllowlistedColumn(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	tdb.SeedTestData(t)

	repo := NewItemRepository(tdb.GetDatabase())
	if _, err := repo.CountByFieldIn("title", []int{1}); err == nil {
		t.Fatal("CountByFieldIn must reject a column outside the allow-list")
	}
}

func TestFindRootIDForItemContextWalksInOneQuery(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	// Build a chain deeper than the old per-level walk would have been cheap
	// for: every level used to cost one query.
	const depth = 20
	var parentID *int
	rootID := 0
	deepestID := 0
	for i := 0; i < depth; i++ {
		id := insertBatchTestItem(t, db, seed, seed.WorkspaceID, i+1, parentID)
		if i == 0 {
			rootID = id
		}
		deepestID = id
		next := id
		parentID = &next
	}

	counting := testutils.CountQueries(db)
	repo := NewItemRepository(counting)

	got, err := repo.FindRootIDForItemContext(context.Background(), deepestID, 30)
	if err != nil {
		t.Fatalf("FindRootIDForItemContext: %v", err)
	}
	if got == nil || *got != rootID {
		t.Fatalf("root = %v, want %d", got, rootID)
	}
	if queries := counting.Queries(); queries != 1 {
		t.Fatalf("walk of depth %d issued %d queries, want 1", depth, queries)
	}
}

func TestFindRootIDForItemContextReportsMissingItem(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	tdb.SeedTestData(t)

	repo := NewItemRepository(tdb.GetDatabase())
	got, err := repo.FindRootIDForItemContext(context.Background(), 999999, 30)
	if err != nil {
		t.Fatalf("FindRootIDForItemContext: %v", err)
	}
	if got != nil {
		t.Fatalf("root = %d, want nil for a non-existent item", *got)
	}
}

func TestFindRootIDForItemContextDetectsCycle(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	first := insertBatchTestItem(t, db, seed, seed.WorkspaceID, 1, nil)
	second := insertBatchTestItem(t, db, seed, seed.WorkspaceID, 2, &first)
	third := insertBatchTestItem(t, db, seed, seed.WorkspaceID, 3, &second)
	// Close the loop: first -> third -> second -> first.
	if _, err := db.Exec(`UPDATE items SET parent_id = ? WHERE id = ?`, third, first); err != nil {
		t.Fatalf("inject cycle: %v", err)
	}

	repo := NewItemRepository(db)
	_, err := repo.FindRootIDForItemContext(context.Background(), third, 30)
	if err == nil {
		t.Fatal("a stored cycle must surface an error, not a silent root")
	}
	if !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("error should mention cyclic, got: %v", err)
	}
}

func TestFindStepResultsForExamplesGroupsInOneQuery(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	setID := testutils.InsertID(t, db,
		`INSERT INTO test_sets (workspace_id, name, description) VALUES (?, ?, '')`,
		seed.WorkspaceID, "n+1 set")
	testCaseID := testutils.InsertID(t, db,
		`INSERT INTO test_cases (workspace_id, title, format, priority) VALUES (?, ?, 'bdd', 'medium')`,
		seed.WorkspaceID, "n+1 case")
	runID := testutils.InsertID(t, db,
		`INSERT INTO test_runs (workspace_id, set_id, name) VALUES (?, ?, ?)`,
		seed.WorkspaceID, setID, "n+1 run")

	const exampleCount = 5
	resultIDs := make([]int, 0, exampleCount)
	wantSteps := make(map[int][]int, exampleCount)
	for i := 0; i < exampleCount; i++ {
		resultID := testutils.InsertID(t, db, `
			INSERT INTO test_example_results (run_id, test_case_id, example_index, row_values, status)
			VALUES (?, ?, ?, '{}', 'not_run')
		`, runID, testCaseID, i)
		resultIDs = append(resultIDs, resultID)
		// Give each example a different number of steps so grouping errors
		// surface as mismatched lengths rather than passing by coincidence.
		stepCount := i + 1
		for step := 1; step <= stepCount; step++ {
			testutils.InsertID(t, db, `
				INSERT INTO test_example_step_results (example_result_id, step_number, status, actual_result, notes)
				VALUES (?, ?, 'passed', ?, '')
			`, resultID, step, "output")
			wantSteps[resultID] = append(wantSteps[resultID], step)
		}
	}

	counting := testutils.CountQueries(db)
	repo := NewTestCaseBDDRepository(counting)

	grouped, err := repo.FindStepResultsForExamples(resultIDs)
	if err != nil {
		t.Fatalf("FindStepResultsForExamples: %v", err)
	}
	if len(grouped) != exampleCount {
		t.Fatalf("grouped %d examples, want %d", len(grouped), exampleCount)
	}
	for resultID, want := range wantSteps {
		got := grouped[resultID]
		if len(got) != len(want) {
			t.Fatalf("example %d has %d steps, want %d", resultID, len(got), len(want))
		}
		for i, step := range got {
			if step.StepNumber != want[i] {
				t.Errorf("example %d step %d number = %d, want %d", resultID, i, step.StepNumber, want[i])
			}
			if step.ExampleResultID != resultID {
				t.Errorf("step %d attributed to example %d, want %d", step.StepNumber, step.ExampleResultID, resultID)
			}
		}
	}
	if got := counting.Queries(); got != 1 {
		t.Fatalf("FindStepResultsForExamples issued %d queries for %d examples, want 1", got, exampleCount)
	}
}

func TestFindStepResultsForExamplesReturnsEmptySliceForSteplessExample(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	setID := testutils.InsertID(t, db,
		`INSERT INTO test_sets (workspace_id, name, description) VALUES (?, ?, '')`,
		seed.WorkspaceID, "n+1 set")
	testCaseID := testutils.InsertID(t, db,
		`INSERT INTO test_cases (workspace_id, title, format, priority) VALUES (?, ?, 'bdd', 'medium')`,
		seed.WorkspaceID, "n+1 case")
	runID := testutils.InsertID(t, db,
		`INSERT INTO test_runs (workspace_id, set_id, name) VALUES (?, ?, ?)`,
		seed.WorkspaceID, setID, "n+1 run")
	resultID := testutils.InsertID(t, db, `
		INSERT INTO test_example_results (run_id, test_case_id, example_index, row_values, status)
		VALUES (?, ?, 0, '{}', 'not_run')
	`, runID, testCaseID)

	repo := NewTestCaseBDDRepository(db)
	grouped, err := repo.FindStepResultsForExamples([]int{resultID})
	if err != nil {
		t.Fatalf("FindStepResultsForExamples: %v", err)
	}
	steps, ok := grouped[resultID]
	if !ok {
		t.Fatalf("example %d missing from grouped result", resultID)
	}
	// The per-example loop assigned an empty (non-nil) slice; keep that so the
	// JSON contract stays [] rather than null.
	if steps == nil {
		t.Fatalf("steps for a stepless example must be an empty slice, got nil")
	}
	if len(steps) != 0 {
		t.Fatalf("steps = %d, want 0", len(steps))
	}
}

func TestFindAssetTypeFieldsForTypesGroupsInOneQuery(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	setID := testutils.InsertID(t, db,
		`INSERT INTO asset_management_sets (name) VALUES (?)`, "n+1 asset set")

	const typeCount = 4
	typeIDs := make([]int, 0, typeCount)
	wantFields := make(map[int]int, typeCount)
	for i := 0; i < typeCount; i++ {
		typeID := testutils.InsertID(t, db,
			`INSERT INTO asset_types (set_id, name) VALUES (?, ?)`, setID, "Type "+string(rune('A'+i)))
		typeIDs = append(typeIDs, typeID)
		fieldCount := i + 1
		for f := 0; f < fieldCount; f++ {
			fieldID := testutils.InsertID(t, db,
				`INSERT INTO custom_field_definitions (name, field_type) VALUES (?, 'text')`,
				"Field")
			testutils.InsertID(t, db, `
				INSERT INTO asset_type_fields (asset_type_id, custom_field_id, is_required, display_order)
				VALUES (?, ?, false, ?)
			`, typeID, fieldID, f)
		}
		wantFields[typeID] = fieldCount
	}

	counting := testutils.CountQueries(db)
	repo := NewAssetRepository(counting)

	grouped, err := repo.FindAssetTypeFieldsForTypes(typeIDs)
	if err != nil {
		t.Fatalf("FindAssetTypeFieldsForTypes: %v", err)
	}
	if len(grouped) != typeCount {
		t.Fatalf("grouped %d types, want %d", len(grouped), typeCount)
	}
	for typeID, want := range wantFields {
		if got := len(grouped[typeID]); got != want {
			t.Fatalf("type %d has %d fields, want %d", typeID, got, want)
		}
		for _, field := range grouped[typeID] {
			if field.AssetTypeID != typeID {
				t.Errorf("field %d attributed to type %d, want %d", field.ID, field.AssetTypeID, typeID)
			}
		}
	}
	if got := counting.Queries(); got != 1 {
		t.Fatalf("FindAssetTypeFieldsForTypes issued %d queries for %d types, want 1", got, typeCount)
	}
}
