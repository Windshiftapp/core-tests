//go:build test

package database_test

import (
	"testing"

	"windshift/internal/testutils"
)

// TestSchema_UsersAgentFlagsImmutable pins the WI-87 invariant that
// service-user classification is stable: once a row is created as a
// centralized service user (is_agent=true, agent_owner_user_id=NULL),
// nothing can flip is_agent off (converting it to a human) or stamp an
// owner (converting it to an owned agent). The SQLite schema enforces
// this via two BEFORE UPDATE triggers (users_is_agent_immutable +
// users_agent_owner_immutable in internal/database/schema/users.sql);
// the Postgres schema does the equivalent via a function fired by
// users_is_agent_immutable_trigger in
// internal/database/schema/base_tables_postgres.sql.
// AgentSecurityHandler.AddAllowlist relies on the invariant — once the
// allowlist accepts a service user, that decision can't be silently
// undermined by a later UPDATE on the users row.
func TestSchema_UsersAgentFlagsImmutable(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.DB

	if _, err := db.Exec(`INSERT INTO users(email, username, first_name, last_name, is_agent) VALUES ('admin-immut@example.com','admin-immut','A','',?)`, false); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users(email, username, first_name, last_name, is_agent) VALUES ('svc-immut@agents.local','svc-immut','S','',?)`, true); err != nil {
		t.Fatalf("seed service user: %v", err)
	}
	var adminID, svcID int
	if err := db.QueryRow(`SELECT id FROM users WHERE username='admin-immut'`).Scan(&adminID); err != nil {
		t.Fatalf("read admin: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM users WHERE username='svc-immut'`).Scan(&svcID); err != nil {
		t.Fatalf("read svc: %v", err)
	}

	// Service user → human user (is_agent flip) must be rejected by the
	// schema-level trigger.
	if _, err := db.Exec(`UPDATE users SET is_agent = ? WHERE id = ?`, false, svcID); err == nil {
		t.Fatal("expected UPDATE of is_agent to be rejected, got nil")
	}

	// Service user → owned agent (stamp owner) must be rejected.
	if _, err := db.Exec(`UPDATE users SET agent_owner_user_id = ? WHERE id = ?`, adminID, svcID); err == nil {
		t.Fatal("expected UPDATE of agent_owner_user_id to be rejected, got nil")
	}

	// Sanity: the row is unchanged.
	var (
		isAgent     bool
		ownerExists bool
	)
	if err := db.QueryRow(
		`SELECT COALESCE(is_agent, false), agent_owner_user_id IS NOT NULL FROM users WHERE id = ?`,
		svcID,
	).Scan(&isAgent, &ownerExists); err != nil {
		t.Fatalf("re-read svc: %v", err)
	}
	if !isAgent || ownerExists {
		t.Errorf("service user classification drifted: is_agent=%v ownerExists=%v", isAgent, ownerExists)
	}
}
