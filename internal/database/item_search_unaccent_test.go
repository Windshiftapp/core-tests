//go:build test

package database_test

import (
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// TestItemSearchIndexSurvivesUnaccentOutsideSearchPath is the WI-413 regression
// guard: the migration and schema file used to assume the unaccent extension
// was resolvable by name, so an installation that keeps extensions in a schema
// outside search_path failed with "function unaccent(unknown, text) does not
// exist". Installing unaccent in a dedicated schema must still produce a
// working ws_unaccent and a valid index on both the fresh-install (schema file)
// and upgrade (migration) paths.
func TestItemSearchIndexSurvivesUnaccentOutsideSearchPath(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()
	if db.GetDriverName() != "postgres" {
		t.Skip("PostgreSQL-only: unaccent extension schema handling")
	}

	if _, err := db.Exec(`CREATE SCHEMA ext`); err != nil {
		t.Fatalf("create extension schema: %v", err)
	}
	if _, err := db.Exec(`CREATE EXTENSION unaccent SCHEMA ext`); err != nil {
		t.Fatalf("install unaccent outside search_path: %v", err)
	}

	// Fresh install: the schema file defines ws_unaccent and the index.
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize with unaccent outside search_path: %v", err)
	}
	assertItemSearchUnaccent(t, db)

	// Upgrade: Initialize already stamped the migration, so drop the objects
	// and run the migration's ApplyPostgres directly.
	if _, err := db.Exec(`DROP INDEX IF EXISTS idx_items_search`); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if _, err := db.Exec(`DROP FUNCTION IF EXISTS ws_unaccent(text)`); err != nil {
		t.Fatalf("drop ws_unaccent: %v", err)
	}
	var migration *database.Migration
	for i := range database.Catalog {
		if database.Catalog[i].Version == "20261116_item_search_prefix" {
			migration = &database.Catalog[i]
			break
		}
	}
	if migration == nil {
		t.Fatal("item search migration not found in catalog")
	}
	if migration.ApplyPostgres == nil {
		t.Fatal("item search migration has no ApplyPostgres")
	}
	if err := migration.ApplyPostgres(db); err != nil {
		t.Fatalf("apply migration with unaccent outside search_path: %v", err)
	}
	assertItemSearchUnaccent(t, db)
}

func assertItemSearchUnaccent(t *testing.T, db database.Database) {
	t.Helper()
	var folded string
	if err := db.QueryRow(`SELECT ws_unaccent('Café')`).Scan(&folded); err != nil {
		t.Fatalf("ws_unaccent: %v", err)
	}
	if folded != "cafe" {
		t.Fatalf("ws_unaccent('Café') = %q, want %q", folded, "cafe")
	}

	var valid int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		WHERE c.relname = 'idx_items_search' AND i.indisvalid
	`).Scan(&valid); err != nil {
		t.Fatalf("read item search index: %v", err)
	}
	if valid != 1 {
		t.Fatal("idx_items_search is missing or invalid")
	}
}
