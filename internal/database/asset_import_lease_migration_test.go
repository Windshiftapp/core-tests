//go:build test

package database_test

import (
	"testing"

	"windshift/internal/testutils"
)

func TestAssetImportLeaseMigrationUpgradesExistingSchema(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	// Remove the lease column and stamp to model the state a renamed
	// production database reaches before the lease migration re-runs.
	for _, query := range []string{
		"ALTER TABLE import_jobs DROP COLUMN lease_expires_at",
		"DELETE FROM schema_migrations WHERE version = '20260905_asset_import_leases'",
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
	rows, err := db.Query("SELECT lease_expires_at FROM import_jobs")
	if err != nil {
		t.Fatalf("lease column was not restored: %v", err)
	}
	rows.Close()
	var stamps int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = '20260905_asset_import_leases'").Scan(&stamps); err != nil {
		t.Fatal(err)
	}
	if stamps != 1 {
		t.Fatalf("migration stamps = %d, want 1", stamps)
	}
}

func TestAssetSetPortalAccessMigrationUpgradesExistingSchema(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	// Remove the new table and stamp to model the previous production schema.
	for _, query := range []string{
		"DROP TABLE asset_set_portal_access",
		"DELETE FROM schema_migrations WHERE version = '20260927_asset_set_portal_access'",
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
	rows, err := db.Query("SELECT set_id, granted_by, granted_at FROM asset_set_portal_access")
	if err != nil {
		t.Fatalf("portal access table was not restored: %v", err)
	}
	rows.Close()
	var stamps int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = '20260927_asset_set_portal_access'").Scan(&stamps); err != nil {
		t.Fatal(err)
	}
	if stamps != 1 {
		t.Fatalf("migration stamps = %d, want 1", stamps)
	}
}

func TestAssetSetPortalAccessCascadesOnSetDeletion(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	var userID, setID int
	if err := db.QueryRow("INSERT INTO users (email, username, first_name, last_name) VALUES ('portal-access-cascade@example.test', 'portal-access-cascade', 'Portal', 'Cascade') RETURNING id").Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := db.QueryRow("INSERT INTO asset_management_sets (name, created_by) VALUES ('Cascade set', ?) RETURNING id", userID).Scan(&setID); err != nil {
		t.Fatalf("insert set: %v", err)
	}
	if _, err := db.ExecWrite("INSERT INTO asset_set_portal_access (set_id, granted_by) VALUES (?, ?)", setID, userID); err != nil {
		t.Fatalf("grant portal access: %v", err)
	}
	if _, err := db.ExecWrite("DELETE FROM asset_management_sets WHERE id = ?", setID); err != nil {
		t.Fatalf("delete set: %v", err)
	}
	var remaining int
	if err := db.QueryRow("SELECT COUNT(*) FROM asset_set_portal_access WHERE set_id = ?", setID).Scan(&remaining); err != nil {
		t.Fatalf("count portal access: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("portal access rows = %d, want 0 after set deletion", remaining)
	}
}

func TestAssetImportUploadMigrationUpgradesExistingSchema(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	// Remove the new table and stamp to model the previous production schema.
	for _, query := range []string{
		"DROP TABLE import_uploads",
		"DELETE FROM schema_migrations WHERE version = '20260905_asset_import_upload_ownership'",
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
	rows, err := db.Query("SELECT id, scope_id, created_by, created_at FROM import_uploads")
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	var stamps int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = '20260905_asset_import_upload_ownership'").Scan(&stamps); err != nil {
		t.Fatal(err)
	}
	if stamps != 1 {
		t.Fatalf("migration stamps = %d, want 1", stamps)
	}
}
