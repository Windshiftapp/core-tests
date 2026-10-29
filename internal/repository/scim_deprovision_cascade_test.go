//go:build test

package repository

import (
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// WI-1309: the SCIM security cascade (owned-agent deactivation + API-token
// revocation) must commit atomically with the resource write it belongs to,
// and PATCH must be all-or-none (RFC 7644 §3.5.2).

func newSCIMRepoForCascade(t *testing.T) (*SCIMRepository, *testutils.TestDB) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	return NewSCIMRepository(tdb.GetDatabase()), tdb
}

func insertCascadeUser(t *testing.T, db database.Database, email string, scimManaged bool) int {
	t.Helper()
	var id int
	err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active, scim_managed)
		VALUES (?, ?, 'Cascade', 'Owner', true, ?) RETURNING id
	`, email, email[:len(email)-len("@example.test")], scimManaged).Scan(&id)
	if err != nil {
		t.Fatalf("insert user %s: %v", email, err)
	}
	return id
}

func insertCascadeAgent(t *testing.T, db database.Database, ownerID int, active bool) int {
	t.Helper()
	var id int
	err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active, is_agent, agent_owner_user_id)
		VALUES (?, ?, 'Cascade', 'Agent', ?, true, ?) RETURNING id
	`, "agent@example.test", "agent", active, ownerID).Scan(&id)
	if err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	return id
}

func insertCascadeToken(t *testing.T, db database.Database, userID int, hash string) int {
	t.Helper()
	var id int
	err := db.QueryRow(`
		INSERT INTO api_tokens (user_id, name, token_hash, token_prefix, permissions)
		VALUES (?, ?, ?, 'crw_', '["read"]') RETURNING id
	`, userID, "tok-"+hash, hash).Scan(&id)
	if err != nil {
		t.Fatalf("insert api_token: %v", err)
	}
	return id
}

func insertCascadeGroupMembership(t *testing.T, db database.Database, userID int) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO groups (name, scim_managed) VALUES ('cascade-group', true)
	`); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO group_members (group_id, user_id, scim_managed)
		SELECT id, ?, true FROM groups WHERE name = 'cascade-group'
	`, userID); err != nil {
		t.Fatalf("insert group membership: %v", err)
	}
}

func cascadeCount(t *testing.T, db database.Database, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func assertUserState(t *testing.T, db database.Database, userID int, wantActive, wantTombstoned bool) {
	t.Helper()
	var active, tombstoned bool
	if err := db.QueryRow(
		`SELECT COALESCE(is_active, false), scim_deleted_at IS NOT NULL FROM users WHERE id = ?`, userID,
	).Scan(&active, &tombstoned); err != nil {
		t.Fatalf("read user %d: %v", userID, err)
	}
	if active != wantActive {
		t.Fatalf("user %d is_active = %t, want %t", userID, active, wantActive)
	}
	if tombstoned != wantTombstoned {
		t.Fatalf("user %d tombstoned = %t, want %t", userID, tombstoned, wantTombstoned)
	}
}

func assertTokenGone(t *testing.T, db database.Database, tokenID int) {
	t.Helper()
	if n := cascadeCount(t, db, `SELECT COUNT(*) FROM api_tokens WHERE id = ?`, tokenID); n != 0 {
		t.Fatalf("api_token %d survived deprovisioning", tokenID)
	}
}

func TestSCIMTombstoneUserCascadesAgentsTokensAndMemberships(t *testing.T) {
	repo, tdb := newSCIMRepoForCascade(t)
	db := tdb.GetDatabase()

	ownerID := insertCascadeUser(t, db, "owner@example.test", true)
	agentID := insertCascadeAgent(t, db, ownerID, true)
	ownerToken := insertCascadeToken(t, db, ownerID, "hash-owner")
	agentToken := insertCascadeToken(t, db, agentID, "hash-agent")
	insertCascadeGroupMembership(t, db, ownerID)

	cascade, err := repo.TombstoneUser(ownerID)
	if err != nil {
		t.Fatalf("TombstoneUser: %v", err)
	}
	if !cascade.HasImpact() {
		t.Fatalf("expected cascade impact, got %+v", cascade)
	}
	if len(cascade.DeactivatedAgentIDs) != 1 || cascade.DeactivatedAgentIDs[0] != agentID {
		t.Fatalf("deactivated agent IDs = %v, want [%d]", cascade.DeactivatedAgentIDs, agentID)
	}
	if len(cascade.RevokedAPITokenIDs) != 2 {
		t.Fatalf("revoked token IDs = %v, want owner + agent tokens", cascade.RevokedAPITokenIDs)
	}

	assertUserState(t, db, ownerID, false, true)
	assertUserState(t, db, agentID, false, false)
	assertTokenGone(t, db, ownerToken)
	assertTokenGone(t, db, agentToken)
	if n := cascadeCount(t, db, `SELECT COUNT(*) FROM group_members WHERE user_id = ? AND scim_managed = true`, ownerID); n != 0 {
		t.Fatalf("scim group memberships survived tombstone: %d", n)
	}

	// Repeated calls are safe and make no further changes.
	cascadeAgain, err := repo.TombstoneUser(ownerID)
	if err != nil {
		t.Fatalf("repeated TombstoneUser: %v", err)
	}
	if cascadeAgain.HasImpact() {
		t.Fatalf("repeated tombstone must be a no-op, got %+v", cascadeAgain)
	}
}

func TestSCIMApplyUserPatchDeactivationCascadesAtomically(t *testing.T) {
	repo, tdb := newSCIMRepoForCascade(t)
	db := tdb.GetDatabase()

	ownerID := insertCascadeUser(t, db, "patcher@example.test", true)
	agentID := insertCascadeAgent(t, db, ownerID, true)
	ownerToken := insertCascadeToken(t, db, ownerID, "hash-patch-owner")
	agentToken := insertCascadeToken(t, db, agentID, "hash-patch-agent")

	active := false
	firstName := "Deactivated"
	cascade, err := repo.ApplyUserPatch(ownerID, SCIMUserPatch{
		SetActive:    &active,
		SetFirstName: &firstName,
	})
	if err != nil {
		t.Fatalf("ApplyUserPatch: %v", err)
	}
	if len(cascade.DeactivatedAgentIDs) != 1 || cascade.DeactivatedAgentIDs[0] != agentID {
		t.Fatalf("deactivated agent IDs = %v, want [%d]", cascade.DeactivatedAgentIDs, agentID)
	}
	if len(cascade.RevokedAPITokenIDs) != 2 {
		t.Fatalf("revoked token IDs = %v, want both", cascade.RevokedAPITokenIDs)
	}

	assertUserState(t, db, ownerID, false, false)
	var gotFirstName string
	if err := db.QueryRow(`SELECT first_name FROM users WHERE id = ?`, ownerID).Scan(&gotFirstName); err != nil {
		t.Fatalf("read first_name: %v", err)
	}
	if gotFirstName != firstName {
		t.Fatalf("first_name = %q, want %q", gotFirstName, firstName)
	}
	assertTokenGone(t, db, ownerToken)
	assertTokenGone(t, db, agentToken)
}

func TestSCIMApplyUserPatchFailureLeavesResourceUntouched(t *testing.T) {
	repo, tdb := newSCIMRepoForCascade(t)
	db := tdb.GetDatabase()

	ownerID := insertCascadeUser(t, db, "victim@example.test", true)
	insertCascadeUser(t, db, "collider@example.test", true)

	newName := "Should"
	conflict := "collider" // users.username is UNIQUE; insertCascadeUser derives it from the email
	if _, err := repo.ApplyUserPatch(ownerID, SCIMUserPatch{
		SetFirstName: &newName,
		SetUsername:  &conflict,
	}); err == nil {
		t.Fatal("expected the username conflict to fail the patch")
	}

	// RFC 7644 §3.5.2: a failed operation set must not be applied at all.
	assertUserState(t, db, ownerID, true, false)
	var gotName string
	if err := db.QueryRow(`SELECT first_name FROM users WHERE id = ?`, ownerID).Scan(&gotName); err != nil {
		t.Fatalf("read first_name: %v", err)
	}
	if gotName == newName {
		t.Fatal("partial patch state survived a failed patch: first_name was written")
	}
}
