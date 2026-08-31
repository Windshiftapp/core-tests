package services

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

type countingTransitionMatrixDatabase struct {
	database.Database
	queries atomic.Int64
	started chan struct{}
	release chan struct{}
	gate    sync.Once
}

func (db *countingTransitionMatrixDatabase) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	db.queries.Add(1)
	if db.started != nil {
		db.gate.Do(func() {
			close(db.started)
			select {
			case <-db.release:
			case <-ctx.Done():
			}
		})
	}
	return db.Database.QueryContext(ctx, query, args...)
}

func newTransitionMatrixTestDB(t *testing.T) database.Database {
	t.Helper()
	return testutils.CreateTestDB(t, true)
}

func insertTransitionMatrixRow(t *testing.T, db database.Database, query string, args ...any) int {
	t.Helper()
	var id int
	if err := db.QueryRow(query+" RETURNING id", args...).Scan(&id); err != nil {
		t.Fatalf("insert fixture row: %v\nquery: %s", err, query)
	}
	return id
}

type matrixFixture struct {
	workspaceID int
	itemTypeIDs []int
}

func seedTransitionMatrixFixture(t *testing.T, db database.Database, itemTypeCount, statusCount int) matrixFixture {
	t.Helper()
	workspaceID := insertTransitionMatrixRow(t, db,
		`INSERT INTO workspaces (name, key, description, active, is_personal) VALUES (?, ?, '', true, false)`,
		"Matrix fixture", "MXF",
	)
	workflowID := insertTransitionMatrixRow(t, db,
		`INSERT INTO workflows (name, description, is_default) VALUES (?, '', false)`, "Matrix workflow")
	configurationID := insertTransitionMatrixRow(t, db,
		`INSERT INTO configuration_sets (name, description, workflow_id) VALUES (?, '', ?)`, "Matrix configuration", workflowID)
	insertTransitionMatrixRow(t, db,
		`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, workspaceID, configurationID)
	categoryID := insertTransitionMatrixRow(t, db,
		`INSERT INTO status_categories (name, color, description) VALUES (?, '#123456', '')`, "Matrix category")

	statusIDs := make([]int, statusCount)
	for i := range statusCount {
		statusIDs[i] = insertTransitionMatrixRow(t, db,
			`INSERT INTO statuses (name, description, category_id) VALUES (?, '', ?)`, fmt.Sprintf("Matrix status %02d", i), categoryID)
	}
	for i, fromStatusID := range statusIDs {
		toStatusID := statusIDs[(i+1)%len(statusIDs)]
		insertTransitionMatrixRow(t, db, `
			INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, display_order)
			VALUES (?, ?, ?, ?)
		`, workflowID, fromStatusID, toStatusID, i)
	}

	itemTypeIDs := make([]int, itemTypeCount)
	for i := range itemTypeCount {
		itemTypeIDs[i] = insertTransitionMatrixRow(t, db, `
			INSERT INTO item_types (name, description, hierarchy_level, sort_order)
			VALUES (?, '', 3, ?)
		`, fmt.Sprintf("Matrix type %02d", i), i)
		insertTransitionMatrixRow(t, db, `
			INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id)
			VALUES (?, ?)
		`, configurationID, itemTypeIDs[i])
	}
	return matrixFixture{workspaceID: workspaceID, itemTypeIDs: itemTypeIDs}
}

func TestTransitionMatrixStatementCountIsIndependentOfCardinality(t *testing.T) {
	baseDB := newTransitionMatrixTestDB(t)
	fixture := seedTransitionMatrixFixture(t, baseDB, 25, 30)
	countingDB := &countingTransitionMatrixDatabase{Database: baseDB}
	service := NewTransitionMatrixService(countingDB)

	started := time.Now()
	matrix, err := service.Load(context.Background(), fixture.workspaceID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	elapsed := time.Since(started)
	if calls := countingDB.queries.Load(); calls != 3 {
		t.Fatalf("SQL statement count = %d, want 3", calls)
	}
	if matrix.SQLCount != 3 {
		t.Fatalf("reported SQL statement count = %d, want 3", matrix.SQLCount)
	}
	if matrix.ItemTypeCount != 25 || matrix.StatusCount != 30 || matrix.WorkflowCount != 1 {
		t.Fatalf("matrix cardinality = types:%d statuses:%d workflows:%d, want 25/30/1",
			matrix.ItemTypeCount, matrix.StatusCount, matrix.WorkflowCount)
	}
	if entries := len(matrix.ByItemType) * matrix.StatusCount; entries != 750 {
		t.Fatalf("matrix entries = %d, want 750", entries)
	}
	for _, itemTypeID := range fixture.itemTypeIDs {
		if len(matrix.ByItemType[itemTypeID]) != 30 {
			t.Fatalf("item type %d status count = %d, want 30", itemTypeID, len(matrix.ByItemType[itemTypeID]))
		}
	}
	if elapsed > time.Second {
		t.Fatalf("25x30 transition matrix exceeded 1s latency budget: %s", elapsed)
	}
	t.Logf("25x30 matrix loaded with %d SQL statements in %s", matrix.SQLCount, elapsed)
}

func TestTransitionMatrixFallbacksApplicabilityAndFreshness(t *testing.T) {
	db := newTransitionMatrixTestDB(t)
	if _, err := db.ExecWrite(`UPDATE workflows SET is_default = false`); err != nil {
		t.Fatalf("clear seeded default workflow: %v", err)
	}
	categoryID := insertTransitionMatrixRow(t, db,
		`INSERT INTO status_categories (name, color, description) VALUES ('Fallback category', '#654321', '')`)
	statusA := insertTransitionMatrixRow(t, db,
		`INSERT INTO statuses (name, description, category_id) VALUES ('Fallback A', '', ?)`, categoryID)
	statusB := insertTransitionMatrixRow(t, db,
		`INSERT INTO statuses (name, description, category_id) VALUES ('Fallback B', '', ?)`, categoryID)
	statusC := insertTransitionMatrixRow(t, db,
		`INSERT INTO statuses (name, description, category_id) VALUES ('Fallback C', '', ?)`, categoryID)
	statusD := insertTransitionMatrixRow(t, db,
		`INSERT INTO statuses (name, description, category_id) VALUES ('Fallback D', '', ?)`, categoryID)

	defaultWorkflow := insertTransitionMatrixRow(t, db,
		`INSERT INTO workflows (name, description, is_default) VALUES ('Config default workflow', '', false)`)
	overrideWorkflow := insertTransitionMatrixRow(t, db,
		`INSERT INTO workflows (name, description, is_default) VALUES ('Override workflow', '', false)`)
	globalWorkflow := insertTransitionMatrixRow(t, db,
		`INSERT INTO workflows (name, description, is_default) VALUES ('Global fallback workflow', '', true)`)
	insertTransitionMatrixRow(t, db, `INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id) VALUES (?, ?, ?)`, defaultWorkflow, statusA, statusB)
	insertTransitionMatrixRow(t, db, `INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id) VALUES (?, ?, ?)`, overrideWorkflow, statusA, statusC)
	insertTransitionMatrixRow(t, db, `INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id) VALUES (?, ?, ?)`, overrideWorkflow, statusC, statusD)
	insertTransitionMatrixRow(t, db, `INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id) VALUES (?, ?, ?)`, globalWorkflow, statusA, statusD)

	defaultType := insertTransitionMatrixRow(t, db, `INSERT INTO item_types (name, description) VALUES ('Fallback default type', '')`)
	overrideType := insertTransitionMatrixRow(t, db, `INSERT INTO item_types (name, description) VALUES ('Fallback override type', '')`)
	globalType := insertTransitionMatrixRow(t, db, `INSERT INTO item_types (name, description) VALUES ('Fallback global type', '')`)
	unlinkedType := insertTransitionMatrixRow(t, db, `INSERT INTO item_types (name, description) VALUES ('Fallback unlinked type', '')`)

	configWithDefault := insertTransitionMatrixRow(t, db,
		`INSERT INTO configuration_sets (name, description, workflow_id) VALUES ('Fallback config default', '', ?)`, defaultWorkflow)
	configWithGlobalFallback := insertTransitionMatrixRow(t, db,
		`INSERT INTO configuration_sets (name, description, workflow_id) VALUES ('Fallback config global', '', NULL)`)
	insertTransitionMatrixRow(t, db, `INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id) VALUES (?, ?)`, configWithDefault, defaultType)
	insertTransitionMatrixRow(t, db, `INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id, workflow_id) VALUES (?, ?, ?)`, configWithDefault, overrideType, overrideWorkflow)
	insertTransitionMatrixRow(t, db, `INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id) VALUES (?, ?)`, configWithGlobalFallback, globalType)

	workspaceDefault := insertTransitionMatrixRow(t, db,
		`INSERT INTO workspaces (name, key, description, active, is_personal) VALUES ('Fallback workspace', 'FBW', '', true, false)`)
	workspaceGlobal := insertTransitionMatrixRow(t, db,
		`INSERT INTO workspaces (name, key, description, active, is_personal) VALUES ('Global workspace', 'GLW', '', true, false)`)
	workspacePersonal := insertTransitionMatrixRow(t, db,
		`INSERT INTO workspaces (name, key, description, active, is_personal) VALUES ('Personal workspace', 'PSW', '', true, true)`)
	insertTransitionMatrixRow(t, db, `INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, workspaceDefault, configWithDefault)
	insertTransitionMatrixRow(t, db, `INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, workspaceGlobal, configWithGlobalFallback)
	insertTransitionMatrixRow(t, db, `INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, workspacePersonal, configWithDefault)

	service := NewTransitionMatrixService(db)
	matrix, err := service.Load(context.Background(), workspaceDefault)
	if err != nil {
		t.Fatalf("Load config-default workspace: %v", err)
	}
	assertTransitionDestination(t, matrix, defaultType, statusA, statusB)
	assertTransitionDestination(t, matrix, overrideType, statusA, statusC)
	assertTransitionDestination(t, matrix, overrideType, statusC, statusD)
	if _, exists := matrix.ByItemType[unlinkedType]; exists {
		t.Fatalf("unlinked item type %d was included", unlinkedType)
	}

	globalMatrix, err := service.Load(context.Background(), workspaceGlobal)
	if err != nil {
		t.Fatalf("Load global-fallback workspace: %v", err)
	}
	assertTransitionDestination(t, globalMatrix, globalType, statusA, statusD)

	personalMatrix, err := service.Load(context.Background(), workspacePersonal)
	if err != nil {
		t.Fatalf("Load personal workspace: %v", err)
	}
	if len(personalMatrix.ByItemType) != 0 || personalMatrix.SQLCount != 1 {
		t.Fatalf("personal matrix = types:%d SQL:%d, want 0/1", len(personalMatrix.ByItemType), personalMatrix.SQLCount)
	}

	// No persistent cache means a workflow mutation is visible immediately.
	if _, err := db.ExecWrite(`DELETE FROM workflow_transitions WHERE workflow_id = ? AND from_status_id = ?`, defaultWorkflow, statusA); err != nil {
		t.Fatalf("delete default transition: %v", err)
	}
	insertTransitionMatrixRow(t, db, `INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id) VALUES (?, ?, ?)`, defaultWorkflow, statusA, statusD)
	refreshed, err := service.Load(context.Background(), workspaceDefault)
	if err != nil {
		t.Fatalf("Load refreshed workspace: %v", err)
	}
	assertTransitionDestination(t, refreshed, defaultType, statusA, statusD)
}

func TestTransitionMatrixUnconfiguredWorkspaceUsesGlobalCatalogAndDefaultWorkflow(t *testing.T) {
	db := newTransitionMatrixTestDB(t)
	workspaceID := insertTransitionMatrixRow(t, db,
		`INSERT INTO workspaces (name, key, description, active, is_personal) VALUES ('Unconfigured workspace', 'UCW', '', true, false)`)

	var itemTypeID, itemTypeCount int
	if err := db.QueryRow(`SELECT id FROM item_types ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("load global item type: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_types`).Scan(&itemTypeCount); err != nil {
		t.Fatalf("count global item types: %v", err)
	}

	var fromStatusID, toStatusID int
	if err := db.QueryRow(`
		SELECT wt.from_status_id, wt.to_status_id
		FROM workflow_transitions wt
		JOIN workflows w ON w.id = wt.workflow_id
		WHERE w.is_default = true AND wt.from_status_id IS NOT NULL
		ORDER BY wt.display_order, wt.id
		LIMIT 1
	`).Scan(&fromStatusID, &toStatusID); err != nil {
		t.Fatalf("load default workflow transition: %v", err)
	}

	matrix, err := NewTransitionMatrixService(db).Load(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("Load unconfigured workspace: %v", err)
	}
	if matrix.ItemTypeCount != itemTypeCount {
		t.Fatalf("matrix item type count = %d, want global catalog count %d", matrix.ItemTypeCount, itemTypeCount)
	}
	assertTransitionDestination(t, matrix, itemTypeID, fromStatusID, toStatusID)
}

func TestTransitionMatrixConcurrentColdLoadsAreSingleflighted(t *testing.T) {
	baseDB := newTransitionMatrixTestDB(t)
	fixture := seedTransitionMatrixFixture(t, baseDB, 2, 3)
	countingDB := &countingTransitionMatrixDatabase{
		Database: baseDB,
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	service := NewTransitionMatrixService(countingDB)

	type result struct {
		matrix *WorkspaceTransitionMatrix
		err    error
	}
	results := make(chan result, 2)
	go func() {
		matrix, err := service.Load(context.Background(), fixture.workspaceID)
		results <- result{matrix: matrix, err: err}
	}()
	select {
	case <-countingDB.started:
	case <-time.After(time.Second):
		t.Fatal("first matrix load did not reach the database")
	}
	go func() {
		matrix, err := service.Load(context.Background(), fixture.workspaceID)
		results <- result{matrix: matrix, err: err}
	}()
	deadline := time.Now().Add(time.Second)
	for service.Stats().Requests < 2 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	close(countingDB.release)

	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent Load: %v", result.err)
		}
		if result.matrix.ItemTypeCount != 2 || result.matrix.StatusCount != 3 {
			t.Fatalf("concurrent matrix cardinality = %d/%d, want 2/3", result.matrix.ItemTypeCount, result.matrix.StatusCount)
		}
	}
	if calls := countingDB.queries.Load(); calls != 3 {
		t.Fatalf("concurrent SQL statement count = %d, want one three-query load", calls)
	}
	stats := service.Stats()
	if stats.Requests != 2 || stats.DatabaseLoads != 1 || stats.CoalescedResponses != 2 {
		t.Fatalf("singleflight stats = requests:%d loads:%d coalesced:%d, want 2/1/2",
			stats.Requests, stats.DatabaseLoads, stats.CoalescedResponses)
	}
}

func assertTransitionDestination(t *testing.T, matrix *WorkspaceTransitionMatrix, itemTypeID, fromStatusID, wantStatusID int) {
	t.Helper()
	options, exists := matrix.ByItemType[itemTypeID][fromStatusID]
	if !exists {
		t.Fatalf("matrix missing item type/status %d:%d", itemTypeID, fromStatusID)
	}
	if len(options) < 2 {
		t.Fatalf("matrix options for %d:%d = %+v, want current plus transition", itemTypeID, fromStatusID, options)
	}
	if options[0].StatusID != fromStatusID || options[1].StatusID != wantStatusID {
		t.Fatalf("matrix options for %d:%d = [%d, %d], want [%d, %d]",
			itemTypeID, fromStatusID, options[0].StatusID, options[1].StatusID, fromStatusID, wantStatusID)
	}
}
