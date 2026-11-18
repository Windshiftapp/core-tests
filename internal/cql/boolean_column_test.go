package cql

import (
	"reflect"
	"testing"
)

// Column-backed boolean fields must bind the engine-native literal. Postgres
// rejects `boolean_col = 0` ("operator does not exist: boolean = integer"), so
// the generator binds a Go bool there; SQLite stores booleans as integers and
// keeps the 0/1 form. Regression: statusCompleted, inheritProject and isTask
// all compared a real boolean column against the 0/1 literal.
func TestGenerator_BooleanColumn_LiteralBinding(t *testing.T) {
	tests := []struct {
		query       string
		wantSQL     string
		sqliteArg   any
		postgresArg any
	}{
		{`statusCompleted = false`, `sc.is_completed = ?`, int64(0), false},
		{`statusCompleted = true`, `sc.is_completed = ?`, int64(1), true},
		{`statusCompleted != true`, `sc.is_completed != ?`, int64(1), true},
		{`inheritProject = false`, `i.inherit_project = ?`, int64(0), false},
		{`isTask = true`, `i.is_task = ?`, int64(1), true},
	}

	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			ast, err := parseQL(t, tc.query)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			for _, engine := range []struct {
				driver string
				want   any
			}{
				{"sqlite", tc.sqliteArg},
				{"postgres", tc.postgresArg},
			} {
				gen := NewSQLGenerator(nil, nil, engine.driver)
				sqlStr, args, err := gen.GenerateSQL(ast)
				if err != nil {
					t.Fatalf("%s generate: %v", engine.driver, err)
				}
				if sqlStr != tc.wantSQL {
					t.Fatalf("%s SQL = %q, want %q", engine.driver, sqlStr, tc.wantSQL)
				}
				if !reflect.DeepEqual(args, []any{engine.want}) {
					t.Fatalf("%s args = %#v, want %#v", engine.driver, args, []any{engine.want})
				}
			}
		})
	}
}

// IS NULL on a column-backed boolean stays on the raw column so a missing
// status category remains distinguishable from an incomplete one; the literal
// rewrite must not leak into null checks.
func TestGenerator_BooleanColumn_IsNullKeepsRawColumn(t *testing.T) {
	ast, err := parseQL(t, `statusCompleted IS NULL`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, driver := range []string{"sqlite", "postgres"} {
		gen := NewSQLGenerator(nil, nil, driver)
		sqlStr, args, err := gen.GenerateSQL(ast)
		if err != nil {
			t.Fatalf("%s generate: %v", driver, err)
		}
		if sqlStr != `sc.is_completed IS NULL` || len(args) != 0 {
			t.Fatalf("%s = %q %#v, want raw column IS NULL with no args", driver, sqlStr, args)
		}
	}
}
