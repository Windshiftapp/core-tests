//go:build test

package database_test

import (
	"database/sql"
	"testing"

	"windshift/internal/testutils"
)

// seedLegacyIntakes creates the pre-reshape intakes shape (portal XOR
// workspace target) plus the channel and request-type rows the backfill reads.
// Direct SQL is required because the fresh schema no longer produces this
// shape.
func seedLegacyIntakes(t *testing.T, tdb *testutils.TestDB) {
	t.Helper()

	idColumn := "INTEGER PRIMARY KEY AUTOINCREMENT"
	createdAt := "DATETIME DEFAULT CURRENT_TIMESTAMP"
	if testutils.IsPostgres() {
		idColumn = "SERIAL PRIMARY KEY"
		createdAt = "TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP"
	}

	statements := []string{
		`CREATE TABLE channels (id ` + idColumn + `, name TEXT, type TEXT NOT NULL, config TEXT)`,
		`CREATE TABLE request_types (id ` + idColumn + `, channel_id INTEGER NOT NULL, item_type_id INTEGER, workspace_id INTEGER, kind TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE intakes (
			id ` + idColumn + `,
			mailbox_id INTEGER NOT NULL,
			folder TEXT NOT NULL DEFAULT 'INBOX',
			target_type TEXT NOT NULL,
			target_id INTEGER NOT NULL,
			request_type_id INTEGER,
			item_type_id INTEGER,
			rate_limit_per_hour INTEGER,
			processing_disposition TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'enabled',
			created_at ` + createdAt + `,
			updated_at ` + createdAt + `
		)`,
		`INSERT INTO channels (id, name, type, config) VALUES
			(1, 'Mailbox', 'email', '{}'),
			(10, 'Portal', 'portal', '{"portal_workspace_ids":[2]}')`,
		`INSERT INTO request_types (id, channel_id, item_type_id, workspace_id, kind) VALUES (100, 10, 5, 2, 'email')`,
		`INSERT INTO intakes (id, mailbox_id, folder, target_type, target_id, item_type_id) VALUES
			(1, 1, 'INBOX', 'workspace', 2, 5),
			(2, 1, 'Support', 'portal', 10, NULL)`,
	}
	for _, statement := range statements {
		if _, err := tdb.Exec(statement); err != nil {
			t.Fatalf("seed legacy intakes with %q: %v", statement, err)
		}
	}
}

// The reshape keeps workspace intakes on their workspace and converts portal
// intakes into a portal link plus the workspace/item type their system Email
// request type already carried.
func TestIntakeWorkspacePortalMigration_BackfillsLegacyTargets(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)
	seedCanonicalSchemaCheckpoint(t, tdb)
	seedMinimalWorkspaces(t, tdb)
	seedLegacyIntakes(t, tdb)

	createSchemaMigrationsTable(t, tdb)
	stampAllExcept(t, tdb, "20261026_intake_workspace_portal", "")

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run intake workspace/portal migration: %v", err)
	}

	var workspaceID int
	var portalChannel sql.NullInt64
	if err := tdb.QueryRow(
		`SELECT workspace_id, portal_channel_id FROM intakes WHERE id = 1`,
	).Scan(&workspaceID, &portalChannel); err != nil {
		t.Fatalf("read workspace intake: %v", err)
	}
	if workspaceID != 2 {
		t.Fatalf("workspace intake workspace_id = %d, want 2", workspaceID)
	}
	if portalChannel.Valid {
		t.Fatalf("workspace intake portal_channel_id = %d, want NULL", portalChannel.Int64)
	}

	var itemTypeID int
	if err := tdb.QueryRow(
		`SELECT workspace_id, portal_channel_id, item_type_id FROM intakes WHERE id = 2`,
	).Scan(&workspaceID, &portalChannel, &itemTypeID); err != nil {
		t.Fatalf("read portal intake: %v", err)
	}
	if workspaceID != 2 {
		t.Fatalf("portal intake workspace_id = %d, want 2 (from request type)", workspaceID)
	}
	if !portalChannel.Valid || portalChannel.Int64 != 10 {
		t.Fatalf("portal intake portal_channel_id = %v, want 10", portalChannel)
	}
	if itemTypeID != 5 {
		t.Fatalf("portal intake item_type_id = %d, want 5 (from request type)", itemTypeID)
	}

	if n := countFixtureRows(t, tdb, columnPresentQuery("intakes", "target_type")); n != 0 {
		t.Fatal("target_type column still present after migration")
	}
	if n := countFixtureRows(t, tdb, columnPresentQuery("intakes", "workspace_id")); n != 1 {
		t.Fatal("workspace_id column missing after migration")
	}

	// Idempotent: a second initialize must not re-run or fail.
	if err := tdb.Initialize(); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}
}
