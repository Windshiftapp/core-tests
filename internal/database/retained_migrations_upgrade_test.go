//go:build test

package database_test

import (
	"database/sql"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// The fixtures below start from a valid 0.8.5 database (canonical checkpoint
// state via seedCanonicalSchemaCheckpoint) that lacks one retained
// migration's effect. Direct SQL creates the pre-migration shapes because
// the API cannot intentionally produce them anymore.

func TestMigration_WorkflowTransitionsFromAll_AddsColumn(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)
	seedCanonicalSchemaCheckpoint(t, tdb)
	seedMinimalWorkspaces(t, tdb)

	idColumn := "INTEGER PRIMARY KEY AUTOINCREMENT"
	if testutils.IsPostgres() {
		idColumn = "SERIAL PRIMARY KEY"
	}
	if _, err := tdb.Exec(`CREATE TABLE workflow_transitions (
		id ` + idColumn + `,
		name TEXT NOT NULL,
		from_status_id INTEGER,
		to_status_id INTEGER
	)`); err != nil {
		t.Fatalf("seed legacy workflow_transitions: %v", err)
	}
	if _, err := tdb.Exec(
		`INSERT INTO workflow_transitions (name) VALUES ('reopen')`,
	); err != nil {
		t.Fatalf("seed transition row: %v", err)
	}

	createSchemaMigrationsTable(t, tdb)
	stampAllExcept(t, tdb, "20260814_workflow_transitions_from_all", "")

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run from_all_statuses migration: %v", err)
	}

	fromAllCount := countFixtureRows(t, tdb, columnPresentQuery("workflow_transitions", "from_all_statuses"))
	if fromAllCount != 1 {
		t.Fatalf("from_all_statuses column absent after migration (count=%d)", fromAllCount)
	}

	var fromAll bool
	if err := tdb.QueryRow(`SELECT from_all_statuses FROM workflow_transitions WHERE name='reopen'`).Scan(&fromAll); err != nil {
		t.Fatalf("read seeded transition: %v", err)
	}
	if fromAll {
		t.Fatal("existing rows must default to from_all_statuses = false")
	}

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}
	var stamps int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version='20260814_workflow_transitions_from_all'`).Scan(&stamps); err != nil || stamps != 1 {
		t.Fatalf("from_all_statuses migration stamps = %d, %v; want 1", stamps, err)
	}
}

func TestMigration_CustomerOrganisationsBuiltinKey_AddsPlaceholder(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)
	seedCanonicalSchemaCheckpoint(t, tdb)
	seedMinimalWorkspaces(t, tdb)

	idColumn := "INTEGER PRIMARY KEY AUTOINCREMENT"
	if testutils.IsPostgres() {
		idColumn = "SERIAL PRIMARY KEY"
	}
	if _, err := tdb.Exec(`CREATE TABLE customer_organisations (
		id ` + idColumn + `,
		name TEXT NOT NULL,
		email TEXT,
		description TEXT,
		active BOOLEAN DEFAULT true,
		avatar_url TEXT,
		custom_field_values TEXT,
		settings TEXT NOT NULL DEFAULT '{}',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("seed legacy customer_organisations: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO customer_organisations (name, active) VALUES ('Acme', true)`); err != nil {
		t.Fatalf("seed existing customer: %v", err)
	}

	createSchemaMigrationsTable(t, tdb)
	stampAllExcept(t, tdb, "20261030_customer_organisations_builtin_key", "")

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run builtin_key migration: %v", err)
	}

	if got := countFixtureRows(t, tdb, columnPresentQuery("customer_organisations", "builtin_key")); got != 1 {
		t.Fatalf("builtin_key column absent after migration (count=%d)", got)
	}
	if got := countFixtureRows(t, tdb, `SELECT COUNT(*) FROM customer_organisations WHERE builtin_key = 'no_customer'`); got != 1 {
		t.Fatalf("placeholder rows = %d, want 1", got)
	}
	if got := countFixtureRows(t, tdb, `SELECT COUNT(*) FROM customer_organisations WHERE name = 'Acme' AND builtin_key IS NULL`); got != 1 {
		t.Fatalf("existing customer rows = %d, want 1", got)
	}

	// Idempotent: re-running the catalog must not add a second placeholder.
	if err := tdb.Initialize(); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}
	if got := countFixtureRows(t, tdb, `SELECT COUNT(*) FROM customer_organisations WHERE builtin_key = 'no_customer'`); got != 1 {
		t.Fatalf("placeholder rows after repeat = %d, want 1", got)
	}
}

func TestMigration_PortalVoteUniqueness_KeepsEarliestSurvivor(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)
	seedCanonicalSchemaCheckpoint(t, tdb)
	seedMinimalWorkspaces(t, tdb)

	idColumn := "INTEGER PRIMARY KEY AUTOINCREMENT"
	createdAtType := "DATETIME"
	if testutils.IsPostgres() {
		idColumn = "SERIAL PRIMARY KEY"
		createdAtType = "TIMESTAMPTZ"
	}
	if _, err := tdb.Exec(`CREATE TABLE approval_decisions (
		id ` + idColumn + `,
		approval_request_id INTEGER NOT NULL,
		approval_step_instance_id INTEGER,
		actor_user_id INTEGER,
		actor_portal_customer_id INTEGER,
		decision TEXT NOT NULL,
		created_at ` + createdAtType + `
	)`); err != nil {
		t.Fatalf("seed legacy approval_decisions: %v", err)
	}

	seed := []struct {
		id        int
		step      int
		customer  int
		decision  string
		createdAt string
	}{
		// Later (created_at, id) duplicate loses.
		{1, 10, 20, "approve", "2026-08-01 10:00:00"},
		{2, 10, 20, "approve", "2026-08-01 09:00:00"},
		// Equal created_at: lower id survives.
		{3, 11, 21, "reject", "2026-08-02 12:00:00"},
		{4, 11, 21, "reject", "2026-08-02 12:00:00"},
		// Different customer: untouched.
		{5, 10, 22, "approve", "2026-08-01 08:00:00"},
	}
	for _, row := range seed {
		if _, err := tdb.Exec(
			`INSERT INTO approval_decisions (id, approval_request_id, approval_step_instance_id, actor_portal_customer_id, decision, created_at)
			 VALUES (?, 1, ?, ?, ?, ?)`,
			row.id, row.step, row.customer, row.decision, row.createdAt,
		); err != nil {
			t.Fatalf("seed decision %d: %v", row.id, err)
		}
	}
	if testutils.IsPostgres() {
		// Keep later id-less inserts off the explicitly seeded primary keys.
		if _, err := tdb.Exec(`SELECT setval(pg_get_serial_sequence('approval_decisions', 'id'), (SELECT MAX(id) FROM approval_decisions))`); err != nil {
			t.Fatalf("advance approval_decisions sequence: %v", err)
		}
	}

	createSchemaMigrationsTable(t, tdb)
	stampAllExcept(t, tdb, "20260816_portal_approval_vote_uniqueness", "")

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run portal vote migration: %v", err)
	}

	assertSurvivors := func() {
		t.Helper()
		for _, c := range []struct{ step, customer, wantID int }{
			{10, 20, 2},
			{11, 21, 3},
			{10, 22, 5},
		} {
			var gotID int
			if err := tdb.QueryRow(
				`SELECT id FROM approval_decisions WHERE approval_step_instance_id=? AND actor_portal_customer_id=?`,
				c.step, c.customer,
			).Scan(&gotID); err != nil {
				t.Fatalf("read survivor for step=%d customer=%d: %v", c.step, c.customer, err)
			}
			if gotID != c.wantID {
				t.Fatalf("survivor for step=%d customer=%d = %d, want earliest (created_at, id) = %d", c.step, c.customer, gotID, c.wantID)
			}
		}
		var total int
		if err := tdb.QueryRow(`SELECT COUNT(*) FROM approval_decisions`).Scan(&total); err != nil || total != 3 {
			t.Fatalf("rows after dedup = %d, %v; want 3", total, err)
		}
	}
	assertSurvivors()

	if _, err := tdb.Exec(
		`INSERT INTO approval_decisions (approval_request_id, approval_step_instance_id, actor_portal_customer_id, decision, created_at)
		 VALUES (1, 10, 20, 'reject', '2026-08-03T10:00:00Z')`,
	); err == nil {
		t.Fatal("duplicate portal vote passed the migrated unique index")
	}

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}
	assertSurvivors()
}

func TestSchema_FreshInstallWorkspaceIsTemplateIsNullable(t *testing.T) {
	tdb := testutils.CreateTestDB(t, false)

	if testutils.IsPostgres() {
		var nullable string
		if err := tdb.QueryRow(`SELECT is_nullable FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='workspaces' AND column_name='is_template'`).Scan(&nullable); err != nil || nullable != "YES" {
			t.Fatalf("fresh is_template is_nullable = %q, %v; want YES", nullable, err)
		}
		return
	}
	var notNull int
	if err := tdb.QueryRow(`SELECT "notnull" FROM pragma_table_info('workspaces') WHERE name='is_template'`).Scan(&notNull); err != nil || notNull != 0 {
		t.Fatalf("fresh is_template notnull = %d, %v; want 0", notNull, err)
	}
}

func TestMigration_RetiredMigrationRowsAreIgnored(t *testing.T) {
	tdb := testutils.CreateTestDB(t, false)

	retired := []string{
		"cutover_items_milestone_backfill",
		"cutover_sqlite_datetime_format",
		"20260807_global_rank_checkpoint",
		"20260814_global_item_labels",
		"20260727_milestone_release_attempts",
	}
	for _, version := range retired {
		if _, err := tdb.Exec(
			`INSERT INTO schema_migrations(version, name, checksum) VALUES (?, 'retired', 'deadbeef')`,
			version,
		); err != nil {
			t.Fatalf("seed retired row %s: %v", version, err)
		}
	}

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("initialize with retired rows: %v", err)
	}

	for _, version := range retired {
		var checksum string
		if err := tdb.QueryRow(`SELECT checksum FROM schema_migrations WHERE version=?`, version).Scan(&checksum); err != nil || checksum != "deadbeef" {
			t.Fatalf("retired row %s checksum = %q, %v; want preserved", version, checksum, err)
		}
	}
}

func TestMigration_CFVCustomFieldCleanupRetries_AddsSchedulingColumns(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)
	seedCanonicalSchemaCheckpoint(t, tdb)
	seedMinimalWorkspaces(t, tdb)

	idColumn := "INTEGER PRIMARY KEY AUTOINCREMENT"
	if testutils.IsPostgres() {
		idColumn = "SERIAL PRIMARY KEY"
	}
	if _, err := tdb.Exec(`CREATE TABLE pending_custom_field_cleanups (id ` + idColumn + `)`); err != nil {
		t.Fatalf("seed legacy cleanup queue: %v", err)
	}
	createSchemaMigrationsTable(t, tdb)
	stampAllExcept(t, tdb, "20260823_cfv_cleanup_retries", "")

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run cleanup retry migration: %v", err)
	}
	for _, column := range []string{"attempt_count", "next_attempt_at"} {
		if count := countFixtureRows(t, tdb, columnPresentQuery("pending_custom_field_cleanups", column)); count != 1 {
			t.Fatalf("%s column absent after migration", column)
		}
	}
}

func TestMigration_JiraConnectionSoftDelete_AddsColumn(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)
	seedCanonicalSchemaCheckpoint(t, tdb)
	seedMinimalWorkspaces(t, tdb)

	if _, err := tdb.Exec(`CREATE TABLE jira_import_connections (
		id TEXT PRIMARY KEY,
		instance_url TEXT NOT NULL,
		email TEXT NOT NULL,
		encrypted_credentials TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("seed legacy jira_import_connections: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO jira_import_connections (id, instance_url, email, encrypted_credentials)
		VALUES ('legacy', 'https://example.invalid', 'a@example.invalid', 'cipher')
	`); err != nil {
		t.Fatalf("seed legacy connection row: %v", err)
	}

	createSchemaMigrationsTable(t, tdb)
	stampAllExcept(t, tdb, "20261108_jira_connection_soft_delete", "")

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run Jira connection soft-delete migration: %v", err)
	}
	if count := countFixtureRows(t, tdb, columnPresentQuery("jira_import_connections", "deleted_at")); count != 1 {
		t.Fatal("deleted_at column absent after migration")
	}
	var deletedAt sql.NullTime
	if err := tdb.QueryRow(`SELECT deleted_at FROM jira_import_connections WHERE id = 'legacy'`).Scan(&deletedAt); err != nil {
		t.Fatalf("load migrated connection row: %v", err)
	}
	if deletedAt.Valid {
		t.Fatalf("existing connection deleted_at = %v, want NULL", deletedAt.Time)
	}
}

func TestMigration_JiraImportJobHiddenAt_AddsColumn(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)
	seedCanonicalSchemaCheckpoint(t, tdb)
	seedMinimalWorkspaces(t, tdb)

	if _, err := tdb.Exec(`CREATE TABLE jira_import_jobs (
		id TEXT PRIMARY KEY,
		status TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("seed legacy jira_import_jobs: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO jira_import_jobs (id, status) VALUES ('legacy', 'completed')`); err != nil {
		t.Fatalf("seed legacy job row: %v", err)
	}

	createSchemaMigrationsTable(t, tdb)
	stampAllExcept(t, tdb, "20261109_jira_import_job_hidden_at", "")

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run Jira import job hidden_at migration: %v", err)
	}
	if count := countFixtureRows(t, tdb, columnPresentQuery("jira_import_jobs", "hidden_at")); count != 1 {
		t.Fatal("hidden_at column absent after migration")
	}
	var hiddenAt sql.NullTime
	if err := tdb.QueryRow(`SELECT hidden_at FROM jira_import_jobs WHERE id = 'legacy'`).Scan(&hiddenAt); err != nil {
		t.Fatalf("load migrated job row: %v", err)
	}
	if hiddenAt.Valid {
		t.Fatalf("existing job hidden_at = %v, want NULL", hiddenAt.Time)
	}
}

// TestCompactCatalogContainsExactlyRetainedMigrations pins the squashed
// catalog: the fresh-install baseline marker plus the retained post-0.8.5
// upgrades, in catalog order.
func TestCompactCatalogContainsExactlyRetainedMigrations(t *testing.T) {
	want := []string{
		"0000_baseline",
		"20260924_generic_import_jobs",
		"20260814_workflow_transitions_from_all",
		"20260816_portal_approval_vote_uniqueness",
		"20260815_workspaces_is_template",
		"20260823_cfv_cleanup_retries",
		"20260824_agent_skill_page_snapshots",
		"20260826_board_completed_item_retention",
		"20260827_notification_provenance",
		"20260827_domain_event_engine",
		"20260827_durable_action_consumer",
		"20260827_durable_asset_action_consumer",
		"20260827_scm_connection_health",
		"20260829_builtin_translation_keys",
		"20260831_gitlab_release_metadata",
		"20260831_scm_webhook_ingress",
		"20260831_object_translations",
		"20260831_sso_email_verified_attribute_mapping",
		"20260829_zammad_integration",
		"20260830_zammad_oauth_connections",
		"20260830_zammad_oauth_generation",
		"20260830_zammad_ticket_link_metadata",
		"20260830_zammad_ticket_link_completion_postgres",
		"20260831_zammad_connection_config_revision",
		"20260831_zammad_ticket_sync_lock_owner",
		"20260831_zammad_ticket_link_item_restrict",
		"20260831_zammad_persisted_group_catalog",
		"20260901_zammad_canonical_workspace_scope",
		"20260901_daily_briefing_workspace_provenance",
		"20260905_asset_import_leases",
		"20260905_asset_import_upload_ownership",
		"20260905_notification_reference_provenance",
		"20260911_users_offboarded_at",
		"20260911_users_scim_deleted_at",
		"20260914_users_erased_at",
		"20260914_user_erasure_records",
		"20260905_notification_email_claims",
		"20260905_notification_delivery_keys",
		"20260911_theme_logo_url",
		"20260911_bdd_test_case_format",
		"20260918_workspaces_personal_owner_uniqueness",
		"20260918_kb_events",
		"20260923_email_intake_rate_limit",
		"20260923_email_reply_outbox_discarded_at",
		"20260924_theme_dark_logo_url",
		"20260924_items_merged_into",
		"20260924_item_import_rows",
		"20260925_items_team",
		"20260925_incidents",
		"20260926_incident_notification_state",
		"20260927_asset_set_portal_access",
		"20260928_sla_engine",
		"20260929_sla_warning_thresholds",
		"20260930_sla_cycle_source_id",
		"20260932_sla_hot_indexes",
		"20261001_action_trigger_marks",
		"20261001_canned_responses",
		"20261001_board_view_settings",
		"20261002_jsonb_hygiene",
		"20261003_item_history_portal_actors",
		"20261003_attachments_portal_uploader",
		"20261004_import_jobs_scope_fk",
		"20261005_portal_customers_erased_at",
		"20261005_customer_erasure_records",
		"20261006_portal_customers_deactivated_at",
		"20261006_portal_customers_created_via",
		"20261006_item_import_rows_lookup_indexes",
		"20261006_email_tracking_sender_indexes",
		"20261006_incident_state_hardening",
		"20261006_email_outbox_lease_owner",
		"20261002_view_settings_backfill_tools",
		"20261007_support_queues",
		"20261008_item_participants",
		"20261009_email_reply_outbox_per_recipient",
		"20261010_portal_org_sharing",
		"20261011_item_support_events",
		"20261002_zammad_ticket_change_history",
		"20260931_item_history_source",
		"20260931_item_history_agent_run",
		"20260931_llm_usage_calls",
		"20261012_transactional_email_copy",
		"20261013_portal_reply_markdown",
		"20261014_notification_digest_links",
		"20261015_request_types_kind",
		"20261016_email_outbox_reply_to",
		"20261018_intake_split",
		"20261026_intake_workspace_portal",
		"20261027_intake_send_confirmation",
		"20261019_email_tracking_completed_at",
		"20261020_email_tracking_completed_backfill",
		"20261021_collections_private",
		"20261022_request_types_workspace_pinned",
		"20261023_asset_reports_workspace_pinned",
		"20261024_attachments_internal_visibility",
		"20261025_jira_import_job_leases",
		"20261028_jira_import_job_lease_token",
		"20261029_mention_email_outbox",
		"20261030_customer_organisations_builtin_key",
		"20261031_email_tracking_skipped_reason",
		"20261101_email_lookup_indexes",
		"20261102_notification_digest_recipient_index",
		"20261103_email_confirmation_outbox",
		"20261104_sla_single_goal_target",
		"20261105_comment_reactions",
		"20261106_collections_public_milestone_swimlanes",
		"20261107_custom_widgets",
		"20261108_jira_connection_soft_delete",
		"20261109_jira_import_job_hidden_at",
		"20261110_intake_import_existing_mail",
		"20261111_queues_collection_scope",
		"20261112_audit_logs_source",
		"20261113_item_due_reminders",
		"20261114_user_due_date_reminder_prefs",
		"20261115_remove_personal_labels",
	}
	if len(database.Catalog) != len(want) {
		t.Fatalf("catalog length = %d, want %d", len(database.Catalog), len(want))
	}
	for i, version := range want {
		if database.Catalog[i].Version != version {
			t.Fatalf("catalog[%d] = %q, want %q", i, database.Catalog[i].Version, version)
		}
	}
}

// seedMinimalWorkspaces adds the fourth core probe table in its most
// minimal shape; the retained migrations under test never touch it.
func seedMinimalWorkspaces(t *testing.T, tdb *testutils.TestDB) {
	t.Helper()
	idColumn := "INTEGER PRIMARY KEY"
	if testutils.IsPostgres() {
		idColumn = "SERIAL PRIMARY KEY"
	}
	if _, err := tdb.Exec("CREATE TABLE workspaces (id " + idColumn + ")"); err != nil {
		t.Fatalf("seed minimal workspaces: %v", err)
	}
}

func columnPresentQuery(table, column string) string {
	if testutils.IsPostgres() {
		return "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='" + table + "' AND column_name='" + column + "'"
	}
	return "SELECT COUNT(*) FROM pragma_table_info('" + table + "') WHERE name='" + column + "'"
}

func countFixtureRows(t *testing.T, tdb *testutils.TestDB, query string) int {
	t.Helper()
	var n int
	if err := tdb.QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}
