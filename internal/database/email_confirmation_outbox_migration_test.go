//go:build test

package database_test

import (
	"testing"

	"windshift/internal/testutils"
)

// seedLegacyEmailReplyOutbox recreates the outbox shape before WI-1769: a NOT
// NULL comment_id and no kind column. Direct SQL is required because the API
// cannot intentionally produce the pre-upgrade shape; this is a migration
// test.
func seedLegacyEmailReplyOutbox(t *testing.T, tdb *testutils.TestDB) {
	t.Helper()
	idColumn := "INTEGER PRIMARY KEY"
	timestampType := "DATETIME"
	if testutils.IsPostgres() {
		idColumn = "SERIAL PRIMARY KEY"
		timestampType = "TIMESTAMPTZ"
	}
	statements := []string{
		`CREATE TABLE channels (id ` + idColumn + `)`,
		`CREATE TABLE comments (id ` + idColumn + `)`,
		`INSERT INTO channels (id) VALUES (1)`,
		`INSERT INTO comments (id) VALUES (1)`,
		`INSERT INTO items (id, workspace_id, frac_index) VALUES (1, 1, '0|a0')`,
		`CREATE TABLE email_reply_outbox (
			id ` + idColumn + `,
			comment_id INTEGER NOT NULL,
			channel_id INTEGER NOT NULL,
			item_id INTEGER NOT NULL,
			to_email TEXT NOT NULL,
			to_name TEXT NOT NULL DEFAULT '',
			subject TEXT NOT NULL,
			html_body TEXT NOT NULL,
			text_body TEXT NOT NULL,
			message_id TEXT NOT NULL,
			in_reply_to TEXT NOT NULL DEFAULT '',
			references_json TEXT NOT NULL DEFAULT '[]',
			from_email TEXT NOT NULL,
			from_name TEXT NOT NULL DEFAULT '',
			reply_to_email TEXT NOT NULL DEFAULT '',
			reply_to_name TEXT NOT NULL DEFAULT '',
			attempt_count INTEGER NOT NULL DEFAULT 0,
			next_attempt_at ` + timestampType + ` NOT NULL DEFAULT CURRENT_TIMESTAMP,
			lease_owner TEXT,
			last_error TEXT,
			delivered_at ` + timestampType + `,
			discarded_at ` + timestampType + `,
			created_at ` + timestampType + ` NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at ` + timestampType + ` NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (comment_id) REFERENCES comments(id) ON DELETE CASCADE,
			FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE,
			FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_email_reply_outbox_pending ON email_reply_outbox(delivered_at, next_attempt_at)`,
		`CREATE UNIQUE INDEX uq_email_reply_outbox_comment_recipient ON email_reply_outbox(comment_id, to_email)`,
		`INSERT INTO email_reply_outbox (
			id, comment_id, channel_id, item_id, to_email, subject, html_body, text_body, message_id, from_email
		) VALUES (9001, 1, 1, 1, 'customer@example.com', 'Re: Help', '<p>body</p>', 'body', '<ws-comment-1@test>', 'noreply@test.com')`,
	}
	for _, statement := range statements {
		if _, err := tdb.Exec(statement); err != nil {
			t.Fatalf("seed legacy outbox with %q: %v", statement, err)
		}
	}
}

// The WI-1769 rebuild must preserve existing reply rows, accept a receipt with
// no comment, and keep both kinds idempotent.
func TestMigration_EmailConfirmationOutbox_RebuildsLegacyRows(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)
	t.Cleanup(func() { _ = tdb.Close() })
	seedCanonicalSchemaCheckpoint(t, tdb)
	seedMinimalWorkspaces(t, tdb)
	seedLegacyEmailReplyOutbox(t, tdb)

	createSchemaMigrationsTable(t, tdb)
	stampAllExcept(t, tdb, "20261103_email_confirmation_outbox", "")
	if err := tdb.Initialize(); err != nil {
		t.Fatalf("apply confirmation outbox migration: %v", err)
	}
	if err := tdb.Initialize(); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}

	var kind string
	var commentID int
	if err := tdb.QueryRow(`SELECT kind, comment_id FROM email_reply_outbox WHERE id = 9001`).Scan(&kind, &commentID); err != nil {
		t.Fatalf("read migrated reply row: %v", err)
	}
	if kind != "reply" || commentID != 1 {
		t.Fatalf("migrated row = kind %q comment %d, want reply/1", kind, commentID)
	}

	insertConfirmation := func(messageID string) error {
		_, err := tdb.Exec(`
			INSERT INTO email_reply_outbox (
				kind, channel_id, item_id, to_email, subject, html_body, text_body, message_id, from_email
			) VALUES ('intake_confirmation', 1, 1, 'customer@example.com', 'Received', '<p>ok</p>', 'ok', ?, 'noreply@test.com')
		`, messageID)
		return err
	}
	if err := insertConfirmation("<ws-confirm-1@test>"); err != nil {
		t.Fatalf("insert confirmation with NULL comment_id: %v", err)
	}
	if err := insertConfirmation("<ws-confirm-2@test>"); err == nil {
		t.Fatal("second confirmation for the same item was accepted, want unique-index violation")
	}

	if _, err := tdb.Exec(`
		INSERT INTO email_reply_outbox (
			comment_id, channel_id, item_id, to_email, subject, html_body, text_body, message_id, from_email
		) VALUES (1, 1, 1, 'customer@example.com', 'Re: Help', '<p>body</p>', 'body', '<dup@test>', 'noreply@test.com')
	`); err == nil {
		t.Fatal("duplicate reply for the same comment recipient was accepted, want unique-index violation")
	}
}
