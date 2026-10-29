//go:build test

package database_test

import (
	"testing"

	"windshift/internal/testutils"
)

// Regression guard for issue #257: a schema file that references a table
// before creating it fails fresh initialization on PostgreSQL only (SQLite
// resolves forward FK references lazily), and the failure only shows up on
// brand-new deploys — existing databases never re-run the schema files.
// This test initializes a pristine database from the embedded schema files
// on the engine under test and asserts the deepest FK-dependent tables
// landed, so a schema-ordering regression fails CI instead of a user's
// first docker-compose up.
func TestFreshSchemaInitialization(t *testing.T) {
	// Database-initialization test: driving the raw production schema through
	// Initialize() is the behavior under test, so the no-init fixture is the
	// documented exception to the CreateTestDB default.
	tdb := testutils.CreateFreshDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("fresh database initialization failed (check schema file FK ordering): %v", err)
	}

	// Tables created late in the dependency chain, including the run-scoped
	// BDD tables whose forward FK references broke #257.
	for _, table := range []string{
		"workspaces",
		"items",
		"test_runs",
		"test_run_case_snapshots",
		"test_example_results",
		"test_example_step_results",
	} {
		tdb.AssertTableExists(t, table)
	}

	// Cross-file foreign keys must resolve: workspaces comes from
	// workspace_postgres.sql while test_folders comes from
	// tests_postgres.sql.
	workspaceID := testutils.InsertID(t, tdb.Database, `
		INSERT INTO workspaces (name, key) VALUES ('Smoke', 'SMOKE')
	`)
	if _, err := tdb.ExecWrite(`
		INSERT INTO test_folders (workspace_id, name) VALUES (?, 'Smoke folder')
	`, workspaceID); err != nil {
		t.Fatalf("insert test_folder across schema files: %v", err)
	}
}
