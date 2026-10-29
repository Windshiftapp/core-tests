//go:build test

package database

import (
	"strings"
	"testing"
)

// TestCatalog_MigrationBodiesNonEmpty catches catalog edits that gut a
// migration's driver body: an empty body stamps without running, which
// silently skips the schema change on that backend.
func TestCatalog_MigrationBodiesNonEmpty(t *testing.T) {
	for _, m := range Catalog {
		// The baseline marker is intentionally body-less on both backends.
		if m.Version == "0000_baseline" {
			continue
		}
		sqliteEmpty := strings.TrimSpace(m.SQLite) == ""
		pgEmpty := strings.TrimSpace(m.Postgres) == ""
		bothEmpty := sqliteEmpty && pgEmpty
		oneEmpty := sqliteEmpty != pgEmpty

		// Both empty on a real migration means the entry would stamp without
		// running anything — the silent-skip failure mode.
		if bothEmpty {
			t.Errorf("migration %s (%s) has empty bodies on both backends", m.Version, m.Name)
		}
		if oneEmpty && m.CheckSQLite != "" && m.CheckPostgres != "" {
			t.Errorf("migration %s (%s): checks exist for both backends but the %s body is empty",
				m.Version, m.Name, map[bool]string{true: "SQLite", false: "PostgreSQL"}[sqliteEmpty])
		}
	}
}
