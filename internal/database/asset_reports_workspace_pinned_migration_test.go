//go:build test

package database_test

import (
	"database/sql"
	"testing"

	"windshift/internal/testutils"
)

// seedLegacyAssetReports creates the pre-WI-1697 asset_reports shape: nullable
// item_type_id and workspace_id with ON DELETE SET NULL FKs. Direct SQL is
// required because neither the API nor the fresh schema can produce this
// pre-migration shape anymore.
func seedLegacyAssetReports(t *testing.T, tdb *testutils.TestDB) {
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
		`CREATE TABLE asset_management_sets (id ` + idColumn + `)`,
		`CREATE TABLE asset_reports (
			id ` + idColumn + `,
			channel_id INTEGER NOT NULL,
			asset_set_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			cql_query TEXT DEFAULT '',
			icon TEXT DEFAULT 'Table2',
			color TEXT DEFAULT '#6b7280',
			display_order INTEGER DEFAULT 0,
			is_active BOOLEAN DEFAULT TRUE,
			column_config TEXT DEFAULT NULL,
			visibility_group_ids TEXT DEFAULT NULL,
			visibility_org_ids TEXT DEFAULT NULL,
			run_mode TEXT NOT NULL DEFAULT 'direct',
			item_type_id INTEGER DEFAULT NULL,
			workspace_id INTEGER DEFAULT NULL,
			config TEXT DEFAULT NULL,
			created_at ` + createdAt + `,
			updated_at ` + updatedAt + `,
			FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE,
			FOREIGN KEY (asset_set_id) REFERENCES asset_management_sets(id) ON DELETE CASCADE,
			FOREIGN KEY (item_type_id) REFERENCES item_types(id) ON DELETE SET NULL,
			FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE SET NULL
		)`,
		`INSERT INTO workspaces (id) VALUES (1), (2)`,
		`INSERT INTO item_types (id) VALUES (1)`,
		`INSERT INTO asset_management_sets (id) VALUES (1)`,
		`INSERT INTO channels (id, type, config) VALUES
			(10, 'portal', '{"portal_workspace_ids":[1,2]}'),
			(11, 'portal', '{"portal_workspace_ids":[]}')`,
		`INSERT INTO asset_reports (id, channel_id, asset_set_id, name, run_mode, item_type_id, workspace_id) VALUES
			(200, 10, 1, 'Unpinned form report', 'form', 1, NULL),
			(201, 10, 1, 'Direct report', 'direct', NULL, NULL),
			(202, 10, 1, 'Pinned form report', 'form', 1, 2),
			(203, 11, 1, 'Unreachable form report', 'form', 1, NULL)`,
	}
	for _, statement := range statements {
		if _, err := tdb.Exec(statement); err != nil {
			t.Fatalf("seed legacy asset_reports with %q: %v", statement, err)
		}
	}
}

func TestAssetReportsWorkspacePinnedMigration_BackfillsAndCascades(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)
	seedCanonicalSchemaCheckpoint(t, tdb)
	seedMinimalWorkspaces(t, tdb)
	seedLegacyAssetReports(t, tdb)

	createSchemaMigrationsTable(t, tdb)
	stampAllExcept(t, tdb, "20261023_asset_reports_workspace_pinned", "")

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run asset_reports workspace migration: %v", err)
	}

	// An unpinned form-mode report is pinned to the first served workspace.
	var backfilled int
	if err := tdb.QueryRow(`SELECT workspace_id FROM asset_reports WHERE id = 200`).Scan(&backfilled); err != nil {
		t.Fatalf("read backfilled workspace: %v", err)
	}
	if backfilled != 1 {
		t.Fatalf("backfilled workspace = %d, want first served workspace 1", backfilled)
	}

	// Direct reports keep their NULL binding.
	var direct sql.NullInt64
	if err := tdb.QueryRow(`SELECT workspace_id FROM asset_reports WHERE id = 201`).Scan(&direct); err != nil {
		t.Fatalf("read direct report: %v", err)
	}
	if direct.Valid {
		t.Fatalf("direct report workspace = %d, want NULL", direct.Int64)
	}

	// A report whose channel serves no workspace stays NULL and is rejected at
	// runtime rather than deleted.
	var orphan sql.NullInt64
	if err := tdb.QueryRow(`SELECT workspace_id FROM asset_reports WHERE id = 203`).Scan(&orphan); err != nil {
		t.Fatalf("read orphan report: %v", err)
	}
	if orphan.Valid {
		t.Fatalf("orphan workspace = %d, want NULL", orphan.Int64)
	}

	// Deleting the workspace removes its reports instead of nulling them.
	if _, err := tdb.Exec(`DELETE FROM workspaces WHERE id = 2`); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
	if n := countFixtureRows(t, tdb, `SELECT COUNT(*) FROM asset_reports WHERE id = 202`); n != 0 {
		t.Fatalf("pinned report rows after workspace delete = %d, want 0 (cascade)", n)
	}

	// Deleting a referenced item type is refused.
	if _, err := tdb.Exec(`DELETE FROM item_types WHERE id = 1`); err == nil {
		t.Fatal("deleting a referenced item type succeeded; want RESTRICT violation")
	}

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}
	var stamps int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version='20261023_asset_reports_workspace_pinned'`).Scan(&stamps); err != nil || stamps != 1 {
		t.Fatalf("migration stamps = %d, %v; want 1", stamps, err)
	}
}
