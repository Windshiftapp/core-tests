package database

import (
	"fmt"
	"strings"
	"testing"
)

// schemaMigrationsDDL mirrors the bootstrap DDL in DB.Initialize and
// PostgresDB.Initialize. Tests create the table directly so they don't
// depend on the full schema bootstrap.
const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version    TEXT PRIMARY KEY,
	name       TEXT NOT NULL,
	checksum   TEXT NOT NULL DEFAULT '',
	applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);`

func openTestDB(t *testing.T) Database {
	t.Helper()
	// Per-test on-disk temp DB. file::memory: would also work but on-disk
	// keeps the write-connection pragmas honest under WAL.
	dsn := fmt.Sprintf("file:%s/test.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(schemaMigrationsDDL); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	return db
}

func countRows(t *testing.T, db Database, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func TestRunPendingMigrations_NoopWithEmptyCatalog(t *testing.T) {
	db := openTestDB(t)
	if err := runPendingMigrations(db, nil); err != nil {
		t.Fatalf("empty catalog: %v", err)
	}
	if n := countRows(t, db, "SELECT COUNT(*) FROM schema_migrations"); n != 0 {
		t.Fatalf("expected 0 stamped rows, got %d", n)
	}
}

func TestRunPendingMigrations_AppliesNewMigration(t *testing.T) {
	db := openTestDB(t)

	catalog := []Migration{{
		Version: "test_001_create_widget",
		Name:    "create widget table",
		SQLite:  "CREATE TABLE widget (id INTEGER PRIMARY KEY)",
	}}

	if err := runPendingMigrations(db, catalog); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if n := countRows(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='widget'"); n != 1 {
		t.Fatalf("widget table not created (count=%d)", n)
	}
	if n := countRows(t, db, "SELECT COUNT(*) FROM schema_migrations WHERE version='test_001_create_widget'"); n != 1 {
		t.Fatalf("stamp not recorded (count=%d)", n)
	}
}

func TestRunPendingMigrations_RetroactiveStampWhenCheckTrue(t *testing.T) {
	db := openTestDB(t)

	// Simulate an existing install: the table is already there.
	if _, err := db.Exec("CREATE TABLE widget (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("seed widget table: %v", err)
	}

	catalog := []Migration{{
		Version:     "test_001_create_widget",
		Name:        "create widget table",
		CheckSQLite: "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='widget'",
		// If the runner mistakenly re-ran this body, the second CREATE would error.
		SQLite: "CREATE TABLE widget (id INTEGER PRIMARY KEY)",
	}}

	if err := runPendingMigrations(db, catalog); err != nil {
		t.Fatalf("retroactive stamp: %v", err)
	}
	if n := countRows(t, db, "SELECT COUNT(*) FROM schema_migrations WHERE version='test_001_create_widget'"); n != 1 {
		t.Fatalf("retroactive stamp not recorded (count=%d)", n)
	}
}

func TestRunPendingMigrations_IdempotentOnRerun(t *testing.T) {
	db := openTestDB(t)

	catalog := []Migration{{
		Version: "test_001_create_widget",
		Name:    "create widget table",
		// No Check: would re-run and conflict if the runner ignored
		// schema_migrations on the second pass.
		SQLite: "CREATE TABLE widget (id INTEGER PRIMARY KEY)",
	}}

	if err := runPendingMigrations(db, catalog); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := runPendingMigrations(db, catalog); err != nil {
		t.Fatalf("second run should be a no-op: %v", err)
	}
	if n := countRows(t, db, "SELECT COUNT(*) FROM schema_migrations"); n != 1 {
		t.Fatalf("expected exactly 1 stamped row, got %d", n)
	}
}

func TestRunPendingMigrations_RejectsChecksumMismatch(t *testing.T) {
	db := openTestDB(t)

	migration := Migration{
		Version: "test_001_immutable",
		Name:    "immutable migration",
		SQLite:  "CREATE TABLE immutable_widget (id INTEGER PRIMARY KEY)",
	}
	if err := runPendingMigrations(db, []Migration{migration}); err != nil {
		t.Fatalf("initial migration: %v", err)
	}

	migration.SQLite = "CREATE TABLE immutable_widget (id INTEGER PRIMARY KEY, name TEXT)"
	err := runPendingMigrations(db, []Migration{migration})
	if err == nil {
		t.Fatal("expected checksum mismatch after changing an applied migration")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") || !strings.Contains(err.Error(), migration.Version) {
		t.Fatalf("checksum mismatch error = %q, want version and useful context", err)
	}
}

func TestRunPendingMigrations_ReconcilesMutableSchemaChecksum(t *testing.T) {
	db := openTestDB(t)

	migration := Migration{
		Version:           "test_001_mutable_schema",
		Name:              "mutable schema wrapper",
		ReconcileChecksum: true,
		SQLite:            "CREATE TABLE mutable_widget (id INTEGER PRIMARY KEY)",
	}
	if err := runPendingMigrations(db, []Migration{migration}); err != nil {
		t.Fatalf("initial migration: %v", err)
	}

	// Canonical schema wrappers change whenever their schema file gains an
	// object. An already-stamped wrapper must not re-run its body or abort
	// startup; the append-only migration paired with the schema edit owns the
	// existing-database upgrade.
	migration.SQLite = "THIS BODY MUST NOT RUN"
	if err := runPendingMigrations(db, []Migration{migration}); err != nil {
		t.Fatalf("reconcile mutable schema checksum: %v", err)
	}

	var checksum string
	if err := db.QueryRow(
		"SELECT checksum FROM schema_migrations WHERE version = ?", migration.Version,
	).Scan(&checksum); err != nil {
		t.Fatalf("load reconciled checksum: %v", err)
	}
	if checksum != migration.checksum(driverSQLite) {
		t.Fatalf("checksum = %q, want reconciled %q", checksum, migration.checksum(driverSQLite))
	}
}

func TestRunPendingMigrations_BackfillsLegacyEmptyChecksum(t *testing.T) {
	db := openTestDB(t)
	migration := Migration{
		Version: "test_001_legacy_checksum",
		Name:    "legacy migration",
		SQLite:  "CREATE TABLE legacy_widget (id INTEGER PRIMARY KEY)",
	}
	if _, err := db.Exec(
		"INSERT INTO schema_migrations(version, name, checksum) VALUES(?, ?, '')",
		migration.Version, migration.Name,
	); err != nil {
		t.Fatalf("seed legacy migration stamp: %v", err)
	}

	if err := runPendingMigrations(db, []Migration{migration}); err != nil {
		t.Fatalf("backfill legacy checksum: %v", err)
	}
	var checksum string
	if err := db.QueryRow(
		"SELECT checksum FROM schema_migrations WHERE version = ?", migration.Version,
	).Scan(&checksum); err != nil {
		t.Fatalf("load backfilled checksum: %v", err)
	}
	if checksum != migration.checksum(driverSQLite) {
		t.Fatalf("checksum = %q, want %q", checksum, migration.checksum(driverSQLite))
	}
}

func TestMigration_PortalApprovalVotesDeduplicatesAndEnforcesUniqueness(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`
		CREATE TABLE approval_decisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			approval_request_id INTEGER NOT NULL,
			approval_step_instance_id INTEGER,
			actor_user_id INTEGER,
			actor_portal_customer_id INTEGER,
			decision TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		t.Fatalf("create legacy approval decisions: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO approval_decisions
			(approval_request_id, approval_step_instance_id, actor_portal_customer_id, decision)
		VALUES (1, 10, 20, 'approve'), (1, 10, 20, 'approve')
	`); err != nil {
		t.Fatalf("seed duplicate portal votes: %v", err)
	}

	var migration *Migration
	for i := range Catalog {
		if Catalog[i].Version == "20260816_portal_approval_vote_uniqueness" {
			migration = &Catalog[i]
			break
		}
	}
	if migration == nil {
		t.Fatal("portal approval vote uniqueness migration missing")
	}
	if err := runPendingMigrations(db, []Migration{*migration}); err != nil {
		t.Fatalf("apply portal vote migration: %v", err)
	}
	if got := countRows(t, db, "SELECT COUNT(*) FROM approval_decisions WHERE approval_step_instance_id=10 AND actor_portal_customer_id=20 AND decision='approve'"); got != 1 {
		t.Fatalf("portal votes after migration = %d, want 1", got)
	}
	if _, err := db.Exec(`
		INSERT INTO approval_decisions
			(approval_request_id, approval_step_instance_id, actor_portal_customer_id, decision)
		VALUES (1, 10, 20, 'reject')
	`); err == nil {
		t.Fatal("duplicate portal vote passed the migrated unique index")
	}
}

// TestCatalog_FreshInstallStampsEveryEntry verifies that on a fresh SQLite
// install, every catalog entry's CheckSQLite predicate reports the effect
// is already present (so the runner stamps without re-running the body).
// Catches typos in table or column names: if a Check returns 0 on a fresh
// install, the runner would try to ALTER and fail with "duplicate column"
// (since fresh schema files already contain it).
func TestCatalog_FreshInstallStampsEveryEntry(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/fresh.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize fresh db: %v", err)
	}
	if err := db.Initialize(); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}

	// Every catalog entry should be stamped exactly once. Postgres-only
	// entries (empty SQLite body) still get stamped on SQLite — the runner
	// stamps without DDL when the body for this backend is empty.
	stamped := countRows(t, db, "SELECT COUNT(*) FROM schema_migrations")
	if stamped != len(Catalog) {
		t.Fatalf("schema_migrations row count = %d, want %d (len Catalog)", stamped, len(Catalog))
	}
	// Stamps carry a checksum for every row, including the empty-bodied
	// baseline marker (checksum of the empty body).
	var unstamped int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE checksum = '' OR checksum IS NULL").Scan(&unstamped); err != nil || unstamped != 0 {
		t.Fatalf("rows without checksum = %d, %v; want 0", unstamped, err)
	}
}

// TestMigration_ExternalKey_UniquePerWorkspace verifies the partial-unique
// invariant that `create_milestone`'s upsert logic relies on: two
// workspaces can share an external_key (e.g. both have a "2.0" milestone)
// but a single workspace cannot. NULLs are unconstrained.
func TestMigration_ExternalKey_UniquePerWorkspace(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/extkey.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize fresh db: %v", err)
	}

	// Two workspaces.
	for i, key := range []string{"alpha", "beta"} {
		if _, err := db.Exec(
			`INSERT INTO workspaces(id, name, key, active) VALUES (?, ?, ?, TRUE)`,
			i+1, "ws-"+key, key,
		); err != nil {
			t.Fatalf("insert workspace %s: %v", key, err)
		}
	}

	// Same external_key in two different workspaces — allowed.
	for _, wsID := range []int{1, 2} {
		if _, err := db.Exec(
			`INSERT INTO milestones(name, is_global, workspace_id, external_key) VALUES (?, FALSE, ?, '2.0')`,
			fmt.Sprintf("Release 2.0 (ws %d)", wsID), wsID,
		); err != nil {
			t.Fatalf("insert milestone for ws %d: %v", wsID, err)
		}
	}

	// Same external_key twice in the same workspace — must fail.
	if _, err := db.Exec(
		`INSERT INTO milestones(name, is_global, workspace_id, external_key) VALUES ('Dupe', FALSE, 1, '2.0')`,
	); err == nil {
		t.Fatal("expected unique-constraint failure on duplicate external_key within workspace, got nil")
	}

	// NULL external_keys must not collide with each other.
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(
			`INSERT INTO milestones(name, is_global, workspace_id, external_key) VALUES (?, FALSE, 1, NULL)`,
			fmt.Sprintf("Unkeyed %d", i),
		); err != nil {
			t.Fatalf("insert NULL-key milestone: %v", err)
		}
	}
}

func TestRunPendingMigrations_AbortsOnFailingMigration(t *testing.T) {
	db := openTestDB(t)

	catalog := []Migration{
		{
			Version: "test_001_ok",
			Name:    "good migration",
			SQLite:  "CREATE TABLE good (id INTEGER PRIMARY KEY)",
		},
		{
			Version: "test_002_bad",
			Name:    "bad migration",
			SQLite:  "CREATE TABLE bad (id INTEGER PRIMARY KEY)", // succeeds…
			// …but then we deliberately break it with bogus SQL.
		},
	}
	catalog[1].SQLite = "this is not valid sql"

	err := runPendingMigrations(db, catalog)
	if err == nil {
		t.Fatalf("expected error from bad migration, got nil")
	}

	// First migration stamped and applied; second is rolled back.
	if n := countRows(t, db, "SELECT COUNT(*) FROM schema_migrations"); n != 1 {
		t.Fatalf("expected 1 stamped row after partial failure, got %d", n)
	}
	if n := countRows(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='bad'"); n != 0 {
		t.Fatalf("expected 'bad' table to be rolled back, found %d", n)
	}

	// Correcting the failed pending migration must resume from that entry;
	// the already-applied migration remains stamped exactly once.
	catalog[1].SQLite = "CREATE TABLE bad (id INTEGER PRIMARY KEY)"
	if err := runPendingMigrations(db, catalog); err != nil {
		t.Fatalf("resume after partial failure: %v", err)
	}
	if n := countRows(t, db, "SELECT COUNT(*) FROM schema_migrations"); n != 2 {
		t.Fatalf("expected 2 stamped rows after recovery, got %d", n)
	}
	if n := countRows(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='bad'"); n != 1 {
		t.Fatalf("expected corrected migration to create 'bad', found %d", n)
	}
}
