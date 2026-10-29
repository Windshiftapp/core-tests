//go:build test

package database

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestMigrationDailyBriefingProvenanceLeavesLegacySnapshotsUntrusted(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/daily-briefing-provenance.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 2, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY, name TEXT NOT NULL, checksum TEXT NOT NULL,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE daily_briefings (
			id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL, date TEXT NOT NULL,
			content TEXT NOT NULL, error TEXT
		)`,
		`INSERT INTO daily_briefings(id, user_id, date, content) VALUES (1, 7, '2026-08-31', 'legacy private content')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("legacy fixture: %v", err)
		}
	}
	var migration *Migration
	for i := range Catalog {
		if Catalog[i].Version == "20260901_daily_briefing_workspace_provenance" {
			migration = &Catalog[i]
			break
		}
	}
	if migration == nil {
		t.Fatal("daily briefing provenance migration missing from catalog")
	}
	if err := applyMigration(db, driverSQLite, *migration); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	var source sql.NullString
	if err := db.QueryRow(`SELECT source_workspace_ids FROM daily_briefings WHERE id = 1`).Scan(&source); err != nil {
		t.Fatalf("read migrated row: %v", err)
	}
	if source.Valid {
		t.Fatalf("legacy source workspace IDs = %q, want NULL", source.String)
	}
}
