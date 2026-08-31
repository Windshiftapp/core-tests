package services

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"windshift/internal/database"
	"windshift/internal/repository"
)

func openIdentityTestDB(t *testing.T) (database.Database, *repository.AgentSecurityRepository) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s/identity.db?mode=memory&cache=shared", t.TempDir())
	db, err := database.NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("init db: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (1, 'WS', 'WS', true)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (2, 'OTHER', 'OTHER', true)`); err != nil {
		t.Fatalf("seed workspace 2: %v", err)
	}
	return db, repository.NewAgentSecurityRepository(db)
}

func seedIdentityUser(t *testing.T, db database.Database, email, username, firstName, lastName string, isAgent bool, ownerID *int, isActive bool) int {
	t.Helper()
	var owner any
	if ownerID != nil {
		owner = *ownerID
	}
	res, err := db.Exec(
		`INSERT INTO users(email, username, first_name, last_name, is_agent, agent_owner_user_id, is_active) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		email, username, firstName, lastName, isAgent, owner, isActive,
	)
	if err != nil {
		t.Fatalf("seed user %s: %v", username, err)
	}
	id, _ := res.LastInsertId()
	return int(id)
}

func TestAgentActingIdentityService_OwnedAgentAccepted(t *testing.T) {
	ctx := context.Background()
	db, sec := openIdentityTestDB(t)
	creator := seedIdentityUser(t, db, "alice@example.com", "alice", "Alice", "Hu", false, nil, true)
	agent := seedIdentityUser(t, db, "alice-agent@agents.local", "alice-agent", "Alice", "Agent", true, &creator, true)

	svc, err := NewAgentActingIdentityService(NewUserReadService(db), sec)
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}
	id, err := svc.Resolve(ctx, creator, agent, 1)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if id.Kind != ActingIdentityKindAgent {
		t.Errorf("kind: want %q, got %q", ActingIdentityKindAgent, id.Kind)
	}
	if id.Name != "Alice Agent" {
		t.Errorf("git name: want %q, got %q", "Alice Agent", id.Name)
	}
}

func TestAgentActingIdentityService_AgentOwnedByOtherRejected(t *testing.T) {
	ctx := context.Background()
	db, sec := openIdentityTestDB(t)
	alice := seedIdentityUser(t, db, "alice@example.com", "alice", "Alice", "Hu", false, nil, true)
	bob := seedIdentityUser(t, db, "bob@example.com", "bob", "Bob", "Lin", false, nil, true)
	bobsAgent := seedIdentityUser(t, db, "bob-agent@agents.local", "bob-agent", "Bob", "Agent", true, &bob, true)

	svc, _ := NewAgentActingIdentityService(NewUserReadService(db), sec)
	_, err := svc.Resolve(ctx, alice, bobsAgent, 1)
	if !errors.Is(err, ErrActingIdentityNotOwned) {
		t.Errorf("err: want ErrActingIdentityNotOwned, got %v", err)
	}
}

func TestAgentActingIdentityService_NonAgentRejected(t *testing.T) {
	ctx := context.Background()
	db, sec := openIdentityTestDB(t)
	alice := seedIdentityUser(t, db, "alice@example.com", "alice", "Alice", "Hu", false, nil, true)
	bob := seedIdentityUser(t, db, "bob@example.com", "bob", "Bob", "Lin", false, nil, true)

	svc, _ := NewAgentActingIdentityService(NewUserReadService(db), sec)
	_, err := svc.Resolve(ctx, alice, bob, 1)
	if !errors.Is(err, ErrActingIdentityNotAgent) {
		t.Errorf("err: want ErrActingIdentityNotAgent, got %v", err)
	}
}

func TestAgentActingIdentityService_InactiveRejected(t *testing.T) {
	ctx := context.Background()
	db, sec := openIdentityTestDB(t)
	creator := seedIdentityUser(t, db, "alice@example.com", "alice", "Alice", "Hu", false, nil, true)
	agent := seedIdentityUser(t, db, "alice-agent@agents.local", "alice-agent", "Alice", "Agent", true, &creator, false)

	svc, _ := NewAgentActingIdentityService(NewUserReadService(db), sec)
	_, err := svc.Resolve(ctx, creator, agent, 1)
	if !errors.Is(err, ErrActingIdentityInactive) {
		t.Errorf("err: want ErrActingIdentityInactive, got %v", err)
	}
}

func TestAgentActingIdentityService_CentralizedFlagGated(t *testing.T) {
	ctx := context.Background()
	db, sec := openIdentityTestDB(t)
	alice := seedIdentityUser(t, db, "alice@example.com", "alice", "Alice", "Hu", false, nil, true)
	svc1 := seedIdentityUser(t, db, "svc1@agents.local", "svc1", "Svc", "One", true, nil, true) // service user (no owner)

	svc, _ := NewAgentActingIdentityService(NewUserReadService(db), sec)

	// Flag is false by default. Even with an allowlist row, the gate is
	// closed.
	if err := sec.AddAllowlistEntry(ctx, svc1, nil, &alice, "test"); err != nil {
		t.Fatalf("seed allowlist: %v", err)
	}
	_, err := svc.Resolve(ctx, alice, svc1, 1)
	if !errors.Is(err, ErrActingIdentityCentralizedGated) {
		t.Errorf("err with flag off: want ErrActingIdentityCentralizedGated, got %v", err)
	}

	// Flag on, but workspace-scoped allowlist for ws=2 only → ws=1 fails.
	if err := sec.SetAllowCentralizedServiceUsers(ctx, true); err != nil {
		t.Fatalf("flip flag: %v", err)
	}
	if _, err := sec.RemoveAllowlistEntry(ctx, svc1, nil); err != nil {
		t.Fatalf("clear allowlist: %v", err)
	}
	ws2 := 2
	if err := sec.AddAllowlistEntry(ctx, svc1, &ws2, &alice, "ws-2 only"); err != nil {
		t.Fatalf("add ws-2 grant: %v", err)
	}
	_, err = svc.Resolve(ctx, alice, svc1, 1)
	if !errors.Is(err, ErrActingIdentityNotInAllowlist) {
		t.Errorf("err on wrong workspace: want ErrActingIdentityNotInAllowlist, got %v", err)
	}

	// Same identity but ws=2 (matching allowlist) succeeds.
	id, err := svc.Resolve(ctx, alice, svc1, 2)
	if err != nil {
		t.Fatalf("resolve ws=2: %v", err)
	}
	if id.Kind != ActingIdentityKindCentralized {
		t.Errorf("kind: want %q, got %q", ActingIdentityKindCentralized, id.Kind)
	}

	// Switch to a NULL-workspace (any) grant — every workspace works.
	if _, err := sec.RemoveAllowlistEntry(ctx, svc1, &ws2); err != nil {
		t.Fatalf("remove ws-2 grant: %v", err)
	}
	if err := sec.AddAllowlistEntry(ctx, svc1, nil, &alice, "any-workspace"); err != nil {
		t.Fatalf("add any-grant: %v", err)
	}
	if _, err := svc.Resolve(ctx, alice, svc1, 1); err != nil {
		t.Errorf("any-workspace grant should resolve for ws=1: %v", err)
	}
	if _, err := svc.Resolve(ctx, alice, svc1, 2); err != nil {
		t.Errorf("any-workspace grant should resolve for ws=2: %v", err)
	}
}

func TestAgentActingIdentityService_ListCandidatesForBinding(t *testing.T) {
	ctx := context.Background()
	db, sec := openIdentityTestDB(t)

	// Two binding-creator users + their own agents, plus a third user
	// whose agent must never appear in alice's candidate list, plus a
	// centralized service user gated behind the global flag + allowlist.
	alice := seedIdentityUser(t, db, "alice@example.com", "alice", "Alice", "Hu", false, nil, true)
	bob := seedIdentityUser(t, db, "bob@example.com", "bob", "Bob", "Lin", false, nil, true)
	aliceAgent := seedIdentityUser(t, db, "alice-agent@agents.local", "alice-agent", "Alice", "Agent", true, &alice, true)
	bobAgent := seedIdentityUser(t, db, "bob-agent@agents.local", "bob-agent", "Bob", "Agent", true, &bob, true)
	inactiveAliceAgent := seedIdentityUser(t, db, "alice-inactive@agents.local", "alice-inactive", "Alice", "Old", true, &alice, false)
	svc1 := seedIdentityUser(t, db, "svc1@agents.local", "svc1", "Svc", "One", true, nil, true)

	svc, err := NewAgentActingIdentityService(NewUserReadService(db), sec)
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}

	// Flag off: nothing is bindable. Owned/personal agents are never
	// offered in the picker (only global service users are), and
	// centralized service users require the master flag. So alice's own
	// agent, bob's agent, the inactive agent, and the gated service user
	// are all excluded.
	cands, err := svc.ListCandidatesForBinding(ctx, alice, 1)
	if err != nil {
		t.Fatalf("list candidates (flag off): %v", err)
	}
	if len(cands) != 0 {
		t.Errorf("flag off: want no candidates, got %+v", cands)
	}

	// Flag on but no allowlist row for ws=1: still nothing — owned agents
	// remain excluded and the service user isn't allowlisted yet.
	if err := sec.SetAllowCentralizedServiceUsers(ctx, true); err != nil {
		t.Fatalf("flip flag: %v", err)
	}
	cands, err = svc.ListCandidatesForBinding(ctx, alice, 1)
	if err != nil {
		t.Fatalf("list candidates (flag on, no allowlist): %v", err)
	}
	if len(cands) != 0 {
		t.Errorf("flag on, no allowlist: want no candidates, got %+v", cands)
	}

	// Flag on + allowlist row for any workspace: the centralized service
	// user surfaces — and it's the ONLY candidate. Owned/personal agents
	// (alice's, bob's, the inactive one) are never offered.
	if err := sec.AddAllowlistEntry(ctx, svc1, nil, &alice, "test"); err != nil {
		t.Fatalf("add allowlist: %v", err)
	}
	cands, err = svc.ListCandidatesForBinding(ctx, alice, 1)
	if err != nil {
		t.Fatalf("list candidates (flag on + allowlist): %v", err)
	}
	got := candidateByID(cands, svc1)
	if got == nil {
		t.Fatalf("expected centralized service user (%d) in candidates, got %+v", svc1, cands)
	}
	if got.Kind != ActingIdentityKindCentralized {
		t.Errorf("centralized candidate kind: want %q, got %q", ActingIdentityKindCentralized, got.Kind)
	}
	if got.OwnerID != nil {
		t.Errorf("centralized candidate must have no owner_id, got %v", got.OwnerID)
	}
	if containsUser(cands, aliceAgent) {
		t.Errorf("owned agent (%d) must never be offered in the picker", aliceAgent)
	}
	if containsUser(cands, bobAgent) {
		t.Errorf("bob's agent (%d) leaked into candidates", bobAgent)
	}
	if containsUser(cands, inactiveAliceAgent) {
		t.Errorf("inactive agent (%d) leaked into candidates", inactiveAliceAgent)
	}
	if len(cands) != 1 {
		t.Errorf("want exactly one candidate (the service user), got %+v", cands)
	}
}

func containsUser(cands []CandidateActingIdentity, userID int) bool {
	for i := range cands {
		if cands[i].UserID == userID {
			return true
		}
	}
	return false
}

func candidateByID(cands []CandidateActingIdentity, userID int) *CandidateActingIdentity {
	for i := range cands {
		if cands[i].UserID == userID {
			return &cands[i]
		}
	}
	return nil
}

func TestAgentActingIdentityService_UnknownUserNotFound(t *testing.T) {
	ctx := context.Background()
	db, sec := openIdentityTestDB(t)
	alice := seedIdentityUser(t, db, "alice@example.com", "alice", "Alice", "Hu", false, nil, true)

	svc, _ := NewAgentActingIdentityService(NewUserReadService(db), sec)
	_, err := svc.Resolve(ctx, alice, 99999, 1)
	if !errors.Is(err, ErrActingIdentityNotFound) {
		t.Errorf("err: want ErrActingIdentityNotFound, got %v", err)
	}
}
