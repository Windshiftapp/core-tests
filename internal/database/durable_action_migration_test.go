//go:build test

package database

import (
	"fmt"
	"testing"
)

func TestMigrationDurableActionConsumerCreatesCutoverAndTargetState(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/durable-actions.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 2, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, statement := range []string{
		`CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY, name TEXT NOT NULL, checksum TEXT NOT NULL,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE action_execution_logs (
			id INTEGER PRIMARY KEY, action_id INTEGER NOT NULL
		)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("prepare migration fixture: %v", err)
		}
	}

	var migration *Migration
	for index := range Catalog {
		if Catalog[index].Version == "20260827_durable_action_consumer" {
			migration = &Catalog[index]
			break
		}
	}
	if migration == nil {
		t.Fatal("durable action migration missing from catalog")
	}
	if err := applyMigration(db, driverSQLite, *migration); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	var complete int
	if err := db.QueryRow(migration.CheckSQLite).Scan(&complete); err != nil {
		t.Fatalf("check migration: %v", err)
	}
	if complete != 1 {
		t.Fatalf("migration check = %d, want 1", complete)
	}
	for _, table := range []string{"action_event_cutovers", "action_event_batches", "action_event_targets"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("table %s count = %d, want 1", table, count)
		}
	}
}
