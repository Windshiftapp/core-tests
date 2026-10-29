//go:build test

package database_test

import (
	"testing"

	"windshift/internal/testutils"
)

// TestQueuesMigrationUpgradesExistingSchema drops the queues table and its
// migration stamp to model the state an existing database reaches before the
// migration re-runs, then asserts the table and stamp are restored.
func TestQueuesMigrationUpgradesExistingSchema(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	for _, query := range []string{
		"DROP TABLE queues",
		"DELETE FROM schema_migrations WHERE version = '20261007_support_queues'",
	} {
		if _, err := db.ExecWrite(query); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := db.Initialize(); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.Query(`SELECT id, workspace_id, collection_id, name, ql_query, filter_state,
		position, created_by, builtin_key, is_hidden, created_at, updated_at FROM queues`)
	if err != nil {
		t.Fatalf("queues table was not restored: %v", err)
	}
	rows.Close()
	var stamps int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = '20261007_support_queues'").Scan(&stamps); err != nil {
		t.Fatal(err)
	}
	if stamps != 1 {
		t.Fatalf("migration stamps = %d, want 1", stamps)
	}
}

// TestQueuesCollectionScopeMigrationMakesWorkspaceNullable models an existing
// database whose queues.workspace_id is still NOT NULL and asserts the
// collection-scope migration rebuilds it so a collection that spans
// workspaces can own saved views.
func TestQueuesCollectionScopeMigrationMakesWorkspaceNullable(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	for _, query := range []string{
		"DROP TABLE queues",
		"DELETE FROM schema_migrations WHERE version = '20261007_support_queues'",
		"DELETE FROM schema_migrations WHERE version = '20261111_queues_collection_scope'",
	} {
		if _, err := db.ExecWrite(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Initialize(); err != nil {
		t.Fatal(err)
	}

	// A NULL workspace only succeeds once the NOT NULL constraint is gone.
	if _, err := db.ExecWrite(
		`INSERT INTO queues (workspace_id, collection_id, name, ql_query, position) VALUES (NULL, NULL, 'global view', 'id > 0', 0)`,
	); err != nil {
		t.Fatalf("insert queue with NULL workspace: %v", err)
	}

	var stamps int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = '20261111_queues_collection_scope'").Scan(&stamps); err != nil {
		t.Fatal(err)
	}
	if stamps != 1 {
		t.Fatalf("collection-scope migration stamps = %d, want 1", stamps)
	}
}
