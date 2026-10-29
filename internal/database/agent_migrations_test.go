package database

import (
	"fmt"
	"testing"
)

// TestMigration_AgentRuns_SchemaSanity exercises the 20260529_agent_runs
// migration's table shape end-to-end on a fresh SQLite install: a workspace
// can have an agent_run, that run can have events, events cascade away with
// the run, and the status CHECK rejects bogus values.
func TestMigration_AgentRuns_SchemaSanity(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/agent_runs.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize fresh db: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (1, 'ws', 'WS', TRUE)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}

	res, err := db.Exec(`INSERT INTO agent_runs(workspace_id, item_id) VALUES (1, NULL)`)
	if err != nil {
		t.Fatalf("insert agent_run: %v", err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}

	if _, err := db.Exec(`
		UPDATE agent_runs
		SET job_kind = 'standard_agent',
		    acting_user_id = 31,
		    root_initiator_user_id = 41,
		    immediate_trigger_user_id = 42,
		    parent_run_id = 99,
		    chain_depth = 3,
		    session_id = 'session-1',
		    profile_version = 7,
		    profile_snapshot_json = '{"profile_version":7}',
		    grants_json = '{"tools":["get_item"]}'
		WHERE id = ?
	`, runID); err != nil {
		t.Fatalf("persist Standard-agent run snapshot: %v", err)
	}
	var jobKind, snapshot string
	var actingID, rootID, immediateID, parentID, depth, profileVersion int
	if err := db.QueryRow(`
		SELECT job_kind, acting_user_id, root_initiator_user_id,
		       immediate_trigger_user_id, parent_run_id, chain_depth,
		       profile_version, profile_snapshot_json
		FROM agent_runs WHERE id = ?
	`, runID).Scan(&jobKind, &actingID, &rootID, &immediateID, &parentID, &depth, &profileVersion, &snapshot); err != nil {
		t.Fatalf("read Standard-agent run snapshot: %v", err)
	}
	if jobKind != "standard_agent" || actingID != 31 || rootID != 41 ||
		immediateID != 42 || parentID != 99 || depth != 3 ||
		profileVersion != 7 || snapshot != `{"profile_version":7}` {
		t.Fatalf("Standard-agent snapshot mismatch: kind=%q acting=%d root=%d immediate=%d parent=%d depth=%d version=%d snapshot=%q",
			jobKind, actingID, rootID, immediateID, parentID, depth, profileVersion, snapshot)
	}

	if _, err := db.Exec(
		`INSERT INTO agent_run_events(run_id, type, payload_json) VALUES (?, 'lifecycle', '{"phase":"queued"}')`,
		runID,
	); err != nil {
		t.Fatalf("insert agent_run_event: %v", err)
	}
	if n := countRows(t, db, fmt.Sprintf("SELECT COUNT(*) FROM agent_run_events WHERE run_id=%d", runID)); n != 1 {
		t.Fatalf("expected 1 event for run, got %d", n)
	}

	// Status CHECK rejects bogus values.
	if _, err := db.Exec(
		`INSERT INTO agent_runs(workspace_id, status) VALUES (1, 'in_orbit')`,
	); err == nil {
		t.Fatal("expected CHECK violation on bogus status, got nil")
	}

	// ON DELETE CASCADE cleans up events.
	if _, err := db.Exec(`DELETE FROM agent_runs WHERE id = ?`, runID); err != nil {
		t.Fatalf("delete agent_run: %v", err)
	}
	if n := countRows(t, db, fmt.Sprintf("SELECT COUNT(*) FROM agent_run_events WHERE run_id=%d", runID)); n != 0 {
		t.Fatalf("expected cascade delete of events, got %d remaining", n)
	}
}

func TestMigration_AgentConversations_SchemaSanity(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/agent_conversations.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (1, 'ws', 'WS', TRUE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO users(id, email, username, first_name, last_name, is_agent)
		VALUES (10, 'human@example.test', 'human', 'Human', '', false),
		       (11, 'agent@example.test', 'agent', 'Agent', '', true)
	`); err != nil {
		t.Fatal(err)
	}
	var generalID int
	if err := db.QueryRow(`
		INSERT INTO agent_sessions(session_type, owner_user_id, title)
		VALUES ('general', 10, 'General') RETURNING id
	`).Scan(&generalID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_session_participants(session_id, user_id, participant_role)
		VALUES (?, 10, 'human')
	`, generalID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_messages(session_id, role, author_user_id, content)
		VALUES (?, 'user', 10, 'exact body')
	`, generalID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_runs(workspace_id, status, job_kind, session_id)
		VALUES (1, 'queued', 'general_agent', ?)
	`, generalID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_runs(workspace_id, status, job_kind, session_id)
		VALUES (1, 'running', 'general_agent', ?)
	`, generalID); err == nil {
		t.Fatal("expected one active run per agent session")
	}
	if _, err := db.Exec(`
		UPDATE agent_runs SET status = 'succeeded' WHERE session_id = ?
	`, generalID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_runs(workspace_id, status, job_kind, session_id)
		VALUES (1, 'queued', 'general_agent', ?)
	`, generalID); err != nil {
		t.Fatalf("expected a new turn after the prior run completed: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_sessions(session_type, owner_user_id, title)
		VALUES ('general', 10, 'duplicate')
	`); err == nil {
		t.Fatal("expected one General session per owner")
	}
	if _, err := db.Exec(`
		INSERT INTO agent_sessions(session_type, owner_user_id, title)
		VALUES ('standard', 10, 'missing workspace/profile')
	`); err == nil {
		t.Fatal("expected Standard scope CHECK violation")
	}
	if _, err := db.Exec(`DELETE FROM agent_sessions WHERE id = ?`, generalID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, "SELECT COUNT(*) FROM agent_messages"); n != 0 {
		t.Fatalf("session delete did not cascade messages: %d", n)
	}
}

// TestMigration_AgentSecurity_SchemaSanity exercises the
// 20260529_agent_security_allowlist migration end-to-end on a fresh
// SQLite install: the seeded system_settings row is present, the
// (user_id, workspace_id) uniqueness constraint correctly treats
// NULL as distinct from any concrete workspace, and user cascade
// deletion drops the allowlist row.
func TestMigration_AgentSecurity_SchemaSanity(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/agent_security.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize fresh db: %v", err)
	}

	var flagValue string
	if err := db.QueryRow(`SELECT value FROM system_settings WHERE key='agents.allow_centralized_service_users'`).Scan(&flagValue); err != nil {
		t.Fatalf("read security flag: %v", err)
	}
	if flagValue != "false" {
		t.Errorf("flag default: want false, got %q", flagValue)
	}

	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (1, 'WS', 'WS', TRUE)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users(email, username, first_name, last_name, is_agent) VALUES ('s@example.com','s','S','',TRUE)`); err != nil {
		t.Fatalf("seed service user: %v", err)
	}
	var serviceUserID int
	if err := db.QueryRow(`SELECT id FROM users WHERE username='s'`).Scan(&serviceUserID); err != nil {
		t.Fatalf("read service user id: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users(email, username, first_name, last_name) VALUES ('admin@example.com','admin','A','')`); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	var adminID int
	if err := db.QueryRow(`SELECT id FROM users WHERE username='admin'`).Scan(&adminID); err != nil {
		t.Fatalf("read admin id: %v", err)
	}

	// NULL workspace_id (= any-workspace) and a concrete workspace_id
	// must coexist for the same user — they're distinct grants.
	if _, err := db.Exec(
		`INSERT INTO global_agent_acting_user_allowlist(user_id, workspace_id, reason, created_by_user_id) VALUES (?, NULL, 'broad grant', ?)`,
		serviceUserID, adminID,
	); err != nil {
		t.Fatalf("insert any-workspace grant: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO global_agent_acting_user_allowlist(user_id, workspace_id, reason, created_by_user_id) VALUES (?, 1, 'workspace-1 grant', ?)`,
		serviceUserID, adminID,
	); err != nil {
		t.Fatalf("insert workspace-scoped grant: %v", err)
	}

	// Re-inserting the same (user_id, workspace_id) pair must fail.
	if _, err := db.Exec(
		`INSERT INTO global_agent_acting_user_allowlist(user_id, workspace_id, reason, created_by_user_id) VALUES (?, 1, 'dup', ?)`,
		serviceUserID, adminID,
	); err == nil {
		t.Fatal("expected unique-index violation on duplicate (user_id, workspace_id), got nil")
	}

	// Cascade on user delete drops the rows.
	if _, err := db.Exec(`DELETE FROM users WHERE id = ?`, serviceUserID); err != nil {
		t.Fatalf("delete service user: %v", err)
	}
	if n := countRows(t, db, fmt.Sprintf("SELECT COUNT(*) FROM global_agent_acting_user_allowlist WHERE user_id=%d", serviceUserID)); n != 0 {
		t.Fatalf("cascade delete failed: %d rows still reference user", n)
	}
}

// TestMigration_WorkspaceAgentBindings_SchemaSanity exercises the
// 20260529_workspace_agent_bindings migration: the kind CHECK rejects
// bogus values, the (workspace_id, acting_user_id) unique index forbids
// duplicates, and the workspace + user cascades drop bindings.
func TestMigration_WorkspaceAgentBindings_SchemaSanity(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/bindings.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("init db: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (1, 'WS', 'WS', TRUE)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users(email, username, first_name, last_name, is_agent) VALUES ('alice@example.com','alice','A','',FALSE)`); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users(email, username, first_name, last_name, is_agent) VALUES ('agent@agents.local','agent','Ag','Ent',TRUE)`); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	var adminID, agentID int
	if err := db.QueryRow(`SELECT id FROM users WHERE username='alice'`).Scan(&adminID); err != nil {
		t.Fatalf("read admin id: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM users WHERE username='agent'`).Scan(&agentID); err != nil {
		t.Fatalf("read agent id: %v", err)
	}

	// Insert a valid binding.
	if _, err := db.Exec(
		`INSERT INTO workspace_agent_bindings(workspace_id, acting_user_id, acting_user_kind, created_by_user_id) VALUES (?, ?, 'agent', ?)`,
		1, agentID, adminID,
	); err != nil {
		t.Fatalf("insert binding: %v", err)
	}

	var profileType, lifecycle, identityClass string
	var profileVersion int
	if err := db.QueryRow(`
		SELECT profile_type, lifecycle, profile_version, identity_class
		FROM workspace_agent_bindings
		WHERE acting_user_id = ?
	`, agentID).Scan(&profileType, &lifecycle, &profileVersion, &identityClass); err != nil {
		t.Fatalf("read Agent Studio profile defaults: %v", err)
	}
	if profileType != "legacy" || lifecycle != "ready" || profileVersion != 1 || identityClass != "centralized_service" {
		t.Fatalf(
			"profile defaults = type:%q lifecycle:%q version:%d identity:%q",
			profileType, lifecycle, profileVersion, identityClass,
		)
	}

	if _, err := db.Exec(`
		UPDATE workspace_agent_bindings SET profile_type = 'invalid'
		WHERE acting_user_id = ?
	`, agentID); err == nil {
		t.Fatal("expected CHECK violation on invalid profile type")
	}

	// CHECK constraint rejects bogus kind.
	if _, err := db.Exec(
		`INSERT INTO workspace_agent_bindings(workspace_id, acting_user_id, acting_user_kind, created_by_user_id) VALUES (?, ?, 'wat', ?)`,
		1, adminID, adminID,
	); err == nil {
		t.Fatal("expected CHECK violation on bogus kind, got nil")
	}

	// Unique index rejects duplicate (workspace, acting_user).
	if _, err := db.Exec(
		`INSERT INTO workspace_agent_bindings(workspace_id, acting_user_id, acting_user_kind, created_by_user_id) VALUES (?, ?, 'agent', ?)`,
		1, agentID, adminID,
	); err == nil {
		t.Fatal("expected uniqueness violation on duplicate (workspace, acting_user), got nil")
	}

	// Cascade on user delete drops the binding.
	if _, err := db.Exec(`DELETE FROM users WHERE id = ?`, agentID); err != nil {
		t.Fatalf("delete agent user: %v", err)
	}
	if n := countRows(t, db, fmt.Sprintf("SELECT COUNT(*) FROM workspace_agent_bindings WHERE acting_user_id=%d", agentID)); n != 0 {
		t.Fatalf("cascade delete failed: %d rows remain", n)
	}
}
