//go:build test

package cql

import (
	"strings"
	"testing"
)

func TestClassifyCustomFieldKind_MultiMilestone(t *testing.T) {
	if got := ClassifyCustomFieldKind("multi_milestone"); got != CFKindMultiselect {
		t.Fatalf("ClassifyCustomFieldKind(multi_milestone) = %v, want CFKindMultiselect", got)
	}
}

// Multi-milestone fields store an array of milestone ids, so CQL must lower
// them through the multiselect array-containment path.
func TestGenerator_MultiMilestone_EqualsEmitsArrayContains(t *testing.T) {
	cfMap := CustomFieldMap{
		"affectedversion": {ID: 42, Kind: CFKindMultiselect, FieldType: "multi_milestone"},
	}

	t.Run("sqlite", func(t *testing.T) {
		sqlStr, args := generateForTest(t, `affectedVersion = 4`, cfMap, "sqlite")
		if !strings.Contains(sqlStr, "json_each") {
			t.Fatalf("expected json_each subquery, got: %s", sqlStr)
		}
		if !strings.Contains(sqlStr, `$."42"`) {
			t.Fatalf("expected JSON path for field 42, got: %s", sqlStr)
		}
		if len(args) != 1 || args[0] != "4" {
			t.Fatalf("expected text-coerced bound arg \"4\", got: %#v", args)
		}
	})

	t.Run("postgres", func(t *testing.T) {
		sqlStr, args := generateForTest(t, `affectedVersion = 4`, cfMap, "postgres")
		if !strings.Contains(sqlStr, "jsonb_array_elements_text") {
			t.Fatalf("expected jsonb_array_elements_text subquery, got: %s", sqlStr)
		}
		if !strings.Contains(sqlStr, "->'42'") {
			t.Fatalf("expected array extract for field 42, got: %s", sqlStr)
		}
		if len(args) != 1 || args[0] != "4" {
			t.Fatalf("expected text-coerced bound arg \"4\", got: %#v", args)
		}
	})
}

func TestGenerator_BareCustomFieldSyntaxesShareLowering(t *testing.T) {
	cfMap := CustomFieldMap{
		"affectedversion": {ID: 42, Kind: CFKindMultiselect, FieldType: "multi_milestone"},
	}
	queries := []string{
		`affectedVersion = 4`,
		`cf_affectedVersion = 4`,
		`custom.affectedVersion = 4`,
		`cfid_42 = 4`,
	}

	var wantSQL string
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			sqlStr, args := generateForTest(t, query, cfMap, "sqlite")
			if wantSQL == "" {
				wantSQL = sqlStr
			} else if sqlStr != wantSQL {
				t.Fatalf("SQL for %q = %s, want the same lowering as the bare form: %s", query, sqlStr, wantSQL)
			}
			if len(args) != 1 || args[0] != "4" {
				t.Fatalf("args = %#v, want [\"4\"]", args)
			}
		})
	}
}

func TestGenerator_BareCustomField_INAndNotEquals(t *testing.T) {
	cfMap := CustomFieldMap{
		"affectedversion": {ID: 42, Kind: CFKindMultiselect, FieldType: "multi_milestone"},
	}

	t.Run("IN contains any", func(t *testing.T) {
		sqlStr, args := generateForTest(t, `affectedVersion IN (4, 5)`, cfMap, "sqlite")
		if !strings.Contains(sqlStr, "json_each") {
			t.Fatalf("expected array containment, got: %s", sqlStr)
		}
		if len(args) != 2 || args[0] != "4" || args[1] != "5" {
			t.Fatalf("args = %#v, want [\"4\" \"5\"]", args)
		}
	})

	t.Run("not equals negates containment", func(t *testing.T) {
		sqlStr, _ := generateForTest(t, `affectedVersion != 4`, cfMap, "sqlite")
		if !strings.HasPrefix(strings.TrimSpace(sqlStr), "NOT EXISTS") {
			t.Fatalf("expected NOT EXISTS for !=, got: %s", sqlStr)
		}
	})
}

// A bare system field always wins over a custom field with the same name.
func TestGenerator_BareSystemFieldTakesPrecedenceOverCustomField(t *testing.T) {
	cfMap := CustomFieldMap{
		"status": {ID: 42, Kind: CFKindMultiselect, FieldType: "multi_milestone"},
	}
	sqlStr, args := generateForTest(t, `status = "open"`, cfMap, "sqlite")

	if strings.Contains(sqlStr, "json_each") {
		t.Fatalf("system status field must not lower through custom-field storage, got: %s", sqlStr)
	}
	if !strings.Contains(sqlStr, "st.name") {
		t.Fatalf("expected the system status column, got: %s", sqlStr)
	}
	if len(args) != 1 || args[0] != "open" {
		t.Fatalf("args = %#v, want [\"open\"]", args)
	}
}

// Bare scalar custom fields also resolve after system fields.
func TestGenerator_BareScalarCustomFieldResolves(t *testing.T) {
	cfMap := CustomFieldMap{
		"severity": {ID: 7, Kind: CFKindScalar, FieldType: "text"},
	}
	sqlStr, args := generateForTest(t, `severity = "high"`, cfMap, "sqlite")

	if !strings.Contains(sqlStr, `$."7"`) {
		t.Fatalf("expected JSON path for scalar field 7, got: %s", sqlStr)
	}
	if len(args) != 1 || args[0] != "high" {
		t.Fatalf("args = %#v, want [\"high\"]", args)
	}
}

func generateForTest(t *testing.T, query string, cfMap CustomFieldMap, driver string) (string, []any) {
	t.Helper()
	ast, err := parseQL(t, query)
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	gen := NewSQLGenerator(map[string]int{}, cfMap, driver)
	sqlStr, args, err := gen.GenerateSQL(ast)
	if err != nil {
		t.Fatalf("generate %q: %v", query, err)
	}
	return sqlStr, args
}
