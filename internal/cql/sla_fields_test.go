//go:build test

package cql

import (
	"strings"
	"testing"
	"time"
)

func generateSLA(t *testing.T, query string) (string, []any) {
	t.Helper()
	tokens, err := NewTokenizer(query).Tokenize()
	if err != nil {
		t.Fatalf("tokenize %q: %v", query, err)
	}
	ast, err := NewParser(tokens).Parse()
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	generator := NewSQLGenerator(map[string]int{}, nil, "sqlite")
	sql, args, err := generator.GenerateSQLAt(ast, time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generate %q: %v", query, err)
	}
	return sql, args
}

func TestSLAFieldGeneratesStoredColumnPredicate(t *testing.T) {
	t.Parallel()
	sql, args := generateSLA(t, "slaBreached = true")
	for _, fragment := range []string{"item_sla_cycles", "breached_at IS NOT NULL", "next_deadline_at <=", "sla_c.status = 'ongoing'"} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("generated SQL %q missing %q", sql, fragment)
		}
	}
	if len(args) == 0 {
		t.Fatalf("expected the evaluation time argument, got none")
	}
}

func TestSLAPausedAndEverBreached(t *testing.T) {
	t.Parallel()
	pausedSQL, _ := generateSLA(t, "slaPaused = true")
	if !strings.Contains(pausedSQL, "pause_started_at IS NOT NULL") {
		t.Fatalf("paused SQL = %q", pausedSQL)
	}
	everSQL, _ := generateSLA(t, "slaEverBreached = true")
	if strings.Contains(everSQL, "status = 'ongoing'") {
		t.Fatalf("ever-breached SQL must not require an ongoing cycle: %q", everSQL)
	}
	if !strings.Contains(everSQL, "breached_at IS NOT NULL") {
		t.Fatalf("ever-breached SQL = %q", everSQL)
	}
}

func TestSLAMetricScopedFunction(t *testing.T) {
	t.Parallel()
	sql, args := generateSLA(t, "slaBreached('First response') = true")
	if !strings.Contains(sql, "sla_c.metric_id IN (SELECT id FROM sla_metrics WHERE name = ?)") {
		t.Fatalf("scoped SQL = %q", sql)
	}
	found := false
	for _, arg := range args {
		if arg == "First response" {
			found = true
		}
	}
	if !found {
		t.Fatalf("metric name argument missing from %#v", args)
	}
}

func TestSLAMetricBareIdentifierTreatedAsName(t *testing.T) {
	t.Parallel()
	tokens, err := NewTokenizer("slaBreached(cf_title) = true").Tokenize()
	if err != nil {
		t.Fatalf("tokenize: %v", err)
	}
	ast, err := NewParser(tokens).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	generator := NewSQLGenerator(map[string]int{}, nil, "sqlite")
	if _, _, err := generator.GenerateSQLAt(ast, time.Now()); err != nil {
		t.Fatalf("a bare identifier should be treated as a metric name, got %v", err)
	}
}

func TestSLACompletionFieldsPresent(t *testing.T) {
	t.Parallel()
	names := map[string]bool{}
	for _, field := range ItemCompletionFields() {
		names[field.Name] = true
	}
	for _, want := range []string{"slaBreached", "slaPaused", "slaRunning", "slaEverBreached", "slaCompleted"} {
		if !names[want] {
			t.Fatalf("completion catalog missing %s", want)
		}
	}
}

func TestSLADeadlineAcceptsRelativeWindow(t *testing.T) {
	t.Parallel()
	sql, args := generateSLA(t, "slaRunning = true AND slaDeadline <= 2d")
	if !strings.Contains(sql, "next_deadline_at") {
		t.Fatalf("slaDeadline SQL = %q", sql)
	}
	// The relative literal resolves to evaluation time + 2 days.
	found := false
	for _, arg := range args {
		if value, ok := arg.(time.Time); ok && value.Equal(time.Date(2025, 1, 10, 12, 0, 0, 0, time.UTC)) {
			found = true
		}
	}
	if !found {
		t.Fatalf("relative deadline argument missing from %#v", args)
	}
}

