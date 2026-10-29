//go:build test

package database_test

import (
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// preAlignmentChecksum is the body checksum carried by unreleased main
// builds whose 20260815_workspaces_is_template migration added the column
// NOT NULL. The canonical body is nullable; Superseded lets stamped
// databases advance without aborting startup.
const preAlignmentChecksum = "ea8a11f5aff9de67107eaaa4a23a1519397546f69d693b77beb1dd53c9478054"

func isTemplateMigration(t *testing.T) database.Migration {
	t.Helper()
	for _, migration := range database.Catalog {
		if migration.Version == "20260815_workspaces_is_template" {
			return migration
		}
	}
	t.Fatal("20260815_workspaces_is_template missing from catalog")
	return database.Migration{}
}

// The old-shape fixture is the v0.8.5 workspaces table created directly
// because the production schema and API can no longer produce a workspaces
// table without is_template.
func seedWorkspacesWithoutIsTemplate(t *testing.T, tdb *testutils.TestDB) {
	t.Helper()
	if testutils.IsPostgres() {
		for _, statement := range []string{
			`CREATE TABLE workspaces (
				id SERIAL PRIMARY KEY,
				name TEXT NOT NULL,
				key TEXT UNIQUE NOT NULL,
				description TEXT,
				active BOOLEAN DEFAULT true,
				is_personal BOOLEAN DEFAULT false,
				owner_id INTEGER,
				icon TEXT,
				color TEXT,
				avatar_url TEXT,
				homepage_layout TEXT,
				default_view TEXT DEFAULT 'board',
				display_mode TEXT DEFAULT 'default',
				internal_comments_enabled BOOLEAN DEFAULT false,
				created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE INDEX idx_workspaces_is_personal ON workspaces(is_personal)`,
			`CREATE INDEX idx_workspaces_owner_id ON workspaces(owner_id)`,
			`INSERT INTO workspaces (id, name, key) VALUES (1, 'Main', 'MAIN'), (2, 'Second', 'SEC')`,
		} {
			if _, err := tdb.Exec(statement); err != nil {
				t.Fatalf("seed legacy workspaces with %q: %v", statement, err)
			}
		}
		return
	}
	for _, statement := range []string{
		`CREATE TABLE workspaces (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			key TEXT UNIQUE NOT NULL,
			description TEXT,
			time_project_id INTEGER,
			active BOOLEAN DEFAULT TRUE,
			is_personal BOOLEAN DEFAULT FALSE,
			owner_id INTEGER,
			icon TEXT,
			color TEXT,
			avatar_url TEXT,
			homepage_layout TEXT,
			default_view TEXT DEFAULT 'board',
			display_mode TEXT DEFAULT 'default',
			internal_comments_enabled BOOLEAN DEFAULT FALSE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX idx_workspaces_is_personal ON workspaces(is_personal)`,
		`CREATE INDEX idx_workspaces_owner_id ON workspaces(owner_id)`,
		`INSERT INTO workspaces (id, name, key) VALUES (1, 'Main', 'MAIN'), (2, 'Second', 'SEC')`,
	} {
		if _, err := tdb.Exec(statement); err != nil {
			t.Fatalf("seed legacy workspaces with %q: %v", statement, err)
		}
	}
}

func assertIsTemplateNullable(t *testing.T, tdb *testutils.TestDB) {
	t.Helper()
	if testutils.IsPostgres() {
		var nullable string
		if err := tdb.QueryRow(`SELECT is_nullable FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='workspaces' AND column_name='is_template'`).Scan(&nullable); err != nil || nullable != "YES" {
			t.Fatalf("is_template is_nullable = %q, %v; want YES", nullable, err)
		}
		return
	}
	var notNull int
	if err := tdb.QueryRow(`SELECT "notnull" FROM pragma_table_info('workspaces') WHERE name='is_template'`).Scan(&notNull); err != nil || notNull != 0 {
		t.Fatalf("is_template notnull = %d, %v; want 0", notNull, err)
	}
}

func TestWorkspacesIsTemplateMigrationAddsNullableColumn(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)

	// Core tables plus the 0.8.5 checkpoint state so Initialize classifies
	// the database as existing, passes the preflight, and applies the
	// compact catalog instead of the canonical schema files.
	seedCanonicalSchemaCheckpoint(t, tdb)
	if _, err := tdb.Exec(`CREATE TABLE time_projects (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("seed time_projects: %v", err)
	}
	seedWorkspacesWithoutIsTemplate(t, tdb)

	createSchemaMigrationsTable(t, tdb)

	// Stamp every unrelated migration so only the is_template migration
	// runs against the minimal legacy schema.
	stampAllExcept(t, tdb, "20260815_workspaces_is_template", "")

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run is_template migration: %v", err)
	}

	assertIsTemplateNullable(t, tdb)

	// Existing rows were backfilled to false and survived.
	var rows int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM workspaces WHERE id IN (1, 2) AND is_template = false`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("surviving rows with is_template=false = %d, %v; want 2", rows, err)
	}

	// The partial index exists and enforces the template-active lookup.
	if testutils.IsPostgres() {
		if err := tdb.QueryRow(`SELECT COUNT(*) FROM pg_indexes WHERE indexname='idx_workspaces_template_active'`).Scan(&rows); err != nil || rows != 1 {
			t.Fatalf("idx_workspaces_template_active = %d, %v; want 1", rows, err)
		}
	} else {
		if err := tdb.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_workspaces_template_active'`).Scan(&rows); err != nil || rows != 1 {
			t.Fatalf("idx_workspaces_template_active = %d, %v; want 1", rows, err)
		}
	}

	// NULL remains a legal value: the column contract is nullable. Explicit
	// id because the seeded rows bypassed the sequence on PostgreSQL.
	if _, err := tdb.Exec(`INSERT INTO workspaces (id, name, key, is_template) VALUES (3, 'Explicit', 'EXPL', NULL)`); err != nil {
		t.Fatalf("explicit NULL is_template insert rejected: %v", err)
	}

	// Re-running initialization is idempotent.
	if err := tdb.Initialize(); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}
	var stamps int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version='20260815_workspaces_is_template'`).Scan(&stamps); err != nil || stamps != 1 {
		t.Fatalf("is_template migration stamps = %d, %v; want 1", stamps, err)
	}
}

func TestWorkspacesIsTemplateMigrationAdvancesSupersededChecksum(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)

	if _, err := tdb.Exec(`CREATE TABLE schema_migrations (
		version TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL DEFAULT '',
		applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	// Simulate a database stamped by an unreleased main build: the row
	// carries the pre-alignment NOT NULL body checksum. Only the
	// is_template row is stamped so no unrelated migration interferes.
	migration := isTemplateMigration(t)
	if _, err := tdb.Exec(
		`INSERT INTO schema_migrations(version, name, checksum) VALUES (?, ?, ?)`,
		migration.Version, migration.Name, preAlignmentChecksum,
	); err != nil {
		t.Fatalf("stamp superseded checksum: %v", err)
	}

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("initialize with superseded checksum: %v", err)
	}

	var checksum string
	if err := tdb.QueryRow(`SELECT checksum FROM schema_migrations WHERE version=?`, migration.Version).Scan(&checksum); err != nil {
		t.Fatalf("read advanced checksum: %v", err)
	}
	if checksum == preAlignmentChecksum || checksum == "" {
		t.Fatalf("checksum = %q; want the current body checksum", checksum)
	}

	// A second initialize confirms the advanced checksum is stable.
	if err := tdb.Initialize(); err != nil {
		t.Fatalf("repeat initialize after advance: %v", err)
	}
}
