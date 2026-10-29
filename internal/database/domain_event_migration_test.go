//go:build test

package database

import (
	"fmt"
	"testing"
)

func TestMigrationDomainEventEngineCreatesDurableTables(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/domain-events.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 2, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE schema_migrations (
		version TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create migration catalog: %v", err)
	}

	var migration *Migration
	for index := range Catalog {
		if Catalog[index].Version == "20260827_domain_event_engine" {
			migration = &Catalog[index]
			break
		}
	}
	if migration == nil {
		t.Fatal("domain event migration missing from catalog")
	}
	if err := applyMigration(db, driverSQLite, *migration); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	var tables int
	if err := db.QueryRow(migration.CheckSQLite).Scan(&tables); err != nil {
		t.Fatalf("check migrated tables: %v", err)
	}
	if tables != 1 {
		t.Fatalf("migration check = %d, want 1", tables)
	}
	var stamps int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM schema_migrations
		WHERE version = '20260827_domain_event_engine'
	`).Scan(&stamps); err != nil {
		t.Fatalf("count migration stamps: %v", err)
	}
	if stamps != 1 {
		t.Fatalf("migration stamps = %d, want 1", stamps)
	}
}
