//go:build test

package database_test

import (
	"testing"

	"windshift/internal/testutils"
)

func TestUsersOffboardedAtMigrationUpgradesExistingSchema(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	// Drop the column and stamp to model the previous production schema.
	for _, query := range []string{
		"ALTER TABLE users DROP COLUMN offboarded_at",
		"DELETE FROM schema_migrations WHERE version = '20260911_users_offboarded_at'",
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
	rows, err := db.Query("SELECT offboarded_at FROM users")
	if err != nil {
		t.Fatalf("offboarded_at column was not restored: %v", err)
	}
	rows.Close()
	var stamps int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = '20260911_users_offboarded_at'").Scan(&stamps); err != nil {
		t.Fatal(err)
	}
	if stamps != 1 {
		t.Fatalf("migration stamps = %d, want 1", stamps)
	}
}
