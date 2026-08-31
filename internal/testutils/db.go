//go:build test

package testutils

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lib/pq"
	"windshift/internal/database"
)

// SharedMemoryDSN is the recommended DSN for in-memory test databases.
// Uses shared cache so multiple connections see the same data.
// Required because DB struct uses separate read pool and write connection.
const SharedMemoryDSN = "file::memory:?cache=shared&mode=memory"

// uniqueMemoryDSN returns a NAMED shared-cache in-memory DSN unique to this
// call. The anonymous SharedMemoryDSN is one process-wide database that
// outlives a test whenever any connection is still open (async goroutines,
// permission caches, write batchers), so consecutive tests leak rows into
// each other and trip UNIQUE constraints. A unique name keeps the
// multi-connection shared-cache semantics while isolating each test.
func uniqueMemoryDSN() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("file:testdb-%s?mode=memory&cache=shared", hex.EncodeToString(buf))
}

// engineName returns the test database engine, defaulting to sqlite.
// Set TEST_DB_TYPE=postgres to run tests against PostgreSQL; in that mode
// TEST_POSTGRES_DSN must point at a reachable Postgres instance (a "control"
// connection used to CREATE/DROP per-test databases).
func engineName() string {
	if e := os.Getenv("TEST_DB_TYPE"); e != "" {
		return e
	}
	return "sqlite"
}

// IsPostgres reports whether tests are running against PostgreSQL.
func IsPostgres() bool { return engineName() == "postgres" }

// TestDB wraps a database connection for testing. The underlying engine is
// selected by TEST_DB_TYPE; both the embedded interface and the DB field
// point at the same value so test code can use either tdb.QueryRow(...)
// (promoted) or tdb.DB.QueryRow(...) (legacy explicit access).
type TestDB struct {
	database.Database                   // embedded — promotes QueryRow/Exec/Begin/etc.
	DB                database.Database // alias for legacy tdb.DB.* usage

	Engine   string // "sqlite" or "postgres"
	TempFile string // SQLite-only
	IsMemory bool   // SQLite-only

	pgDBName  string // Postgres-only — name of the per-test database
	pgCtrlDSN string // Postgres-only — DSN used to drop the database in cleanup
}

// CreateTestDB creates a new test database instance with the schema applied.
// In SQLite mode, inMemory selects between an in-memory database and a temp
// file. In Postgres mode, inMemory is ignored: each test gets a freshly
// created Postgres database (via TEST_POSTGRES_DSN) which is dropped on
// t.Cleanup. Set TEST_DB_TYPE=postgres to switch engines.
func CreateTestDB(t *testing.T, inMemory bool) *TestDB {
	if engineName() == "postgres" {
		return createPostgresTestDB(t, true)
	}
	return createSQLiteTestDB(t, inMemory, true)
}

// CreateFreshDB creates a database without running schema initialization
// (used when the test itself exercises the initialization code).
func CreateFreshDB(t *testing.T, inMemory bool) *TestDB {
	if engineName() == "postgres" {
		return createPostgresTestDB(t, false)
	}
	return createSQLiteTestDB(t, inMemory, false)
}

// InsertID inserts one row and returns its generated id on both SQLite and
// PostgreSQL. The query must not already contain a RETURNING clause.
func InsertID(t *testing.T, db database.Database, query string, args ...any) int {
	t.Helper()
	var id int
	if err := db.QueryRow(strings.TrimSpace(query)+" RETURNING id", args...).Scan(&id); err != nil {
		t.Fatalf("insert fixture: %v\nquery: %s", err, query)
	}
	return id
}

func createSQLiteTestDB(t *testing.T, inMemory bool, init bool) *TestDB {
	var dsn, tempFile string
	if inMemory {
		dsn = uniqueMemoryDSN()
	} else {
		tempDir := t.TempDir()
		tempFile = filepath.Join(tempDir, "test.db")
		dsn = tempFile
	}

	db, err := database.NewDB(dsn, 120, 1)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	sqliteDB := &database.SQLiteDB{DB: db}

	if init {
		if err := db.Initialize(); err != nil {
			t.Fatalf("Failed to initialize test database: %v", err)
		}
		seedDefaultUser(sqliteDB)
	}

	return &TestDB{
		Database: sqliteDB,
		DB:       sqliteDB,
		Engine:   "sqlite",
		TempFile: tempFile,
		IsMemory: inMemory,
	}
}

func createPostgresTestDB(t *testing.T, init bool) *TestDB {
	ctrlDSN := os.Getenv("TEST_POSTGRES_DSN")
	if ctrlDSN == "" {
		t.Fatal("TEST_DB_TYPE=postgres but TEST_POSTGRES_DSN is empty")
	}

	// lib/pq doesn't allow placeholder substitution for identifiers, so we
	// generate a name from random bytes and quote it.
	name := "wstest_" + randHex(8)

	ctrl, err := sql.Open("postgres", ctrlDSN)
	if err != nil {
		t.Fatalf("postgres control open: %v", err)
	}
	if _, err := ctrl.Exec("CREATE DATABASE " + pq.QuoteIdentifier(name)); err != nil {
		_ = ctrl.Close()
		t.Fatalf("create test database %s: %v", name, err)
	}
	_ = ctrl.Close()

	testDSN := replaceDBName(ctrlDSN, name)
	pdb, err := database.NewPostgresDB(testDSN, 50)
	if err != nil {
		dropPostgresDB(ctrlDSN, name)
		t.Fatalf("open test database %s: %v", name, err)
	}

	if init {
		if err := pdb.Initialize(); err != nil {
			_ = pdb.Close()
			dropPostgresDB(ctrlDSN, name)
			t.Fatalf("initialize test database %s: %v", name, err)
		}
		seedDefaultUser(pdb)
	}

	tdb := &TestDB{
		Database:  pdb,
		DB:        pdb,
		Engine:    "postgres",
		pgDBName:  name,
		pgCtrlDSN: ctrlDSN,
	}
	t.Cleanup(func() {
		_ = pdb.Close()
		dropPostgresDB(ctrlDSN, name)
	})
	return tdb
}

func dropPostgresDB(ctrlDSN, name string) {
	ctrl, err := sql.Open("postgres", ctrlDSN)
	if err != nil {
		return
	}
	defer func() { _ = ctrl.Close() }()
	// FORCE disconnects lingering sessions (Postgres 13+) so DROP doesn't
	// fail when a connection from the closed pool hasn't fully released yet.
	if _, err := ctrl.Exec("DROP DATABASE IF EXISTS " + pq.QuoteIdentifier(name) + " WITH (FORCE)"); err != nil {
		// Fall back to plain DROP for older Postgres versions.
		_, _ = ctrl.Exec("DROP DATABASE IF EXISTS " + pq.QuoteIdentifier(name))
	}
}

// seedDefaultUser inserts the user row that audit-log FK constraints reference.
// Idempotent so callers can invoke it multiple times against the same DB.
func seedDefaultUser(db database.Database) {
	if _, err := db.ExecWrite(`
		INSERT INTO users (id, email, username, first_name, last_name, password_hash, is_active)
		VALUES (1, 'test@example.com', 'testuser', 'Test', 'User', '$2a$10$hash', true)
		ON CONFLICT DO NOTHING
	`); err != nil {
		return
	}
	if db.GetDriverName() == "postgres" {
		// Bump the SERIAL sequence past 1 so subsequent INSERTs without an
		// explicit id don't collide with the hardcoded user.
		_, _ = db.Exec(`SELECT setval(pg_get_serial_sequence('users', 'id'), 1, true)`)
	}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// replaceDBName rewrites a Postgres DSN to point at a different database.
// Handles both URL-style (postgres://...) and key=value (host=... dbname=...) DSNs.
func replaceDBName(dsn, newDB string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		scheme := dsn
		params := ""
		if i := strings.IndexByte(dsn, '?'); i >= 0 {
			scheme, params = dsn[:i], dsn[i:]
		}
		// Find the path separator after authority — `://` is at index 8 or 11.
		authEnd := strings.Index(scheme, "://") + 3
		if slash := strings.LastIndexByte(scheme, '/'); slash >= authEnd {
			return scheme[:slash+1] + newDB + params
		}
		// No path component; append one.
		return scheme + "/" + newDB + params
	}
	parts := strings.Fields(dsn)
	found := false
	for i, p := range parts {
		if strings.HasPrefix(p, "dbname=") {
			parts[i] = "dbname=" + newDB
			found = true
		}
	}
	if !found {
		parts = append(parts, "dbname="+newDB)
	}
	return strings.Join(parts, " ")
}

// Close closes the database connection and cleans up any temp files.
// Postgres test databases are dropped via t.Cleanup, not here.
func (tdb *TestDB) Close() error {
	if err := tdb.Database.Close(); err != nil {
		return err
	}
	if tdb.Engine == "sqlite" && !tdb.IsMemory && tdb.TempFile != "" {
		if _, err := os.Stat(tdb.TempFile); err == nil {
			_ = os.Remove(tdb.TempFile)
		}
	}
	return nil
}

// AssertTableExists verifies that a table exists in the database
func (tdb *TestDB) AssertTableExists(t *testing.T, tableName string) {
	var query string
	switch tdb.GetDriverName() {
	case "postgres":
		query = `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?)`
	default:
		query = `SELECT EXISTS(SELECT name FROM sqlite_master WHERE type='table' AND name=?)`
	}
	var exists bool
	if err := tdb.QueryRow(query, tableName).Scan(&exists); err != nil {
		t.Fatalf("Failed to check if table %s exists: %v", tableName, err)
	}
	if !exists {
		t.Fatalf("Table %s does not exist", tableName)
	}
}

// AssertTableNotExists verifies that a table does not exist in the database
func (tdb *TestDB) AssertTableNotExists(t *testing.T, tableName string) {
	var query string
	switch tdb.GetDriverName() {
	case "postgres":
		query = `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?)`
	default:
		query = `SELECT EXISTS(SELECT name FROM sqlite_master WHERE type='table' AND name=?)`
	}
	var exists bool
	if err := tdb.QueryRow(query, tableName).Scan(&exists); err != nil {
		t.Fatalf("Failed to check if table %s exists: %v", tableName, err)
	}
	if exists {
		t.Fatalf("Table %s should not exist but does", tableName)
	}
}

// AssertColumnExists verifies that a column exists in a table
func (tdb *TestDB) AssertColumnExists(t *testing.T, tableName, columnName string) {
	var query string
	switch tdb.GetDriverName() {
	case "postgres":
		query = `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`
	default:
		query = `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`
	}
	var count int
	if err := tdb.QueryRow(query, tableName, columnName).Scan(&count); err != nil {
		t.Fatalf("Failed to check column %s.%s: %v", tableName, columnName, err)
	}
	if count == 0 {
		t.Fatalf("Column %s.%s does not exist", tableName, columnName)
	}
}

// AssertIndexExists verifies that an index exists
func (tdb *TestDB) AssertIndexExists(t *testing.T, indexName string) {
	var query string
	switch tdb.GetDriverName() {
	case "postgres":
		query = `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE schemaname = current_schema() AND indexname = ?)`
	default:
		query = `SELECT EXISTS(SELECT name FROM sqlite_master WHERE type='index' AND name=?)`
	}
	var exists bool
	if err := tdb.QueryRow(query, indexName).Scan(&exists); err != nil {
		t.Fatalf("Failed to check if index %s exists: %v", indexName, err)
	}
	if !exists {
		t.Fatalf("Index %s does not exist", indexName)
	}
}

// AssertForeignKeyEnabled verifies that foreign key constraints are enabled.
// SQLite needs an explicit PRAGMA; Postgres always enforces FKs so this is
// a no-op there.
func (tdb *TestDB) AssertForeignKeyEnabled(t *testing.T) {
	if tdb.GetDriverName() == "postgres" {
		return
	}
	var enabled bool
	if err := tdb.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil {
		t.Fatalf("Failed to check foreign key status: %v", err)
	}
	if !enabled {
		t.Fatal("Foreign key constraints are not enabled")
	}
}

// GetTableCount returns the number of user tables in the database
func (tdb *TestDB) GetTableCount() (int, error) {
	var query string
	switch tdb.GetDriverName() {
	case "postgres":
		query = `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`
	default:
		query = `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`
	}
	var count int
	err := tdb.QueryRow(query).Scan(&count)
	return count, err
}

// TestDataSet contains IDs of seeded test data
type TestDataSet struct {
	WorkspaceID      int
	UserID           int
	StatusCategoryID int
	StatusID         int
	PriorityID       int
}

// SeedTestData populates the database with basic test data
func (tdb *TestDB) SeedTestData(t *testing.T) TestDataSet {
	data := TestDataSet{}

	// Create test workspace. ON CONFLICT DO NOTHING is a no-op when a previous
	// test in the same shared-memory SQLite DB already inserted the row.
	if _, err := tdb.Exec(`
		INSERT INTO workspaces (id, name, key, description, active)
		VALUES (1, 'Test Workspace', 'TEST', 'Test workspace for unit tests', true)
		ON CONFLICT DO NOTHING
	`); err != nil {
		t.Fatalf("Failed to seed workspace: %v", err)
	}
	data.WorkspaceID = 1
	tdb.bumpSerialSequence("workspaces", "id", 1)

	// Create test user. CreateTestDB already seeds this row; the conflict is
	// harmless but expected.
	if _, err := tdb.Exec(`
		INSERT INTO users (id, email, username, first_name, last_name, password_hash, is_active)
		VALUES (1, 'test@example.com', 'testuser', 'Test', 'User', '$2a$10$hash', true)
		ON CONFLICT DO NOTHING
	`); err != nil {
		t.Fatalf("Failed to seed user: %v", err)
	}
	data.UserID = 1
	tdb.bumpSerialSequence("users", "id", 1)

	// Use existing default status category (created during database initialization)
	var categoryID int
	if err := tdb.QueryRow("SELECT id FROM status_categories WHERE is_default = true LIMIT 1").Scan(&categoryID); err != nil {
		t.Fatalf("Failed to find default status category: %v", err)
	}
	data.StatusCategoryID = categoryID

	// Use existing default status (created during database initialization)
	var statusID int
	if err := tdb.QueryRow("SELECT id FROM statuses WHERE is_default = true LIMIT 1").Scan(&statusID); err != nil {
		t.Fatalf("Failed to find default status: %v", err)
	}
	data.StatusID = statusID

	// Use existing default priority (created during database initialization)
	var priorityID int
	err := tdb.QueryRow("SELECT id FROM priorities WHERE is_default = true LIMIT 1").Scan(&priorityID)
	if err != nil {
		// Fall back to any priority if there's no default.
		if err = tdb.QueryRow("SELECT id FROM priorities LIMIT 1").Scan(&priorityID); err != nil {
			t.Fatalf("Failed to find any priority: %v", err)
		}
	}
	data.PriorityID = priorityID

	// Grant test user Administrator role on test workspace
	var adminRoleID int
	if err := tdb.QueryRow(`SELECT id FROM workspace_roles WHERE name = 'Administrator'`).Scan(&adminRoleID); err != nil {
		t.Fatalf("Failed to get Administrator role: %v", err)
	}

	if _, err := tdb.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT DO NOTHING
	`, data.UserID, data.WorkspaceID, adminRoleID); err != nil {
		t.Fatalf("Failed to assign workspace role: %v", err)
	}

	return data
}

// bumpSerialSequence advances the SERIAL sequence backing (table, column) past
// the given value on Postgres. No-op on SQLite, where AUTOINCREMENT cooperates
// with explicit-id INSERTs without manual nudging.
func (tdb *TestDB) bumpSerialSequence(table, column string, value int) {
	if tdb.GetDriverName() != "postgres" {
		return
	}
	_, _ = tdb.Exec(
		`SELECT setval(pg_get_serial_sequence(?, ?), ?, true)`,
		table, column, value,
	)
}

// ClearAllTables removes all data from all user tables (for cleanup)
func (tdb *TestDB) ClearAllTables(t *testing.T) {
	if tdb.GetDriverName() == "postgres" {
		tdb.clearAllTablesPostgres(t)
		return
	}
	tdb.clearAllTablesSQLite(t)
}

func (tdb *TestDB) clearAllTablesSQLite(t *testing.T) {
	rows, err := tdb.Query(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name != 'migrations'
	`)
	if err != nil {
		t.Fatalf("Failed to get table names: %v", err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			_ = rows.Close()
			t.Fatalf("Failed to scan table name: %v", err)
		}
		tables = append(tables, n)
	}
	_ = rows.Close()

	if _, err := tdb.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("Failed to disable foreign keys: %v", err)
	}
	for _, table := range tables {
		if _, err := tdb.Exec(fmt.Sprintf("DELETE FROM %s", table)); err != nil {
			t.Fatalf("Failed to clear table %s: %v", table, err)
		}
	}
	if _, err := tdb.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("Failed to re-enable foreign keys: %v", err)
	}
}

func (tdb *TestDB) clearAllTablesPostgres(t *testing.T) {
	rows, err := tdb.Query(`
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_type = 'BASE TABLE'
		  AND table_name != 'migrations'
	`)
	if err != nil {
		t.Fatalf("Failed to get table names: %v", err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			_ = rows.Close()
			t.Fatalf("Failed to scan table name: %v", err)
		}
		tables = append(tables, pq.QuoteIdentifier(n))
	}
	_ = rows.Close()
	if len(tables) == 0 {
		return
	}
	// TRUNCATE with RESTART IDENTITY CASCADE wipes data, resets SERIAL
	// sequences, and follows FK references — semantically equivalent to the
	// SQLite DELETE-with-FK-disabled dance.
	stmt := "TRUNCATE TABLE " + strings.Join(tables, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := tdb.Exec(stmt); err != nil {
		t.Fatalf("Failed to truncate tables: %v", err)
	}
}

// ExecuteInTransaction executes a function within a database transaction
func (tdb *TestDB) ExecuteInTransaction(t *testing.T, fn func(*sql.Tx) error) {
	tx, err := tdb.GetDB().Begin()
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback()
			panic(r)
		}
	}()

	if err := fn(tx); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			t.Fatalf("Failed to rollback transaction: %v (original error: %v)", rollbackErr, err)
		}
		t.Fatalf("Transaction function failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("Failed to commit transaction: %v", err)
	}
}

// GetDatabase returns the Database interface for use with service layer
func (tdb *TestDB) GetDatabase() database.Database {
	return tdb.Database
}
