//go:build test

package events

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

func pgPlaceholders(query string) string {
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func assertEventPlanAvoidsTableScan(t *testing.T, db database.Database, table, query string, args ...any) {
	t.Helper()
	var sqlQuery string
	if db.GetDriverName() == "postgres" {
		sqlQuery = "EXPLAIN " + pgPlaceholders(query)
	} else {
		sqlQuery = "EXPLAIN QUERY PLAN " + query
	}
	var rows *sql.Rows
	var err error
	if db.GetDriverName() == "postgres" {
		conn, connErr := db.GetDB().Conn(context.Background())
		if connErr != nil {
			t.Fatalf("acquire plan connection: %v", connErr)
		}
		defer func() { _ = conn.Close() }()
		if _, setErr := conn.ExecContext(context.Background(), "SET enable_seqscan = off"); setErr != nil {
			t.Fatalf("disable sequential scans: %v", setErr)
		}
		rows, err = conn.QueryContext(context.Background(), sqlQuery, args...)
	} else {
		rows, err = db.Query(sqlQuery, args...)
	}
	if err != nil {
		t.Fatalf("explain event query: %v\nquery: %s", err, sqlQuery)
	}
	defer func() { _ = rows.Close() }()
	var lines []string
	for rows.Next() {
		if db.GetDriverName() == "postgres" {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatalf("scan postgres plan: %v", err)
			}
			lines = append(lines, line)
			continue
		}
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan sqlite plan: %v", err)
		}
		lines = append(lines, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate plan: %v", err)
	}
	plan := strings.Join(lines, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Seq Scan on "+table) {
			t.Fatalf("%s plan uses a sequential scan:\n%s", table, plan)
		}
		if strings.Contains(line, "SCAN "+table) && !strings.Contains(line, "USING INDEX") && !strings.Contains(line, "USING COVERING INDEX") {
			t.Fatalf("%s plan scans the table:\n%s", table, plan)
		}
	}
}

func TestDurableEventHotQueryPlans(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.DB
	store := NewStore(db)
	configureConsumer(t, store, "projection", "item.changed")

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin seed: %v", err)
	}
	const rows = 5000
	for i := 0; i < rows; i++ {
		var eventID int
		if err := tx.QueryRow(`
			INSERT INTO domain_events (
				event_key, workspace_id, aggregate_type, aggregate_id, aggregate_sequence,
				event_type, payload_version, occurred_at, actor_kind, source_kind, payload
			) VALUES (?, 1, 'item', ?, ?, 'item.changed', 1, CURRENT_TIMESTAMP, 'system', 'test', '{}')
			RETURNING id
		`, fmt.Sprintf("plan-%d", i), fmt.Sprintf("%d", i), i+1).Scan(&eventID); err != nil {
			_ = tx.Rollback()
			t.Fatalf("seed event %d: %v", i, err)
		}
		if _, err := tx.Exec(`
			INSERT INTO domain_event_deliveries (event_id, consumer_key, state, next_attempt_at)
			VALUES (?, 'projection', 'pending', ?)
		`, eventID, time.Now().Add(-time.Hour)); err != nil {
			_ = tx.Rollback()
			t.Fatalf("seed delivery %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
	if _, err := db.Exec(`ANALYZE`); err != nil {
		t.Fatalf("ANALYZE: %v", err)
	}

	now := time.Now().Add(-time.Minute)
	assertEventPlanAvoidsTableScan(t, db, "domain_event_deliveries", claimQuery(db.GetDriverName()), "projection", now, now)
	assertEventPlanAvoidsTableScan(t, db, "domain_event_deliveries", gateSQL, int64(0), now, now)
	assertEventPlanAvoidsTableScan(t, db, "domain_events", gateSQL, int64(0), now, now)
}
