//go:build test

package cql

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTokenizer_RelativeDateLiteral(t *testing.T) {
	tokens, err := NewTokenizer(`completed_at >= -90d`).Tokenize()
	if err != nil {
		t.Fatalf("tokenize: %v", err)
	}
	if len(tokens) != 4 {
		t.Fatalf("tokens = %#v, want field, operator, relative literal, EOF", tokens)
	}
	if tokens[2].Type != RelativeDate || tokens[2].Value != "-90d" {
		t.Fatalf("relative token = %#v, want RELATIVE_DATE -90d", tokens[2])
	}
}

func TestParser_RelativeDateValidation(t *testing.T) {
	invalidQueries := []string{
		`created_at >= +30d`,
		`created_at >= 1.5d`,
		`created_at >= 90x`,
		`created_at >= 90D`,
		`created_at >= 999999999999999999999999999999999999d`,
	}
	for _, query := range invalidQueries {
		t.Run(query, func(t *testing.T) {
			if _, err := parseQL(t, query); err == nil {
				t.Fatalf("parse %q succeeded, want validation error", query)
			}
		})
	}
}

func TestParser_IsEmptyCanonicalizesToNull(t *testing.T) {
	cases := []struct {
		emptyQuery string
		nullQuery  string
	}{
		{emptyQuery: `milestonename IS EMPTY`, nullQuery: `milestonename IS NULL`},
		{emptyQuery: `milestonename IS NOT EMPTY`, nullQuery: `milestonename IS NOT NULL`},
	}
	for _, tc := range cases {
		t.Run(tc.emptyQuery, func(t *testing.T) {
			emptyAST, err := parseQL(t, tc.emptyQuery)
			if err != nil {
				t.Fatalf("parse empty query: %v", err)
			}
			nullAST, err := parseQL(t, tc.nullQuery)
			if err != nil {
				t.Fatalf("parse null query: %v", err)
			}
			if !reflect.DeepEqual(emptyAST, nullAST) {
				t.Fatalf("AST for %q = %#v, want %#v", tc.emptyQuery, emptyAST, nullAST)
			}
		})
	}
}

func TestGenerator_RelativeDateUsesOneEvaluationTime(t *testing.T) {
	ast, err := parseQL(t, `completed_at >= -90d AND updated_at < 30m AND created_at = now()`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	evaluationTime := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	gen := NewSQLGenerator(nil, nil, "sqlite")
	sqlStr, args, err := gen.GenerateSQLAt(ast, evaluationTime)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(sqlStr, "CASE") || !strings.Contains(sqlStr, "item_history") {
		t.Fatalf("SQL = %q, want virtual completed_at expression", sqlStr)
	}
	if len(args) != 3 {
		t.Fatalf("args = %#v, want three temporal values", args)
	}

	want := []time.Time{
		evaluationTime.Add(-90 * 24 * time.Hour),
		evaluationTime.Add(30 * time.Minute),
		evaluationTime,
	}
	for i, wantTime := range want {
		got, ok := args[i].(time.Time)
		if !ok || !got.Equal(wantTime) {
			t.Errorf("args[%d] = %#v, want %v", i, args[i], wantTime)
		}
	}
}

func TestGenerator_EndOfDayIncludesFinalNanosecond(t *testing.T) {
	ast, err := parseQL(t, `created_at <= endofday()`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	evaluationTime := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	_, args, err := NewSQLGenerator(nil, nil, "sqlite").GenerateSQLAt(ast, evaluationTime)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	want := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC).Add(-time.Nanosecond)
	if len(args) != 1 {
		t.Fatalf("args = %#v, want one end-of-day value", args)
	}
	got, ok := args[0].(time.Time)
	if !ok || !got.Equal(want) {
		t.Fatalf("endofday() = %#v, want %v", args[0], want)
	}
}

func TestGenerator_RelativeDateRejectsDateOnlyAndUnitlessValues(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{name: "date-only field", query: `due_date >= -1d`, want: "instant field"},
		{name: "unitless timestamp value", query: `created_at >= 90`, want: "requires a unit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ast, err := parseQL(t, tc.query)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, _, err = NewSQLGenerator(nil, nil, "sqlite").GenerateSQLAt(ast, time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want message containing %q", err, tc.want)
			}
		})
	}
}

func TestGenerator_NestedRelativeDateSharesEvaluationTime(t *testing.T) {
	ast, err := parseQL(t, `childrenOf("completed_at >= -90d")`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	evaluationTime := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	sqlStr, args, err := NewSQLGenerator(nil, nil, "sqlite").GenerateSQLAt(ast, evaluationTime)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(sqlStr, "inner_sc") || !strings.Contains(sqlStr, "inner_i.created_at") {
		t.Fatalf("SQL = %q, want nested status-category and completion aliases", sqlStr)
	}
	if len(args) != 1 {
		t.Fatalf("args = %#v, want one nested relative value", args)
	}
	got, ok := args[0].(time.Time)
	if !ok || !got.Equal(evaluationTime.Add(-90*24*time.Hour)) {
		t.Fatalf("nested relative arg = %#v, want %v", args[0], evaluationTime.Add(-90*24*time.Hour))
	}
}

func TestGenerator_AssetLinkedOfNestedRelativeDateAddsCategoryJoin(t *testing.T) {
	ast, err := parseQL(t, `linkedOf("relates", "completed_at >= -1d")`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	evaluationTime := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	sqlStr, args, err := NewAssetSQLGenerator(nil, nil, nil, "sqlite").GenerateSQLAt(ast, evaluationTime)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if strings.Count(sqlStr, "LEFT JOIN status_categories inner_sc") != 2 {
		t.Fatalf("SQL = %q, want category join in both inner subqueries", sqlStr)
	}
	if len(args) != 4 {
		t.Fatalf("args = %#v, want two link labels and two inner relative values", args)
	}
	for _, index := range []int{2, 3} {
		got, ok := args[index].(time.Time)
		if !ok || !got.Equal(evaluationTime.Add(-24*time.Hour)) {
			t.Errorf("args[%d] = %#v, want %v", index, args[index], evaluationTime.Add(-24*time.Hour))
		}
	}
}
