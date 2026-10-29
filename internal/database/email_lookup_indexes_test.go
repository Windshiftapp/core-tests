//go:build test

package database_test

import (
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// TestEmailLookupIndexesExist verifies the per-email lookups added for
// WI-1767 have backing indexes after a fresh schema init on both engines.
func TestEmailLookupIndexesExist(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	indexes := []string{
		"idx_portal_customers_email_lower",
		"idx_email_message_tracking_intake_sender_time",
		"idx_email_message_tracking_item_time",
	}
	for _, name := range indexes {
		if !indexExists(t, db, name) {
			t.Fatalf("index %s is missing after schema init", name)
		}
	}
}

// TestEmailLookupQueriesUseIndexes is the regression guard for WI-1767: the
// queries that run for every inbound or outbound email must stay index
// searches as the tables grow. A future edit that drops the LOWER(...) form
// or the indexed columns fails here instead of silently reverting to a scan.
func TestEmailLookupQueriesUseIndexes(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	cases := []struct {
		name  string
		query string
		args  []interface{}
		index string
	}{
		{
			name:  "portal customer find-or-create by normalized email",
			query: "SELECT id FROM portal_customers WHERE LOWER(email) = ?",
			args:  []interface{}{"sender@example.com"},
			index: "idx_portal_customers_email_lower",
		},
		{
			name: "intake sender rate limit",
			query: `SELECT COUNT(*) FROM email_message_tracking
				WHERE intake_id = ? AND LOWER(from_email) = ? AND direction = 'inbound'
				  AND item_id IS NOT NULL AND comment_id IS NULL AND processed_at > ?`,
			args:  []interface{}{1, "sender@example.com", "2026-01-01 00:00:00"},
			index: "idx_email_message_tracking_intake_sender_time",
		},
		{
			name: "thread participant check",
			query: `SELECT COUNT(*) FROM email_message_tracking
				WHERE item_id = ? AND LOWER(from_email) = ?`,
			args:  []interface{}{1, "sender@example.com"},
			index: "idx_email_message_tracking_item_time",
		},
		{
			name: "reply threading headers",
			query: `SELECT message_id, subject FROM email_message_tracking
				WHERE item_id = ? ORDER BY processed_at ASC`,
			args:  []interface{}{1},
			index: "idx_email_message_tracking_item_time",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := explainQueryPlan(t, db, tc.query, tc.args...)
			if !strings.Contains(plan, tc.index) {
				t.Fatalf("query plan does not use %s:\n%s", tc.index, plan)
			}
		})
	}
}

// explainQueryPlan returns the engine's plan text for query. PostgreSQL
// sequential scans are disabled so a small table cannot hide a missing index.
func explainQueryPlan(t *testing.T, db database.Database, query string, args ...interface{}) string {
	t.Helper()

	var plan strings.Builder
	if db.GetDriverName() == "postgres" {
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("begin explain transaction: %v", err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec("SET LOCAL enable_seqscan = off"); err != nil {
			t.Fatalf("disable sequential scan: %v", err)
		}
		rows, err := tx.Query("EXPLAIN "+query, args...)
		if err != nil {
			t.Fatalf("explain query: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatalf("scan PostgreSQL plan: %v", err)
			}
			plan.WriteString(line + "\n")
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate PostgreSQL plan: %v", err)
		}
		return plan.String()
	}

	rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan SQLite plan: %v", err)
		}
		plan.WriteString(detail + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate SQLite plan: %v", err)
	}
	return plan.String()
}
