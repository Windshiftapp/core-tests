//go:build test

package database_test

import (
	"testing"

	"windshift/internal/testutils"
)

// TestItemSearchIndexExists verifies the WI-413 token-prefix search objects are
// created by a fresh schema init on both engines: the FTS5 table and its sync
// triggers on SQLite, and the valid GIN expression index on PostgreSQL.
func TestItemSearchIndexExists(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	if db.GetDriverName() == "postgres" {
		var valid int
		if err := db.QueryRow(`
			SELECT COUNT(*) FROM pg_index i
			JOIN pg_class c ON c.oid = i.indexrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE c.relname = 'idx_items_search' AND n.nspname = current_schema() AND i.indisvalid
		`).Scan(&valid); err != nil {
			t.Fatalf("read item search index: %v", err)
		}
		if valid != 1 {
			t.Fatal("idx_items_search is missing or invalid after schema init")
		}
		return
	}

	for _, name := range []string{"items_fts", "items_fts_ai", "items_fts_ad", "items_fts_au"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, name).Scan(&count); err != nil {
			t.Fatalf("query sqlite_master for %s: %v", name, err)
		}
		if count != 1 {
			t.Fatalf("%s is missing after schema init", name)
		}
	}
}
