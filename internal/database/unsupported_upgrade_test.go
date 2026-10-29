//go:build test

package database_test

import (
	"strings"
	"testing"

	"windshift/internal/testutils"
)

// The fixtures build an existing database whose core tables are present but
// whose canonical 0.8.5 checkpoint is missing or invalid. Direct SQL is the
// only way to produce these pre-0.8.5 states because the API always creates
// the current schema.
func TestDatabase_InitializeRefusesUnsupportedDatabases(t *testing.T) {
	cases := []struct {
		name    string
		seed    func(t *testing.T, tdb *testutils.TestDB)
		wantMsg string
	}{
		{
			name:    "missing schema checkpoint",
			wantMsg: "schema checkpoint is missing",
			seed:    func(t *testing.T, tdb *testutils.TestDB) {},
		},
		{
			name:    "older checkpoint version",
			wantMsg: `schema checkpoint "0.8.4" is unsupported`,
			seed: func(t *testing.T, tdb *testutils.TestDB) {
				datetimeType := "DATETIME"
				if testutils.IsPostgres() {
					datetimeType = "TIMESTAMPTZ"
				}
				if _, err := tdb.Exec(`CREATE TABLE schema_checkpoint (
					id INTEGER PRIMARY KEY CHECK (id = 1),
					version TEXT NOT NULL,
					applied_at ` + datetimeType + ` NOT NULL DEFAULT CURRENT_TIMESTAMP
				)`); err != nil {
					t.Fatalf("seed schema_checkpoint: %v", err)
				}
				if _, err := tdb.Exec(`INSERT INTO schema_checkpoint (id, version) VALUES (1, '0.8.4')`); err != nil {
					t.Fatalf("seed checkpoint row: %v", err)
				}
			},
		},
		{
			name:    "retired legacy rank phase",
			wantMsg: `has invalid global rank phase "legacy"`,
			seed: func(t *testing.T, tdb *testutils.TestDB) {
				datetimeType := "DATETIME"
				if testutils.IsPostgres() {
					datetimeType = "TIMESTAMPTZ"
				}
				for _, statement := range []string{
					`CREATE TABLE schema_checkpoint (
						id INTEGER PRIMARY KEY CHECK (id = 1),
						version TEXT NOT NULL,
						applied_at ` + datetimeType + ` NOT NULL DEFAULT CURRENT_TIMESTAMP
					)`,
					`INSERT INTO schema_checkpoint (id, version) VALUES (1, '0.8.5')`,
					`CREATE TABLE global_rank_state (
						id INTEGER PRIMARY KEY CHECK (id = 1),
						active_bucket INTEGER NOT NULL,
						phase TEXT NOT NULL,
						updated_at ` + datetimeType + ` NOT NULL DEFAULT CURRENT_TIMESTAMP
					)`,
					`INSERT INTO global_rank_state (id, active_bucket, phase) VALUES (1, 0, 'legacy')`,
				} {
					if _, err := tdb.Exec(statement); err != nil {
						t.Fatalf("seed incomplete checkpoint with %q: %v", statement, err)
					}
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tdb := testutils.CreateFreshDB(t, false)

			seedCoreProbeTables(t, tdb)
			tc.seed(t, tdb)

			err := tdb.Initialize()
			if err == nil {
				t.Fatal("expected startup refusal for unsupported database, got nil")
			}
			for _, want := range []string{"database startup refused", tc.wantMsg} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal error = %q, want it to contain %q", err.Error(), want)
				}
			}

			// The refusal must fire before any retained migration touches
			// the database: no catalog rows stamped, no migration effects.
			assertNoRetainedMigrationRows(t, tdb)
			if n := countFixtureRows(t, tdb, columnPresentQuery("workspaces", "is_template")); n != 0 {
				t.Fatalf("is_template column added despite refusal (count=%d)", n)
			}
			if n := countFixtureRows(t, tdb, columnPresentQuery("workflow_transitions", "from_all_statuses")); n != 0 {
				t.Fatalf("from_all_statuses column added despite refusal (count=%d)", n)
			}
		})
	}
}

func seedCoreProbeTables(t *testing.T, tdb *testutils.TestDB) {
	t.Helper()
	idColumn := "INTEGER PRIMARY KEY"
	if testutils.IsPostgres() {
		idColumn = "SERIAL PRIMARY KEY"
	}
	for _, table := range []string{"workspaces", "items", "users", "workflows"} {
		if _, err := tdb.Exec("CREATE TABLE " + table + " (id " + idColumn + ")"); err != nil {
			t.Fatalf("seed core table %s: %v", table, err)
		}
	}
}

func assertNoRetainedMigrationRows(t *testing.T, tdb *testutils.TestDB) {
	t.Helper()
	for _, version := range []string{
		"0000_baseline",
		"20260814_workflow_transitions_from_all",
		"20260816_portal_approval_vote_uniqueness",
		"20260815_workspaces_is_template",
	} {
		var n int
		if err := tdb.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, version).Scan(&n); err != nil {
			t.Fatalf("count stamps for %s: %v", version, err)
		}
		if n != 0 {
			t.Fatalf("retained migration %s stamped despite refusal (count=%d)", version, n)
		}
	}
}
