//go:build test

package database_test

import (
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// The fixture starts from the 0.8.5 canonical checkpoint and stands in the
// 0.8.9 on-call shapes with direct SQL: the API cannot intentionally create
// a pre-upgrade on-call incident history anymore (WI-1535, WI-1536).

func seedLegacyOnCallIncidents(t *testing.T, tdb *testutils.TestDB) {
	t.Helper()

	datetimeType := "DATETIME"
	idColumn := "INTEGER PRIMARY KEY AUTOINCREMENT"
	if testutils.IsPostgres() {
		datetimeType = "TIMESTAMPTZ"
		idColumn = "SERIAL PRIMARY KEY"
	}

	// Post-0.8.5 upgrade state the incident migrations depend on.
	for _, statement := range []string{
		`INSERT INTO workspaces (id) VALUES (1)`,
		`ALTER TABLE items ADD COLUMN title TEXT`,
		`ALTER TABLE items ADD COLUMN updated_at ` + datetimeType,
		`INSERT INTO items (workspace_id, frac_index, title) VALUES (1, '0|a', 'Legacy item')`,
		`CREATE TABLE on_call_escalation_policies (
			id ` + idColumn + `,
			team_id INTEGER,
			name TEXT,
			description TEXT,
			repeat_count INTEGER DEFAULT 0,
			is_active BOOLEAN DEFAULT TRUE,
			created_by INTEGER,
			created_at ` + datetimeType + ` DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE on_call_escalation_rules (
			id ` + idColumn + `,
			policy_id INTEGER NOT NULL,
			step_order INTEGER DEFAULT 0,
			escalation_delay_minutes INTEGER DEFAULT 0,
			target_type TEXT,
			target_id INTEGER,
			created_at ` + datetimeType + ` DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE on_call_notification_rules (
			id ` + idColumn + `,
			escalation_rule_id INTEGER NOT NULL,
			notification_type TEXT,
			delay_minutes INTEGER DEFAULT 0,
			repeat_interval_minutes INTEGER,
			repeat_count INTEGER DEFAULT 1,
			created_at ` + datetimeType + ` DEFAULT CURRENT_TIMESTAMP
		)`,
		// The 0.8.9 on-call incident table exactly as teams.sql declared it.
		`CREATE TABLE on_call_incidents (
			id ` + idColumn + `,
			escalation_policy_id INTEGER NOT NULL,
			item_id INTEGER,
			status TEXT DEFAULT 'triggered',
			triggered_at ` + datetimeType + ` DEFAULT CURRENT_TIMESTAMP,
			acknowledged_at ` + datetimeType + `,
			acknowledged_by INTEGER,
			resolved_at ` + datetimeType + `,
			resolved_by INTEGER,
			current_escalation_step INTEGER DEFAULT 0,
			escalation_repeat_count INTEGER DEFAULT 0,
			created_at ` + datetimeType + ` DEFAULT CURRENT_TIMESTAMP
		)`,
		// The delayed-notification queue as 20260926 created it, before the
		// state-hardening migration adds the escalation-rule foreign key.
		`CREATE TABLE incident_notification_state (
			id ` + idColumn + `,
			incident_id INTEGER NOT NULL,
			escalation_rule_id INTEGER NOT NULL,
			notification_rule_id INTEGER NOT NULL,
			repeat_index INTEGER NOT NULL DEFAULT 0,
			next_notification_at ` + datetimeType + ` NOT NULL,
			created_at ` + datetimeType + ` NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at ` + datetimeType + ` NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	} {
		if _, err := tdb.Exec(statement); err != nil {
			t.Fatalf("seed legacy on-call schema with %q: %v", statement, err)
		}
	}

	if _, err := tdb.Exec(`INSERT INTO on_call_escalation_policies (id, team_id, name) VALUES (11, NULL, 'Legacy policy')`); err != nil {
		t.Fatalf("seed legacy policy: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO on_call_escalation_rules (id, policy_id, step_order, target_type, target_id) VALUES (21, 11, 0, 'user', 1)`); err != nil {
		t.Fatalf("seed legacy escalation rule: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO on_call_notification_rules (id, escalation_rule_id, notification_type) VALUES (31, 21, 'in_app')`); err != nil {
		t.Fatalf("seed legacy notification rule: %v", err)
	}

	// Legacy history: a resolved row, an older open acknowledged row, the
	// latest open triggered row (the survivor), and an orphaned row without
	// an item.
	legacyRows := []string{
		`INSERT INTO on_call_incidents (id, escalation_policy_id, item_id, status, triggered_at, resolved_at, current_escalation_step)
			VALUES (101, 11, 1, 'resolved', '2026-08-01 08:00:00', '2026-08-01 09:00:00', 1)`,
		`INSERT INTO on_call_incidents (id, escalation_policy_id, item_id, status, triggered_at, acknowledged_at, current_escalation_step)
			VALUES (102, 11, 1, 'acknowledged', '2026-08-02 08:00:00', '2026-08-02 08:05:00', 0)`,
		`INSERT INTO on_call_incidents (id, escalation_policy_id, item_id, status, triggered_at, current_escalation_step, escalation_repeat_count)
			VALUES (103, 11, 1, 'triggered', '2026-08-03 08:00:00', 1, 0)`,
		`INSERT INTO on_call_incidents (id, escalation_policy_id, item_id, status, triggered_at)
			VALUES (104, 11, NULL, 'resolved', '2026-08-04 08:00:00')`,
	}
	for _, statement := range legacyRows {
		if _, err := tdb.Exec(statement); err != nil {
			t.Fatalf("seed legacy incident row: %v", statement)
		}
	}
}

// stampAllExceptVersions stamps the full catalog except the given versions so
// exactly those migrations run against the fixture, in catalog order.
func stampAllExceptVersions(t *testing.T, tdb *testutils.TestDB, exceptVersions []string) {
	t.Helper()
	excepted := make(map[string]bool, len(exceptVersions))
	for _, version := range exceptVersions {
		excepted[version] = true
	}
	for _, migration := range database.Catalog {
		if excepted[migration.Version] {
			continue
		}
		if _, err := tdb.Exec(
			`INSERT INTO schema_migrations(version, name, checksum) VALUES (?, ?, '')`,
			migration.Version, migration.Name,
		); err != nil {
			t.Fatalf("stamp migration %s: %v", migration.Version, err)
		}
	}
}

// TestMigration_Incidents_PreservesOnCallHistory upgrades a 0.8.9-shaped
// database through the incident migrations and proves the destructive
// 20260925_incidents rewrite archives all legacy rows, carries the latest
// open incident per item into the new model, and repoints the item
// (WI-1535). It also proves the state-hardening migration drops the dead
// dedup_key column and enforces the escalation-rule foreign key (WI-1536).
func TestMigration_Incidents_PreservesOnCallHistory(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)
	seedCanonicalSchemaCheckpoint(t, tdb)
	seedMinimalWorkspaces(t, tdb)
	seedLegacyOnCallIncidents(t, tdb)
	createSchemaMigrationsTable(t, tdb)
	stampAllExceptVersions(t, tdb, []string{
		"20260925_incidents",
		"20261006_incident_state_hardening",
	})

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run incident migrations: %v", err)
	}

	// Every legacy row is archived before the source table is dropped.
	if got := countFixtureRows(t, tdb, `SELECT COUNT(*) FROM on_call_incidents_archive`); got != 4 {
		t.Fatalf("archived rows = %d, want 4", got)
	}
	var resolvedStatus string
	if err := tdb.QueryRow(`SELECT status FROM on_call_incidents_archive WHERE id = 101`).Scan(&resolvedStatus); err != nil {
		t.Fatalf("read archived resolved row: %v", err)
	}
	if resolvedStatus != "resolved" {
		t.Fatalf("archived row 101 status = %q, want resolved", resolvedStatus)
	}
	var orphanItemID *int
	if err := tdb.QueryRow(`SELECT item_id FROM on_call_incidents_archive WHERE id = 104`).Scan(&orphanItemID); err != nil {
		t.Fatalf("read archived orphan row: %v", err)
	}
	if orphanItemID != nil {
		t.Fatalf("archived row 104 item_id = %d, want nil", *orphanItemID)
	}

	// The old table is gone; live state holds one migrated incident.
	if tableExists(t, tdb, "on_call_incidents") {
		t.Fatal("on_call_incidents still exists after migration")
	}

	var incidentID, escalationStep int
	var status string
	var deadlinePresent int
	q := `SELECT id, status, escalation_step, CASE WHEN next_escalation_at IS NULL THEN 0 ELSE 1 END FROM incidents`
	if err := tdb.QueryRow(q).Scan(&incidentID, &status, &escalationStep, &deadlinePresent); err != nil {
		t.Fatalf("read migrated incident: %v", err)
	}
	if status != "triggered" {
		t.Fatalf("migrated incident status = %q, want triggered (latest open row survives)", status)
	}
	if escalationStep != 1 {
		t.Fatalf("migrated incident escalation_step = %d, want 1", escalationStep)
	}
	if deadlinePresent != 1 {
		t.Fatal("migrated triggered incident has no escalation deadline; paging would never resume")
	}

	// The item points at the migrated incident and dedup_key is gone.
	var itemIncidentID *int
	if err := tdb.QueryRow(`SELECT incident_id FROM items WHERE id = 1`).Scan(&itemIncidentID); err != nil {
		t.Fatalf("read item pointer: %v", err)
	}
	if itemIncidentID == nil || *itemIncidentID != incidentID {
		t.Fatalf("items.incident_id = %v, want %d", itemIncidentID, incidentID)
	}
	if got := countFixtureRows(t, tdb, columnPresentQuery("incidents", "dedup_key")); got != 0 {
		t.Fatal("incidents.dedup_key still present after state hardening")
	}

	// Scheduled incident notifications reference escalation rules.
	if _, err := tdb.Exec(`INSERT INTO incident_notification_state
		(incident_id, escalation_rule_id, notification_rule_id, repeat_index, next_notification_at)
		VALUES (?, 21, 31, 0, CURRENT_TIMESTAMP)`, incidentID); err != nil {
		t.Fatalf("insert state for existing rule: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO incident_notification_state
		(incident_id, escalation_rule_id, notification_rule_id, repeat_index, next_notification_at)
		VALUES (?, 999, 21, 1, CURRENT_TIMESTAMP)`, incidentID); err == nil {
		t.Fatal("insert state with unknown escalation rule succeeded; foreign key not enforced")
	}

	// The migration adds the escalation-rule foreign key under the same name
	// PostgreSQL derives for the inline fresh-schema constraint, so fresh and
	// upgraded databases stay diffable.
	if testutils.IsPostgres() {
		var constraintName string
		if err := tdb.QueryRow(`
			SELECT conname FROM pg_constraint
			WHERE conrelid = 'incident_notification_state'::regclass
				AND contype = 'f'
				AND pg_get_constraintdef(oid) LIKE '%escalation_rule_id%'`,
		).Scan(&constraintName); err != nil {
			t.Fatalf("read escalation-rule constraint name: %v", err)
		}
		if want := "incident_notification_state_escalation_rule_id_fkey"; constraintName != want {
			t.Fatalf("escalation-rule constraint name = %q, want the fresh-schema default %q", constraintName, want)
		}
	}

	// Re-initializing is stable and both migrations are stamped once.
	if err := tdb.Initialize(); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}
	for _, version := range []string{"20260925_incidents", "20261006_incident_state_hardening"} {
		var stamps int
		if err := tdb.QueryRow(
			`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version,
		).Scan(&stamps); err != nil || stamps != 1 {
			t.Fatalf("migration %s stamps = %d, %v; want 1", version, stamps, err)
		}
	}
}

// tableExists reports whether a table is present in the fixture database.
func tableExists(t *testing.T, tdb *testutils.TestDB, table string) bool {
	t.Helper()
	query := `SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = ?`
	if !testutils.IsPostgres() {
		query = `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`
	}
	var count int
	if err := tdb.QueryRow(query, table).Scan(&count); err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	return count > 0
}
