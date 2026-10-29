//go:build test

package database_test

import (
	"slices"
	"sort"
	"strconv"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// The fixture below recreates the shape migration 20260924_generic_import_jobs
// leaves on databases upgraded from 0.8.8/0.8.9: both generic import tables
// still carry the original set_id → asset_management_sets foreign key under
// the renamed scope_id column. Direct SQL is required because the API cannot
// intentionally produce a stale foreign key; this is a migration test.
func seedImportTablesWithStaleAssetSetFK(t *testing.T, tdb *testutils.TestDB) (workspaceID int) {
	t.Helper()

	createdAtType := "DATETIME"
	if testutils.IsPostgres() {
		createdAtType = "TIMESTAMPTZ"
	}
	statements := []string{
		`CREATE TABLE asset_management_sets (id INTEGER PRIMARY KEY, name TEXT NOT NULL, created_by INTEGER REFERENCES users(id))`,
		`CREATE TABLE import_jobs (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL DEFAULT 'asset',
			scope_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'queued',
			phase TEXT DEFAULT 'initializing',
			file_path TEXT NOT NULL,
			config_json TEXT,
			progress_json TEXT,
			error_message TEXT,
			created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
			created_at ` + createdAtType + ` DEFAULT CURRENT_TIMESTAMP,
			started_at ` + createdAtType + `,
			completed_at ` + createdAtType + `,
			lease_expires_at BIGINT,
			FOREIGN KEY (scope_id) REFERENCES asset_management_sets(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE import_uploads (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL DEFAULT 'asset',
			scope_id INTEGER NOT NULL,
			created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at BIGINT NOT NULL,
			FOREIGN KEY (scope_id) REFERENCES asset_management_sets(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_import_jobs_scope ON import_jobs(scope_id)`,
		`CREATE INDEX idx_import_jobs_status ON import_jobs(status)`,
		`CREATE INDEX idx_import_jobs_created_by ON import_jobs(created_by)`,
	}
	for _, statement := range statements {
		if _, err := tdb.Exec(statement); err != nil {
			t.Fatalf("seed stale-FK import tables with %q: %v", statement, err)
		}
	}

	// Workspace id collides with the asset set id on purpose: before the
	// foreign key is dropped, deleting the set cascade-deletes the
	// workspace's ticket import rows.
	workspaceID = 41
	if _, err := tdb.Exec(`INSERT INTO users (id) VALUES (1)`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO workspaces (id) VALUES (?)`, workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO asset_management_sets (id, name, created_by) VALUES (?, 'import fk set', 1)`, workspaceID); err != nil {
		t.Fatalf("seed asset set: %v", err)
	}
	if _, err := tdb.Exec(
		`INSERT INTO import_jobs (id, kind, scope_id, file_path, created_by) VALUES ('job-ticket', 'ticket', ?, 'tickets.csv', 1)`,
		workspaceID,
	); err != nil {
		t.Fatalf("seed ticket import job: %v", err)
	}
	if _, err := tdb.Exec(
		`INSERT INTO import_uploads (id, kind, scope_id, created_by, created_at) VALUES ('upload-ticket', 'ticket', ?, 1, 1727300000)`,
		workspaceID,
	); err != nil {
		t.Fatalf("seed ticket import upload: %v", err)
	}
	return workspaceID
}

func runImportJobsScopeFKMigration(t *testing.T, tdb *testutils.TestDB) {
	t.Helper()
	createSchemaMigrationsTable(t, tdb)
	stampAllExcept(t, tdb, "20261004_import_jobs_scope_fk", "")
	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run import_jobs scope FK migration: %v", err)
	}
	if err := tdb.Initialize(); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}
}

func staleAssetSetFKCount(t *testing.T, tdb *testutils.TestDB, table string) int {
	t.Helper()
	query := `SELECT COUNT(*) FROM pragma_foreign_key_list('` + table + `') WHERE "table"='asset_management_sets'`
	if testutils.IsPostgres() {
		query = `SELECT COUNT(*) FROM pg_constraint con
			JOIN pg_class rel ON rel.oid = con.conrelid
			JOIN pg_class parent ON parent.oid = con.confrelid
			JOIN pg_namespace n ON n.oid = rel.relnamespace
			WHERE con.contype = 'f' AND n.nspname = current_schema()
			  AND rel.relname = '` + table + `' AND parent.relname = 'asset_management_sets'`
	}
	return countFixtureRows(t, tdb, query)
}

func TestMigration_ImportJobsScopeFK_DropsStaleAssetSetForeignKey(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)
	seedCanonicalSchemaCheckpoint(t, tdb)
	seedMinimalWorkspaces(t, tdb)
	workspaceID := seedImportTablesWithStaleAssetSetFK(t, tdb)

	for _, table := range []string{"import_jobs", "import_uploads"} {
		if got := staleAssetSetFKCount(t, tdb, table); got != 1 {
			t.Fatalf("fixture %s asset-set FK count = %d, want 1 (fixture must model the upgraded schema)", table, got)
		}
	}

	runImportJobsScopeFKMigration(t, tdb)

	for _, table := range []string{"import_jobs", "import_uploads"} {
		if got := staleAssetSetFKCount(t, tdb, table); got != 0 {
			t.Fatalf("%s still carries %d asset-set foreign key(s) after the migration, want 0", table, got)
		}
	}

	// The migrated rows survive the table rebuild.
	scope := strconv.Itoa(workspaceID)
	if got := countFixtureRows(t, tdb, `SELECT COUNT(*) FROM import_jobs WHERE kind='ticket' AND scope_id=`+scope); got != 1 {
		t.Fatalf("ticket import jobs after migration = %d, want 1", got)
	}
	if got := countFixtureRows(t, tdb, `SELECT COUNT(*) FROM import_uploads WHERE kind='ticket' AND scope_id=`+scope); got != 1 {
		t.Fatalf("ticket import uploads after migration = %d, want 1", got)
	}

	// Ticket-shaped rows (scope_id = workspace id) must now insert cleanly.
	if _, err := tdb.Exec(
		`INSERT INTO import_jobs (id, kind, scope_id, file_path, created_by) VALUES ('job-ticket-2', 'ticket', ?, 'more.csv', 1)`,
		workspaceID,
	); err != nil {
		t.Fatalf("ticket-shaped import_jobs insert after migration: %v", err)
	}
	if _, err := tdb.Exec(
		`INSERT INTO import_uploads (id, kind, scope_id, created_by, created_at) VALUES ('upload-ticket-2', 'ticket', ?, 1, 1727300001)`,
		workspaceID,
	); err != nil {
		t.Fatalf("ticket-shaped import_uploads insert after migration: %v", err)
	}

	// Deleting the asset set must no longer cascade into the workspace's
	// import rows despite the colliding numeric id.
	if _, err := tdb.Exec(`DELETE FROM asset_management_sets WHERE id = ?`, workspaceID); err != nil {
		t.Fatalf("delete asset set: %v", err)
	}
	if got := countFixtureRows(t, tdb, `SELECT COUNT(*) FROM import_jobs WHERE scope_id=`+scope); got != 2 {
		t.Fatalf("import jobs after asset set deletion = %d, want 2 (no cascade)", got)
	}
	if got := countFixtureRows(t, tdb, `SELECT COUNT(*) FROM import_uploads WHERE scope_id=`+scope); got != 2 {
		t.Fatalf("import uploads after asset set deletion = %d, want 2 (no cascade)", got)
	}

	// The rebuilt jobs table keeps its indexes.
	indexQuery := `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name='import_jobs' AND name IN ('idx_import_jobs_scope','idx_import_jobs_status','idx_import_jobs_created_by')`
	if testutils.IsPostgres() {
		indexQuery = `SELECT COUNT(*) FROM pg_indexes WHERE schemaname=current_schema() AND tablename='import_jobs'
			AND indexname IN ('idx_import_jobs_scope','idx_import_jobs_status','idx_import_jobs_created_by')`
	}
	if got := countFixtureRows(t, tdb, indexQuery); got != 3 {
		t.Fatalf("import_jobs indexes after migration = %d, want 3", got)
	}

	var stamps int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version='20261004_import_jobs_scope_fk'`).Scan(&stamps); err != nil {
		t.Fatal(err)
	}
	if stamps != 1 {
		t.Fatalf("migration stamps = %d, want 1", stamps)
	}
}

// TestMigration_ImportJobsScopeFK_FreshInstallsStampWithoutRunning asserts the
// migration's check reports the effect present on fresh installs, so the
// runner stamps the row instead of rebuilding tables that never had the FK.
func TestMigration_ImportJobsScopeFK_FreshInstallsStampWithoutRunning(t *testing.T) {
	fresh := testutils.CreateTestDB(t, false)
	t.Cleanup(func() { _ = fresh.Close() })

	var migration *database.Migration
	for i := range database.Catalog {
		if database.Catalog[i].Version == "20261004_import_jobs_scope_fk" {
			migration = &database.Catalog[i]
			break
		}
	}
	if migration == nil {
		t.Fatal("20261004_import_jobs_scope_fk missing from the migration catalog")
	}

	checkQuery := migration.CheckSQLite
	if testutils.IsPostgres() {
		checkQuery = migration.CheckPostgres
	}
	var applied int
	if err := fresh.QueryRow(checkQuery).Scan(&applied); err != nil {
		t.Fatalf("run fresh-install check: %v", err)
	}
	if applied != 1 {
		t.Fatalf("fresh-install check reported %d, want 1 (effect already present)", applied)
	}

	for _, table := range []string{"import_jobs", "import_uploads"} {
		if got := staleAssetSetFKCount(t, fresh, table); got != 0 {
			t.Fatalf("fresh install %s carries %d asset-set foreign key(s), want 0", table, got)
		}
	}

	var stamps int
	if err := fresh.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version='20261004_import_jobs_scope_fk'`).Scan(&stamps); err != nil {
		t.Fatal(err)
	}
	if stamps != 1 {
		t.Fatalf("fresh-install migration stamps = %d, want 1", stamps)
	}
}

// TestSchema_ImportTablesFreshAndMigratedMatch asserts that a database which
// went through the stale-FK state and the fix ends up with exactly the same
// import_jobs/import_uploads shape as a fresh install.
func TestSchema_ImportTablesFreshAndMigratedMatch(t *testing.T) {
	migrated := testutils.CreateFreshDB(t, false)
	t.Cleanup(func() { _ = migrated.Close() })
	seedCanonicalSchemaCheckpoint(t, migrated)
	seedMinimalWorkspaces(t, migrated)
	seedImportTablesWithStaleAssetSetFK(t, migrated)
	runImportJobsScopeFKMigration(t, migrated)

	fresh := testutils.CreateTestDB(t, false)
	t.Cleanup(func() { _ = fresh.Close() })

	for _, table := range []string{"import_jobs", "import_uploads"} {
		wantColumns := importTableColumns(t, fresh, table)
		gotColumns := importTableColumns(t, migrated, table)
		if !slices.Equal(wantColumns, gotColumns) {
			t.Fatalf("%s columns diverge between fresh and migrated:\nfresh:    %v\nmigrated: %v", table, wantColumns, gotColumns)
		}
		wantIndexes := importTableIndexes(t, fresh, table)
		gotIndexes := importTableIndexes(t, migrated, table)
		if !slices.Equal(wantIndexes, gotIndexes) {
			t.Fatalf("%s indexes diverge between fresh and migrated:\nfresh:    %v\nmigrated: %v", table, wantIndexes, gotIndexes)
		}
	}
}

// importTableColumns returns normalized "name|type|notnull|default|pk"
// entries for one table. Both sides come from the same engine, so raw type
// spellings compare equal without cross-engine translation.
func importTableColumns(t *testing.T, tdb *testutils.TestDB, table string) []string {
	t.Helper()
	query := `SELECT name || '|' || type || '|' || CAST("notnull" AS TEXT) || '|' || COALESCE(dflt_value,'') || '|' || CAST(pk AS TEXT)
		FROM pragma_table_info('` + table + `') ORDER BY cid`
	if testutils.IsPostgres() {
		query = `SELECT column_name || '|' || data_type || '|' || is_nullable || '|' || COALESCE(column_default,'') || '|'
			FROM information_schema.columns
			WHERE table_schema=current_schema() AND table_name='` + table + `' ORDER BY ordinal_position`
	}
	rows, err := tdb.Query(query)
	if err != nil {
		t.Fatalf("read %s columns: %v", table, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var entry string
		if err := rows.Scan(&entry); err != nil {
			t.Fatalf("scan %s column: %v", table, err)
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s columns: %v", table, err)
	}
	return out
}

func importTableIndexes(t *testing.T, tdb *testutils.TestDB, table string) []string {
	t.Helper()
	query := `SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='` + table + `' AND name IS NOT NULL`
	if testutils.IsPostgres() {
		query = `SELECT indexname FROM pg_indexes WHERE schemaname=current_schema() AND tablename='` + table + `'`
	}
	rows, err := tdb.Query(query)
	if err != nil {
		t.Fatalf("read %s indexes: %v", table, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan %s index: %v", table, err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s indexes: %v", table, err)
	}
	sort.Strings(out)
	return out
}
