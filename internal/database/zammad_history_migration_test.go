//go:build test

package database_test

import (
	"testing"

	"windshift/internal/testutils"
)

func TestZammadHistoryMigrationReappliesOnExistingDatabase(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	const version = "20261002_zammad_ticket_change_history"
	assertHistoryMigration := func(stage string) {
		t.Helper()
		var stampCount int
		if err := tdb.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version).Scan(&stampCount); err != nil {
			t.Fatalf("%s: read migration stamp: %v", stage, err)
		}
		if stampCount != 1 {
			t.Fatalf("%s: expected one migration stamp, got %d", stage, stampCount)
		}
		rows, err := tdb.Query("SELECT ticket_link_id, field_name, old_value_name, new_value_name, observed_at FROM zammad_ticket_changes WHERE 1=0")
		if err != nil {
			t.Fatalf("%s: history table missing expected columns: %v", stage, err)
		}
		_ = rows.Close()
	}
	assertHistoryMigration("fresh schema")

	// Model an existing installation at the previous migration version.
	if _, err := tdb.Exec("DROP TABLE zammad_ticket_changes"); err != nil {
		t.Fatalf("drop history table for upgrade fixture: %v", err)
	}
	if _, err := tdb.Exec("DELETE FROM schema_migrations WHERE version = ?", version); err != nil {
		t.Fatalf("remove history migration stamp: %v", err)
	}
	if err := tdb.GetDatabase().Initialize(); err != nil {
		t.Fatalf("run history migration on existing database: %v", err)
	}
	assertHistoryMigration("upgraded schema")
}
