//go:build test

package services

import (
	"fmt"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// WI-1909: the service paths that used to fan out one query per row must now
// issue a constant number of queries as their input grows.

func TestGetRootIssuesConstantQueriesRegardlessOfDepth(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	insertItem := func(number int, parentID *int) int {
		t.Helper()
		return testutils.InsertID(t, db, `
			INSERT INTO items (
				workspace_id, workspace_item_number, title, description, parent_id,
				status_id, priority_id, frac_index, created_at, updated_at
			) VALUES (?, ?, ?, '', ?, ?, ?, ?, ?, ?)
		`, seed.WorkspaceID, number, "root walk", parentID,
			seed.StatusID, seed.PriorityID, testutils.NextTestFracIndex(),
			time.Now().UTC(), time.Now().UTC())
	}

	counting := testutils.CountQueries(db)
	service := NewHierarchyService(counting)

	buildChain := func(depth, firstNumber int) int {
		t.Helper()
		var parentID *int
		leaf := 0
		for i := 0; i < depth; i++ {
			id := insertItem(firstNumber+i, parentID)
			leaf = id
			next := id
			parentID = &next
		}
		return leaf
	}

	shallowLeaf := buildChain(3, 1)
	counting.ResetQueries()
	shallowRoot, err := service.GetRoot(shallowLeaf)
	if err != nil {
		t.Fatalf("GetRoot (depth 3): %v", err)
	}
	if shallowRoot == nil {
		t.Fatal("GetRoot (depth 3) returned nil")
	}
	shallowQueries := counting.Queries()

	deepLeaf := buildChain(25, 100)
	counting.ResetQueries()
	deepRoot, err := service.GetRoot(deepLeaf)
	if err != nil {
		t.Fatalf("GetRoot (depth 25): %v", err)
	}
	if deepRoot == nil {
		t.Fatal("GetRoot (depth 25) returned nil")
	}
	deepQueries := counting.Queries()

	if shallowQueries != deepQueries {
		t.Fatalf("GetRoot issued %d queries at depth 3 and %d at depth 25: the per-level walk is back",
			shallowQueries, deepQueries)
	}
}

func TestListExampleResultsIssuesConstantQueries(t *testing.T) {
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

	addExample := func(index int) {
		t.Helper()
		resultID := testutils.InsertID(t, db, `
			INSERT INTO test_example_results (run_id, test_case_id, example_index, row_values, status)
			VALUES (?, ?, ?, '{}', 'not_run')
		`, runID, testCaseID, index)
		testutils.InsertID(t, db, `
			INSERT INTO test_example_step_results (example_result_id, step_number, status, actual_result, notes)
			VALUES (?, 1, 'passed', 'output', '')
		`, resultID)
	}

	addExample(0)

	counting := testutils.CountQueries(db)
	service := NewTestRunService(counting)

	counting.ResetQueries()
	single, err := service.ListExampleResults(runID, seed.WorkspaceID)
	if err != nil {
		t.Fatalf("ListExampleResults with one example: %v", err)
	}
	queriesForOne := counting.Queries()
	if len(single) != 1 || len(single[0].StepResults) != 1 {
		t.Fatalf("one-example result = %d examples, want 1 with 1 step", len(single))
	}

	const extra = 7
	for i := 1; i <= extra; i++ {
		addExample(i)
	}

	counting.ResetQueries()
	many, err := service.ListExampleResults(runID, seed.WorkspaceID)
	if err != nil {
		t.Fatalf("ListExampleResults with many examples: %v", err)
	}
	queriesForMany := counting.Queries()
	if len(many) != extra+1 {
		t.Fatalf("many-example result = %d examples, want %d", len(many), extra+1)
	}
	for _, result := range many {
		if len(result.StepResults) != 1 {
			t.Fatalf("example %d has %d steps, want 1", result.ID, len(result.StepResults))
		}
	}

	if queriesForOne != queriesForMany {
		t.Fatalf("ListExampleResults issued %d queries for 1 example and %d for %d: the per-example query is back",
			queriesForOne, queriesForMany, extra+1)
	}
}

func TestInvalidateMultipleUserCachesResolvesAgentsInOneQuery(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	counting := testutils.CountQueries(db)
	config := DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	service, err := NewPermissionService(counting, config)
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })

	insertUser := func(email string) int {
		t.Helper()
		return testutils.InsertID(t, db,
			`INSERT INTO users (email, username, first_name, last_name) VALUES (?, ?, ?, '')`,
			email, email, email)
	}
	insertAgent := func(ownerID int, email string) int {
		t.Helper()
		return testutils.InsertID(t, db,
			`INSERT INTO users (email, username, first_name, last_name, is_agent, agent_owner_user_id) VALUES (?, ?, ?, '', true, ?)`,
			email, email, email, ownerID)
	}

	firstOwner := insertUser("n1-owner@example.test")
	firstAgent := insertAgent(firstOwner, "n1-agent@example.test")

	// Prime the cache so the invalidation has something to drop.
	if err := service.storeUserPermissionCache(firstAgent, &models.UserPermissionCache{
		UserID:    firstAgent,
		ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatalf("store agent snapshot: %v", err)
	}

	counting.ResetQueries()
	if err := service.InvalidateMultipleUserCaches([]int{firstOwner}); err != nil {
		t.Fatalf("InvalidateMultipleUserCaches (one owner): %v", err)
	}
	queriesForOne := counting.Queries()

	if _, ok := service.cache.Get(firstAgent); ok {
		t.Fatal("the owner's agent snapshot survived invalidation")
	}

	owners := []int{firstOwner}
	const extraOwners = 9
	for i := 0; i < extraOwners; i++ {
		owner := insertUser(fmt.Sprintf("n1-owner-%d@example.test", i))
		insertAgent(owner, fmt.Sprintf("n1-agent-%d@example.test", i))
		owners = append(owners, owner)
	}

	counting.ResetQueries()
	if err := service.InvalidateMultipleUserCaches(owners); err != nil {
		t.Fatalf("InvalidateMultipleUserCaches (many owners): %v", err)
	}
	queriesForMany := counting.Queries()

	if queriesForOne != queriesForMany {
		t.Fatalf("invalidating 1 owner issued %d queries and %d owners issued %d: the per-owner agent lookup is back",
			queriesForOne, len(owners), queriesForMany)
	}
}
