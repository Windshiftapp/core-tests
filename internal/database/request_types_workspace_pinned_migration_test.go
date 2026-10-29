//go:build test

package database_test

import (
	"database/sql"
	"testing"

	"windshift/internal/testutils"
)

// seedLegacyRequestTypes creates the pre-WI-1695 request_types shape: a
// nullable workspace_id with an ON DELETE SET NULL workspace FK. Direct SQL is
// required because neither the API nor the fresh schema can produce this
// pre-migration shape anymore.
func seedLegacyRequestTypes(t *testing.T, tdb *testutils.TestDB) {
	t.Helper()

	idColumn := "INTEGER PRIMARY KEY AUTOINCREMENT"
	createdAt := "DATETIME DEFAULT CURRENT_TIMESTAMP"
	updatedAt := "DATETIME DEFAULT CURRENT_TIMESTAMP"
	if testutils.IsPostgres() {
		idColumn = "SERIAL PRIMARY KEY"
		createdAt = "TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP"
		updatedAt = "TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP"
	}

	statements := []string{
		`CREATE TABLE channels (id ` + idColumn + `, type TEXT NOT NULL DEFAULT 'portal', config TEXT)`,
		`CREATE TABLE item_types (id ` + idColumn + `)`,
		`CREATE TABLE request_types (
			id ` + idColumn + `,
			channel_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			item_type_id INTEGER NOT NULL,
			icon TEXT DEFAULT 'FileText',
			color TEXT DEFAULT '#6b7280',
			display_order INTEGER DEFAULT 0,
			is_active BOOLEAN DEFAULT TRUE,
			config TEXT DEFAULT NULL,
			visibility_group_ids TEXT DEFAULT NULL,
			visibility_org_ids TEXT DEFAULT NULL,
			workspace_id INTEGER DEFAULT NULL,
			title_template TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT '',
			created_at ` + createdAt + `,
			updated_at ` + updatedAt + `,
			FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE SET NULL
		)`,
		`INSERT INTO workspaces (id) VALUES (1), (2)`,
		`INSERT INTO item_types (id) VALUES (1)`,
		`INSERT INTO channels (id, type, config) VALUES
			(10, 'portal', '{"portal_workspace_ids":[1,2]}'),
			(11, 'portal', '{"portal_workspace_ids":[]}')`,
		`INSERT INTO request_types (id, channel_id, name, item_type_id, workspace_id) VALUES
			(100, 10, 'Unpinned multi-workspace route', 1, NULL),
			(101, 11, 'Unreachable route', 1, NULL),
			(102, 10, 'Already pinned route', 1, 2)`,
	}
	for _, statement := range statements {
		if _, err := tdb.Exec(statement); err != nil {
			t.Fatalf("seed legacy request_types with %q: %v", statement, err)
		}
	}
}

func TestRequestTypesWorkspacePinnedMigration_BackfillsAndCascades(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)
	seedCanonicalSchemaCheckpoint(t, tdb)
	seedMinimalWorkspaces(t, tdb)
	seedLegacyRequestTypes(t, tdb)

	createSchemaMigrationsTable(t, tdb)
	stampAllExcept(t, tdb, "20261022_request_types_workspace_pinned", "")

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run request_types workspace migration: %v", err)
	}

	// An unpinned route on a multi-workspace channel keeps today's behavior by
	// pinning the first configured workspace.
	var backfilled int
	if err := tdb.QueryRow(`SELECT workspace_id FROM request_types WHERE id = 100`).Scan(&backfilled); err != nil {
		t.Fatalf("read backfilled workspace: %v", err)
	}
	if backfilled != 1 {
		t.Fatalf("backfilled workspace = %d, want first configured workspace 1", backfilled)
	}

	// An already-pinned route is untouched.
	var pinned int
	if err := tdb.QueryRow(`SELECT workspace_id FROM request_types WHERE id = 102`).Scan(&pinned); err != nil {
		t.Fatalf("read pinned workspace: %v", err)
	}
	if pinned != 2 {
		t.Fatalf("pinned workspace = %d, want 2", pinned)
	}

	// A route whose channel serves no workspace has no workspace to pin. It is
	// preserved with a NULL binding and rejected at runtime, not deleted.
	var orphan sql.NullInt64
	if err := tdb.QueryRow(`SELECT workspace_id FROM request_types WHERE id = 101`).Scan(&orphan); err != nil {
		t.Fatalf("read orphan route: %v", err)
	}
	if orphan.Valid {
		t.Fatalf("orphan workspace = %d, want NULL", orphan.Int64)
	}

	// Deleting the workspace removes its route instead of nulling it.
	if _, err := tdb.Exec(`DELETE FROM workspaces WHERE id = 1`); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
	if n := countFixtureRows(t, tdb, `SELECT COUNT(*) FROM request_types WHERE id = 100`); n != 0 {
		t.Fatalf("route rows after workspace delete = %d, want 0 (cascade)", n)
	}

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}
	var stamps int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version='20261022_request_types_workspace_pinned'`).Scan(&stamps); err != nil || stamps != 1 {
		t.Fatalf("migration stamps = %d, %v; want 1", stamps, err)
	}
}
