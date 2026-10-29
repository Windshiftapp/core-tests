//go:build test

package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// emailLoadRows lets a load run scale the seeded volume without making normal
// CI pay for millions of rows. The default is large enough for the planner to
// prefer the production indexes after ANALYZE.
func emailLoadRows() int {
	if raw := os.Getenv("WINDSHIFT_EMAIL_LOAD_ROWS"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return 20000
}

type emailLoadFixture struct {
	db        database.Database
	channelID int
	itemID    int
	userID    int
}

func seedEmailPipelineLoad(t *testing.T, rows int) emailLoadFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	ctx := context.Background()

	var channelID int
	if err := db.QueryRowContext(ctx, `
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Load', 'email', 'inbound', 'enabled', '{}')
		RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("insert load channel: %v", err)
	}
	var itemID int
	if err := db.QueryRowContext(ctx, `
		INSERT INTO items (workspace_id, workspace_item_number, title, frac_index)
		VALUES (?, 9001, 'Load item', '0.5')
		RETURNING id
	`, data.WorkspaceID).Scan(&itemID); err != nil {
		t.Fatalf("insert load item: %v", err)
	}
	seedLoadRows(t, db, channelID, itemID, data.UserID, rows)
	return emailLoadFixture{db: db, channelID: channelID, itemID: itemID, userID: data.UserID}
}

// seedLoadRows fills the tables the email pipeline reads at production shape.
func seedLoadRows(t testing.TB, db database.Database, channelID, itemID, userID, rows int) {
	t.Helper()
	old := time.Now().Add(-30 * 24 * time.Hour)
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin load seed: %v", err)
	}
	for i := 0; i < rows; i++ {
		messageID := fmt.Sprintf("<load-%d@example.com>", i)
		if _, err := tx.Exec(`
			INSERT INTO email_message_tracking
				(channel_id, message_id, dedup_key, in_reply_to, from_email, direction, processed_at)
			VALUES (?, ?, ?, ?, ?, 'inbound', ?)
		`, channelID, messageID, fmt.Sprintf("load-%d", i), "", fmt.Sprintf("sender%d@example.com", i%500), old); err != nil {
			_ = tx.Rollback()
			t.Fatalf("seed tracking row %d: %v", i, err)
		}
	}
	for i := 0; i < rows/4; i++ {
		if _, err := tx.Exec(`
			INSERT INTO notifications
				(user_id, title, message, type, timestamp, read, sent_at, email_delivery_state, authorization_scope)
			VALUES (?, 'Load', 'Load body', 'info', ?, false, NULL, ?, ?)
		`, userID, time.Now().Add(-time.Duration(i)*time.Minute),
			[]string{"pending", "failed", "sent"}[i%3], models.NotificationScopeSystem); err != nil {
			_ = tx.Rollback()
			t.Fatalf("seed notification %d: %v", i, err)
		}
	}
	for i := 0; i < rows/4; i++ {
		if _, err := tx.Exec(`
			INSERT INTO email_reply_outbox
				(kind, channel_id, item_id, to_email, subject, html_body, text_body,
				 message_id, from_email, next_attempt_at)
			VALUES ('reply', ?, ?, ?, 'Load', '<p>Load</p>', 'Load', ?, 'sender@example.com', ?)
		`, channelID, itemID, fmt.Sprintf("recipient%d@example.com", i), fmt.Sprintf("<out-%d@example.com>", i),
			time.Now().Add(-time.Duration(i)*time.Minute)); err != nil {
			_ = tx.Rollback()
			t.Fatalf("seed outbox row %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit load seed: %v", err)
	}
	if _, err := db.Exec(`ANALYZE`); err != nil {
		t.Fatalf("ANALYZE: %v", err)
	}
}

func queryPlanLines(t *testing.T, db database.Database, query string, args ...any) []string {
	t.Helper()
	var sqlQuery string
	var rows *sql.Rows
	var err error
	if db.GetDriverName() == "postgres" {
		sqlQuery = "EXPLAIN " + pgPlaceholders(query)
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
		sqlQuery = "EXPLAIN QUERY PLAN " + query
		rows, err = db.Query(sqlQuery, args...)
	}
	if err != nil {
		t.Fatalf("explain query: %v\n%s", err, query)
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
	return lines
}

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

func assertPlanAvoidsTableScan(t *testing.T, db database.Database, table, query string, args ...any) {
	t.Helper()
	lines := queryPlanLines(t, db, query, args...)
	plan := strings.Join(lines, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Seq Scan on "+table) {
			t.Fatalf("%s plan uses a sequential scan:\n%s\nquery:\n%s", table, plan, query)
		}
		if strings.Contains(line, "SCAN "+table) && !strings.Contains(line, "USING INDEX") && !strings.Contains(line, "USING COVERING INDEX") {
			t.Fatalf("%s plan scans the table:\n%s\nquery:\n%s", table, plan, query)
		}
	}
}

func TestEmailPipelineHotQueryPlans(t *testing.T) {
	rows := emailLoadRows()
	fixture := seedEmailPipelineLoad(t, rows)
	db := fixture.db
	now := time.Now().Add(-time.Hour)

	assertPlanAvoidsTableScan(t, db, "email_message_tracking", `
		SELECT COUNT(*) FROM email_message_tracking
		WHERE channel_id = ? AND LOWER(from_email) = ? AND processed_at >= ?
	`, fixture.channelID, "sender1@example.com", now)
	assertPlanAvoidsTableScan(t, db, "email_message_tracking", `
		SELECT item_id FROM email_message_tracking
		WHERE channel_id = ? AND message_id = ? LIMIT 1
	`, fixture.channelID, "<load-1@example.com>")
	assertPlanAvoidsTableScan(t, db, "email_message_tracking", `
		SELECT item_id FROM email_message_tracking
		WHERE in_reply_to = ? LIMIT 1
	`, "<load-1@example.com>")
	assertPlanAvoidsTableScan(t, db, "notifications", `
		SELECT user_id FROM notifications
		WHERE sent_at IS NULL AND read = false
		  AND email_delivery_state IN ('pending', 'failed')
		  AND authorization_scope IN ('system', 'workspace', 'asset')
		GROUP BY user_id
		ORDER BY MIN(timestamp) ASC
		LIMIT 50
	`)
	assertPlanAvoidsTableScan(t, db, "email_reply_outbox", `
		SELECT id FROM email_reply_outbox
		WHERE delivered_at IS NULL AND discarded_at IS NULL AND next_attempt_at <= CURRENT_TIMESTAMP
		ORDER BY created_at ASC
		LIMIT 100
	`)
}

func seedBenchmarkLoad(b *testing.B, rows int) emailLoadFixture {
	b.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(b.TempDir(), "email-load.db"))
	if err != nil {
		b.Fatalf("create load database: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		b.Fatalf("initialize load database: %v", err)
	}
	var workspaceID, userID, channelID, itemID int
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, key) VALUES ('Load', 'LOAD') RETURNING id
	`).Scan(&workspaceID); err != nil {
		b.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('load@example.com', 'load', 'Load', 'User', true) RETURNING id
	`).Scan(&userID); err != nil {
		b.Fatalf("insert user: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Load', 'email', 'inbound', 'enabled', '{}') RETURNING id
	`).Scan(&channelID); err != nil {
		b.Fatalf("insert channel: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, frac_index)
		VALUES (?, 9001, 'Load item', '0.5') RETURNING id
	`, workspaceID).Scan(&itemID); err != nil {
		b.Fatalf("insert item: %v", err)
	}
	seedLoadRows(b, db, channelID, itemID, userID, rows)
	return emailLoadFixture{db: db, channelID: channelID, itemID: itemID, userID: userID}
}

func BenchmarkEmailDigestRecipientScan(b *testing.B) {
	fixture := seedBenchmarkLoad(b, emailLoadRows())
	service := services.NewNotificationService(fixture.db, nil, nil, services.DefaultNotificationServiceConfig())
	b.Cleanup(func() { _ = service.Close() })
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := service.UnreadEmailBatches(50); err != nil {
			b.Fatalf("UnreadEmailBatches: %v", err)
		}
	}
}

func BenchmarkEmailOutboxClaim(b *testing.B) {
	fixture := seedBenchmarkLoad(b, emailLoadRows())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := fixture.db.Query(`
			SELECT id FROM email_reply_outbox
			WHERE delivered_at IS NULL AND discarded_at IS NULL AND next_attempt_at <= CURRENT_TIMESTAMP
			ORDER BY created_at ASC
			LIMIT 100
		`)
		if err != nil {
			b.Fatalf("query outbox: %v", err)
		}
		count := 0
		for rows.Next() {
			count++
		}
		_ = rows.Close()
		if count == 0 {
			b.Fatal("no outbox rows seeded")
		}
	}
}
