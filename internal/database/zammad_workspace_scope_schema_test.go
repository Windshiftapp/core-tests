//go:build test

package database_test

import (
	"strings"
	"testing"

	"windshift/internal/testutils"
)

// The fresh-install schema must carry the same Zammad workspace-scoping
// objects and OAuth-generation defaults that the migrations create on
// installs upgraded from 0.8.8. Regression guard: the schema files were
// missing zammad_connections.applies_to_all_workspaces,
// zammad_connections.allowed_group_ids, zammad_connection_workspaces, and
// the oauth_generation DEFAULT 1 on the OAuth tables.
func TestFreshSchemaCarriesZammadWorkspaceScope(t *testing.T) {
	// Database-initialization test: driving the raw production schema through
	// Initialize() is the behavior under test, so the no-init fixture is the
	// documented exception to the CreateTestDB default.
	tdb := testutils.CreateFreshDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("fresh database initialization failed: %v", err)
	}

	// Connection scoping columns with their exact defaults.
	notNull, def := columnNullableAndDefault(t, tdb, "zammad_connections", "applies_to_all_workspaces")
	if notNull != 1 || def != "false" {
		t.Fatalf("zammad_connections.applies_to_all_workspaces notnull=%d default=%q; want notnull=1 default=false", notNull, def)
	}
	notNull, def = columnNullableAndDefault(t, tdb, "zammad_connections", "allowed_group_ids")
	if notNull != 1 || def != "[]" {
		t.Fatalf("zammad_connections.allowed_group_ids notnull=%d default=%q; want notnull=1 default='[]'", notNull, def)
	}

	// Workspace membership table with its composite key, CASCADE foreign
	// keys, and workspace lookup index.
	tdb.AssertTableExists(t, "zammad_connection_workspaces")
	tdb.AssertIndexExists(t, "idx_zammad_connection_workspaces_workspace")
	assertPrimaryKeyColumns(t, tdb, "zammad_connection_workspaces", []string{"provider_id", "workspace_id"})
	assertCascadeForeignKeys(t, tdb, "zammad_connection_workspaces", 2)

	// OAuth generations default to 1 on both OAuth tables.
	for _, table := range []string{"zammad_oauth_tokens", "zammad_oauth_state"} {
		notNull, def := columnNullableAndDefault(t, tdb, table, "oauth_generation")
		if notNull != 1 || def != "1" {
			t.Fatalf("%s.oauth_generation notnull=%d default=%q; want notnull=1 default=1", table, notNull, def)
		}
	}
}

func columnNullableAndDefault(t *testing.T, tdb *testutils.TestDB, table, column string) (notNull int, def string) {
	t.Helper()
	var err error
	if testutils.IsPostgres() {
		err = tdb.QueryRow(`
			SELECT CASE WHEN is_nullable='NO' THEN 1 ELSE 0 END, COALESCE(column_default, '')
			FROM information_schema.columns
			WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2
		`, table, column).Scan(&notNull, &def)
		if def == "'[]'::text" {
			def = "[]"
		}
	} else {
		// "notnull" must be quoted: it is a keyword in SQLite expressions.
		err = tdb.QueryRow(`
			SELECT "notnull", COALESCE(dflt_value, '')
			FROM pragma_table_info(?) WHERE name=?
		`, table, column).Scan(&notNull, &def)
	}
	// SQLite reports the raw literal ('[]'); PostgreSQL decorates types
	// ('[]'::text). Normalize both to the bare value.
	if len(def) >= 2 && strings.HasPrefix(def, "'") && strings.HasSuffix(def, "'") {
		def = def[1 : len(def)-1]
	}
	if err != nil {
		t.Fatalf("read %s.%s shape: %v", table, column, err)
	}
	return notNull, def
}

func assertPrimaryKeyColumns(t *testing.T, tdb *testutils.TestDB, table string, want []string) {
	t.Helper()
	var got []string
	if testutils.IsPostgres() {
		rows, err := tdb.Query(`
			SELECT kcu.column_name
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage kcu
			  ON kcu.constraint_name = tc.constraint_name
			 AND kcu.constraint_schema = tc.constraint_schema
			WHERE tc.table_schema = current_schema() AND tc.table_name = $1
			  AND tc.constraint_type = 'PRIMARY KEY'
			ORDER BY kcu.ordinal_position
		`, table)
		if err != nil {
			t.Fatalf("read primary key of %s: %v", table, err)
		}
		defer rows.Close()
		for rows.Next() {
			var column string
			if err := rows.Scan(&column); err != nil {
				t.Fatalf("scan primary key column: %v", err)
			}
			got = append(got, column)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate primary key columns: %v", err)
		}
	} else {
		rows, err := tdb.Query(`SELECT name FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk`, table)
		if err != nil {
			t.Fatalf("read primary key of %s: %v", table, err)
		}
		defer rows.Close()
		for rows.Next() {
			var column string
			if err := rows.Scan(&column); err != nil {
				t.Fatalf("scan primary key column: %v", err)
			}
			got = append(got, column)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate primary key columns: %v", err)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%s primary key = %v; want %v", table, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s primary key = %v; want %v", table, got, want)
		}
	}
}

func assertCascadeForeignKeys(t *testing.T, tdb *testutils.TestDB, table string, want int) {
	t.Helper()
	var got int
	var err error
	if testutils.IsPostgres() {
		err = tdb.QueryRow(`
			SELECT COUNT(*)
			FROM information_schema.referential_constraints
			WHERE constraint_schema = current_schema()
			  AND constraint_name IN (
				SELECT constraint_name FROM information_schema.table_constraints
				WHERE table_schema = current_schema() AND table_name = $1
				  AND constraint_type = 'FOREIGN KEY'
			  )
			  AND delete_rule = 'CASCADE'
		`, table).Scan(&got)
	} else {
		err = tdb.QueryRow(
			`SELECT COUNT(*) FROM pragma_foreign_key_list(?) WHERE on_delete = 'CASCADE'`,
			table,
		).Scan(&got)
	}
	if err != nil {
		t.Fatalf("read foreign keys of %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s CASCADE foreign keys = %d; want %d", table, got, want)
	}
}
