//go:build test

package database_test

import (
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// seedCanonicalSchemaCheckpoint installs the minimum state the Initialize
// preflight (ValidateCanonicalSchemaCheckpoint) requires of an existing
// database: the four core probe tables, the canonical items rank shape, the
// 0.8.5 schema_checkpoint row, and a stable global_rank_state row. Direct SQL
// is appropriate here because the API cannot intentionally create this
// pre-upgrade state under test.
func seedCanonicalSchemaCheckpoint(t *testing.T, tdb *testutils.TestDB) {
	t.Helper()

	statements := []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE workflows (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workspace_id INTEGER NOT NULL,
			parent_id INTEGER,
			frac_index TEXT COLLATE BINARY NOT NULL
		)`,
		`CREATE UNIQUE INDEX idx_items_frac_index ON items(frac_index)`,
		`CREATE INDEX idx_items_workspace_frac_index ON items(workspace_id, frac_index)`,
		`CREATE INDEX idx_items_workspace_parent_frac_index ON items(workspace_id, parent_id, frac_index)`,
		`CREATE TABLE schema_checkpoint (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			version TEXT NOT NULL,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO schema_checkpoint (id, version) VALUES (1, '0.8.5')`,
		`CREATE TABLE global_rank_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			active_bucket INTEGER NOT NULL,
			target_bucket INTEGER,
			phase TEXT NOT NULL,
			direction TEXT,
			frontier TEXT,
			lease_owner TEXT,
			lease_expires_at DATETIME,
			migrated_count INTEGER NOT NULL DEFAULT 0,
			total_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO global_rank_state (id, active_bucket, phase) VALUES (1, 0, 'stable')`,
	}
	if testutils.IsPostgres() {
		statements = []string{
			`CREATE TABLE users (id SERIAL PRIMARY KEY)`,
			`CREATE TABLE workflows (id SERIAL PRIMARY KEY)`,
			`CREATE TABLE items (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL,
				parent_id INTEGER,
				frac_index TEXT COLLATE "C" NOT NULL
			)`,
			`CREATE UNIQUE INDEX idx_items_frac_index ON items(frac_index)`,
			`CREATE INDEX idx_items_workspace_frac_index ON items(workspace_id, frac_index)`,
			`CREATE INDEX idx_items_workspace_parent_frac_index ON items(workspace_id, parent_id, frac_index)`,
			`CREATE TABLE schema_checkpoint (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				version TEXT NOT NULL,
				applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`INSERT INTO schema_checkpoint (id, version) VALUES (1, '0.8.5')`,
			`CREATE TABLE global_rank_state (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				active_bucket INTEGER NOT NULL,
				target_bucket INTEGER,
				phase TEXT NOT NULL,
				direction TEXT,
				frontier TEXT,
				lease_owner TEXT,
				lease_expires_at TIMESTAMPTZ,
				migrated_count BIGINT NOT NULL DEFAULT 0,
				total_count BIGINT NOT NULL DEFAULT 0,
				last_error TEXT,
				updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`INSERT INTO global_rank_state (id, active_bucket, phase) VALUES (1, 0, 'stable')`,
		}
	}

	for _, statement := range statements {
		if _, err := tdb.Exec(statement); err != nil {
			t.Fatalf("seed canonical checkpoint state with %q: %v", statement, err)
		}
	}
}

// createSchemaMigrationsTable creates the runner's bootstrap table so tests
// can control exactly which catalog rows are pre-stamped.
func createSchemaMigrationsTable(t *testing.T, tdb *testutils.TestDB) {
	t.Helper()
	datetimeType := "DATETIME"
	if testutils.IsPostgres() {
		datetimeType = "TIMESTAMPTZ"
	}
	if _, err := tdb.Exec(`CREATE TABLE schema_migrations (
		version TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL DEFAULT '',
		applied_at ` + datetimeType + ` NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
}

// stampAllExcept stamps every catalog row except the given version with the
// provided checksum so only the excluded migration runs against the fixture.
func stampAllExcept(t *testing.T, tdb *testutils.TestDB, exceptVersion, checksum string) {
	t.Helper()
	for _, migration := range database.Catalog {
		if migration.Version == exceptVersion {
			continue
		}
		if _, err := tdb.Exec(
			`INSERT INTO schema_migrations(version, name, checksum) VALUES (?, ?, ?)`,
			migration.Version, migration.Name, checksum,
		); err != nil {
			t.Fatalf("stamp migration %s: %v", migration.Version, err)
		}
	}
}
