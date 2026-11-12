//go:build test

package database_test

import (
	"strconv"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// seedLegacyWorklogCustomerColumn recreates the pre-migration time_worklogs
// shape: a customer_id column with its index. The fresh schema no longer has
// it and the API cannot create a stale column, so direct SQL is the only way
// to model the upgrade state.
func seedLegacyWorklogCustomerColumn(t *testing.T, tdb *testutils.TestDB) {
	t.Helper()
	if _, err := tdb.Exec(`ALTER TABLE time_worklogs ADD COLUMN customer_id INTEGER`); err != nil {
		t.Fatalf("add legacy customer_id column: %v", err)
	}
	if _, err := tdb.Exec(`CREATE INDEX idx_time_worklogs_customer_id ON time_worklogs(customer_id)`); err != nil {
		t.Fatalf("create legacy customer index: %v", err)
	}
}

func timeWorklogIndexCount(t *testing.T, tdb *testutils.TestDB, name string) int {
	t.Helper()
	query := `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name='time_worklogs' AND name='` + name + `'`
	if testutils.IsPostgres() {
		query = `SELECT COUNT(*) FROM pg_indexes WHERE schemaname=current_schema() AND tablename='time_worklogs' AND indexname='` + name + `'`
	}
	return countFixtureRows(t, tdb, query)
}

// TestMigration_TimeWorklogCustomer_DropsColumnAndPreservesRows upgrades a
// database that still stores the worklog customer reference and proves the
// migration removes the column and its index without losing worklog rows.
func TestMigration_TimeWorklogCustomer_DropsColumnAndPreservesRows(t *testing.T) {
	tdb := testutils.CreateTestDB(t, false)
	t.Cleanup(func() { _ = tdb.Close() })

	orgID := testutils.InsertID(t, tdb, `INSERT INTO customer_organisations (name) VALUES ('Legacy customer')`)
	projectID := testutils.InsertID(t, tdb, `INSERT INTO time_projects (customer_id, name, status) VALUES (?, 'Legacy project', 'Active')`, orgID)
	userID := testutils.InsertID(t, tdb, `INSERT INTO users (email, username, first_name, last_name) VALUES ('legacy-worklog@example.test', 'legacy-worklog', 'Legacy', 'Worklog')`)
	worklogID := testutils.InsertID(t, tdb, `
		INSERT INTO time_worklogs (project_id, user_id, description, date, start_time, end_time, duration_minutes, created_at, updated_at)
		VALUES (?, ?, 'Legacy entry', 100, 100, 3700, 60, 100, 100)
	`, projectID, userID)

	seedLegacyWorklogCustomerColumn(t, tdb)
	if _, err := tdb.Exec(`UPDATE time_worklogs SET customer_id = ? WHERE id = ?`, orgID, worklogID); err != nil {
		t.Fatalf("stamp legacy customer: %v", err)
	}

	// Make the customer-model migrations pending so Initialize runs them.
	for _, version := range []string{"20261117_time_project_customer_backfill", "20261118_drop_time_worklog_customer"} {
		if _, err := tdb.Exec(`DELETE FROM schema_migrations WHERE version = ?`, version); err != nil {
			t.Fatalf("clear migration %s: %v", version, err)
		}
	}

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run time worklog customer migrations: %v", err)
	}
	if err := tdb.Initialize(); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}

	if got := countFixtureRows(t, tdb, columnPresentQuery("time_worklogs", "customer_id")); got != 0 {
		t.Fatalf("time_worklogs still has customer_id after migration")
	}
	if got := countFixtureRows(t, tdb, `SELECT COUNT(*) FROM time_worklogs WHERE id = `+strconv.Itoa(worklogID)); got != 1 {
		t.Fatalf("worklog rows after migration = %d, want 1", got)
	}
	for _, index := range []string{"idx_time_worklogs_project_id", "idx_time_worklogs_date", "idx_time_worklogs_item_id", "idx_time_worklogs_user_id"} {
		if got := timeWorklogIndexCount(t, tdb, index); got != 1 {
			t.Fatalf("index %s after migration = %d, want 1", index, got)
		}
	}
	if got := timeWorklogIndexCount(t, tdb, "idx_time_worklogs_customer_id"); got != 0 {
		t.Fatalf("idx_time_worklogs_customer_id after migration = %d, want 0", got)
	}

	// Deleting the organisation must no longer cascade into worklogs.
	if _, err := tdb.Exec(`DELETE FROM customer_organisations WHERE id = ?`, orgID); err != nil {
		t.Fatalf("delete organisation: %v", err)
	}
	if got := countFixtureRows(t, tdb, `SELECT COUNT(*) FROM time_worklogs`); got != 1 {
		t.Fatalf("worklog rows after organisation delete = %d, want 1", got)
	}
}

// TestMigration_TimeProjectCustomer_BackfillsNulls proves the backfill moves
// legacy NULL project customers onto the built-in no-customer placeholder.
func TestMigration_TimeProjectCustomer_BackfillsNulls(t *testing.T) {
	tdb := testutils.CreateTestDB(t, false)
	t.Cleanup(func() { _ = tdb.Close() })

	var noCustomerID int
	if err := tdb.QueryRow(`SELECT id FROM customer_organisations WHERE builtin_key = 'no_customer'`).Scan(&noCustomerID); err != nil {
		t.Fatalf("load no-customer organisation: %v", err)
	}
	projectID := testutils.InsertID(t, tdb, `INSERT INTO time_projects (customer_id, name, status) VALUES (NULL, 'Orphan project', 'Active')`)

	if _, err := tdb.Exec(`DELETE FROM schema_migrations WHERE version = '20261117_time_project_customer_backfill'`); err != nil {
		t.Fatalf("clear backfill migration: %v", err)
	}
	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run project customer backfill: %v", err)
	}

	var got int
	if err := tdb.QueryRow(`SELECT customer_id FROM time_projects WHERE id = ?`, projectID).Scan(&got); err != nil {
		t.Fatalf("read backfilled customer: %v", err)
	}
	if got != noCustomerID {
		t.Fatalf("backfilled customer_id = %d, want no-customer %d", got, noCustomerID)
	}
}

// TestMigration_TimeWorklogCustomer_FreshInstallsStampWithoutRunning asserts
// the new checks report the effect present on a fresh install, so the runner
// stamps them instead of rebuilding a table that never had the column.
func TestMigration_TimeWorklogCustomer_FreshInstallsStampWithoutRunning(t *testing.T) {
	fresh := testutils.CreateTestDB(t, false)
	t.Cleanup(func() { _ = fresh.Close() })

	for _, version := range []string{"20261117_time_project_customer_backfill", "20261118_drop_time_worklog_customer"} {
		migration := findMigration(t, version)
		check := migration.CheckSQLite
		if testutils.IsPostgres() {
			check = migration.CheckPostgres
		}
		var applied int
		if err := fresh.QueryRow(check).Scan(&applied); err != nil {
			t.Fatalf("run %s fresh-install check: %v", version, err)
		}
		if applied != 1 {
			t.Fatalf("%s fresh-install check reported %d, want 1 (effect already present)", version, applied)
		}
		var stamps int
		if err := fresh.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&stamps); err != nil {
			t.Fatal(err)
		}
		if stamps != 1 {
			t.Fatalf("%s migration stamps = %d, want 1", version, stamps)
		}
	}
	if got := countFixtureRows(t, fresh, columnPresentQuery("time_worklogs", "customer_id")); got != 0 {
		t.Fatalf("fresh install time_worklogs has customer_id")
	}
}

func findMigration(t *testing.T, version string) *database.Migration {
	t.Helper()
	for i := range database.Catalog {
		if database.Catalog[i].Version == version {
			return &database.Catalog[i]
		}
	}
	t.Fatalf("%s missing from the migration catalog", version)
	return nil
}
