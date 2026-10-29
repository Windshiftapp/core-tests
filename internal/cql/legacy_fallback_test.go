package cql

import (
	"strings"
	"testing"
)

func TestEvaluatorLegacyNameFallbackReferenceField(t *testing.T) {
	ev := NewEvaluator(nil, CustomFieldMap{"owner": {ID: 123, Kind: CFKindReference}}, "sqlite")

	sql, args, err := ev.EvaluateToSQL("cf_owner = 42")
	if err != nil {
		t.Fatalf("EvaluateToSQL returned error: %v", err)
	}
	if !strings.Contains(sql, `$."123"`) || !strings.Contains(sql, `$."owner"`) {
		t.Fatalf("SQL should check both numeric and legacy name keys, got: %s", sql)
	}
	if len(args) != 4 {
		t.Fatalf("args len = %d, want 4 for numeric/nested + legacy direct/nested comparisons: %#v", len(args), args)
	}
}

func TestEvaluatorLegacyNameFallbackMultiselectField(t *testing.T) {
	ev := NewEvaluator(nil, CustomFieldMap{"tags": {ID: 123, Kind: CFKindMultiselect}}, "sqlite")

	sql, args, err := ev.EvaluateToSQL("cf_tags IN (1, 2)")
	if err != nil {
		t.Fatalf("EvaluateToSQL returned error: %v", err)
	}
	if !strings.Contains(sql, `$."123"`) || !strings.Contains(sql, `$."tags"`) {
		t.Fatalf("SQL should check both numeric and legacy name keys, got: %s", sql)
	}
	if len(args) != 4 {
		t.Fatalf("args len = %d, want 4 for two values across two storage keys: %#v", len(args), args)
	}
}

func TestSQLGeneratorDoesNotEnableLegacyNameFallbackByDefault(t *testing.T) {
	gen := NewSQLGenerator(nil, CustomFieldMap{"tags": {ID: 123, Kind: CFKindMultiselect}}, "sqlite")
	ast, err := NewParser([]Token{
		{Type: IDENTIFIER, Value: "cf_tags"},
		{Type: IN, Value: "IN"},
		{Type: LPAREN, Value: "("},
		{Type: NUMBER, Value: "1"},
		{Type: RPAREN, Value: ")"},
		{Type: EOF},
	}).Parse()
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	sql, args, err := gen.GenerateSQL(ast)
	if err != nil {
		t.Fatalf("GenerateSQL returned error: %v", err)
	}
	if strings.Contains(sql, `$."tags"`) {
		t.Fatalf("low-level generator should not enable legacy fallback by default, got: %s", sql)
	}
	if len(args) != 1 {
		t.Fatalf("args len = %d, want 1 without fallback: %#v", len(args), args)
	}
}
